/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	thalassaiaas "github.com/thalassa-cloud/client-go/iaas"
	thalassaclient "github.com/thalassa-cloud/client-go/pkg/client"
	iaasv1 "github.com/thalassa-cloud/iaas-controller/api/v1"
)

const blockvolumeFinalizer = "iaas.controllers.thalassa.cloud/blockvolume"

// BlockVolumeReconciler reconciles a BlockVolume object.
type BlockVolumeReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=blockvolumes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=blockvolumes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=blockvolumes/finalizers,verbs=update

// Reconcile moves the current state of a BlockVolume toward the desired spec by creating or updating the volume in Thalassa IaaS.
func (r *BlockVolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var bv iaasv1.BlockVolume
	if err := r.Get(ctx, req.NamespacedName, &bv); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&bv) {
		return ctrl.Result{}, nil
	}

	bv.Status.ObservedGeneration, bv.Status.LastReconcileTime = ReconcileMeta(bv.Generation)

	if !bv.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &bv)
	}
	if controllerutil.AddFinalizer(&bv, blockvolumeFinalizer) {
		if err := r.Update(ctx, &bv); err != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	return r.reconcileBlockVolume(ctx, bv)
}

func (r *BlockVolumeReconciler) createBlockVolume(ctx context.Context, bv iaasv1.BlockVolume) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	SetStandardConditions(&bv.Status.Conditions, ConditionStateProgressing, "Creating", "Creating block volume in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &bv); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	volType := bv.Spec.Type
	if volType == "" {
		volType = "block"
	}
	volumeTypeId := bv.Spec.VolumeTypeId

	// search for volume type in Thalassa
	volumeTypes, err := r.IaaSClient.ListVolumeTypes(ctx, &thalassaiaas.ListVolumeTypesRequest{})
	if err != nil {
		return r.setBlockVolumeErrorCondition(ctx, &bv, "FailedListVolumeTypes", err.Error(), err)
	}
	for _, vt := range volumeTypes {
		if vt.Identity == volumeTypeId || strings.EqualFold(vt.Name, volumeTypeId) {
			volumeTypeId = vt.Identity
			break
		}
	}
	if volumeTypeId == "" {
		return r.setBlockVolumeErrorCondition(ctx, &bv, "FailedFindVolumeType", "Volume type not found in Thalassa", fmt.Errorf("volume type %s not found in Thalassa", volumeTypeId))
	}

	createReq := thalassaiaas.CreateVolume{
		Name:                  effectiveName(bv.Name, bv.Spec.Metadata),
		Description:           bv.Spec.Description,
		Labels:                effectiveLabels(bv.Spec.Metadata),
		Type:                  volType,
		Size:                  int(bv.Spec.Size),
		CloudRegionIdentity:   bv.Spec.Region,
		VolumeTypeIdentity:    volumeTypeId,
		DeleteProtection:      bv.Spec.DeleteProtection,
		RestoreFromSnapshotId: bv.Spec.RestoreFromSnapshotIdentity,
	}
	created, err := r.IaaSClient.CreateVolume(ctx, createReq)
	if err != nil {
		return r.setBlockVolumeErrorCondition(ctx, &bv, "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	bv.Status.ResourceID = identity
	bv.Status.ResourceStatus = created.Status
	bv.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &bv); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	if err := r.IaaSClient.WaitUntilVolumeIsAvailable(ctx, identity); err != nil {
		log.Error(err, "failed to wait until volume is available, will retry later")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	log.Info("created block volume in Thalassa", "identity", identity)

	fetched, err := r.IaaSClient.GetVolume(ctx, identity)
	if err != nil {
		return r.setBlockVolumeErrorCondition(ctx, &bv, "GetFailed", "Failed to get volume after create", err)
	}
	bv.Status.ResourceStatus = fetched.Status
	SetStandardConditions(&bv.Status.Conditions, ConditionStateAvailable, "Created", "Block volume is created in Thalassa")
	bv.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &bv); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *BlockVolumeReconciler) reconcileBlockVolume(ctx context.Context, bv iaasv1.BlockVolume) (ctrl.Result, error) {
	identity := bv.Status.ResourceID
	if identity == "" {
		return r.createBlockVolume(ctx, bv)
	}

	fetched, err := r.IaaSClient.GetVolume(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&bv.Status.Conditions, ConditionStateDegraded, "NotFound", "Volume not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &bv); updateErr != nil {
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setBlockVolumeErrorCondition(ctx, &bv, "NotFound", "Volume not found in Thalassa", err)
		}
		return r.setBlockVolumeErrorCondition(ctx, &bv, "GetFailed", "Failed to get volume from Thalassa", err)
	}

	bv.Status.ResourceStatus = fetched.Status
	if r.requiresUpdate(&bv, fetched) {
		SetStandardConditions(&bv.Status.Conditions, ConditionStateProgressing, "Updating", "Updating block volume in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &bv); updateErr != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		_, err = r.IaaSClient.UpdateVolume(ctx, identity, thalassaiaas.UpdateVolume{
			Name:             effectiveName(bv.Name, bv.Spec.Metadata),
			Description:      bv.Spec.Description,
			Labels:           effectiveLabels(bv.Spec.Metadata),
			Size:             int(bv.Spec.Size),
			DeleteProtection: bv.Spec.DeleteProtection,
		})
		if err != nil {
			return r.setBlockVolumeErrorCondition(ctx, &bv, "FailedUpdate", err.Error(), err)
		}
		fetched, err = r.IaaSClient.GetVolume(ctx, identity)
		if err != nil {
			return r.setBlockVolumeErrorCondition(ctx, &bv, "GetFailed", "Failed to get volume after update", err)
		}
		bv.Status.ResourceStatus = fetched.Status
	}

	SetStandardConditions(&bv.Status.Conditions, ConditionStateAvailable, "Reconciled", "Block volume is up to date")
	bv.Status.LastReconcileError = ""
	return ctrl.Result{}, r.updateStatusWithRetry(ctx, &bv)
}

func (r *BlockVolumeReconciler) reconcileDelete(ctx context.Context, bv *iaasv1.BlockVolume) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	identity := bv.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteVolume(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			return r.setBlockVolumeErrorCondition(ctx, bv, "DeleteFailed", err.Error(), err)
		}
		if err := r.IaaSClient.WaitUntilVolumeIsDeleted(ctx, identity); err != nil {
			log.Error(err, "failed to wait until volume is deleted, will retry")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
		}
	}
	if controllerutil.RemoveFinalizer(bv, blockvolumeFinalizer) {
		if err := r.Update(ctx, bv); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *BlockVolumeReconciler) updateStatusWithRetry(ctx context.Context, bv *iaasv1.BlockVolume) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.BlockVolume{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: bv.Namespace, Name: bv.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest BlockVolume: %w", err)
		}
		latest.Status = bv.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *BlockVolumeReconciler) setBlockVolumeErrorCondition(ctx context.Context, bv *iaasv1.BlockVolume, reason, message string, err error) (ctrl.Result, error) {
	if meta.SetStatusCondition(&bv.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		bv.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, bv); updateErr != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *BlockVolumeReconciler) requiresUpdate(bv *iaasv1.BlockVolume, fetched *thalassaiaas.Volume) bool {
	if effectiveName(bv.Name, bv.Spec.Metadata) != fetched.Name {
		return true
	}
	if bv.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(bv.Spec.Metadata), fetched.Labels) {
		return true
	}
	if int(bv.Spec.Size) != fetched.Size {
		return true
	}
	if bv.Spec.DeleteProtection != fetched.DeleteProtection {
		return true
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *BlockVolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.BlockVolume{}).
		Named("blockvolume").
		WithOptions(controller.Options{
			RateLimiter: workqueue.NewTypedItemFastSlowRateLimiter[reconcile.Request](1*time.Second, 10*time.Second, 15),
		}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldGeneration := e.ObjectOld.GetGeneration()
				newGeneration := e.ObjectNew.GetGeneration()
				return oldGeneration != newGeneration
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		}).
		Complete(r)
}
