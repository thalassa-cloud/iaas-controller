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
	"errors"
	"fmt"
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

const snapshotFinalizer = "iaas.controllers.thalassa.cloud/snapshot"

// SnapshotReconciler reconciles a Snapshot object.
type SnapshotReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=blockvolumes,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=snapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=snapshots/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=snapshots/finalizers,verbs=update

// Reconcile moves the current state of a Snapshot toward the desired spec by creating or updating the snapshot in Thalassa IaaS.
func (r *SnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var snap iaasv1.Snapshot
	if err := r.Get(ctx, req.NamespacedName, &snap); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&snap) {
		return ctrl.Result{}, nil
	}

	snap.Status.ObservedGeneration, snap.Status.LastReconcileTime = ReconcileMeta(snap.Generation)

	if !snap.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &snap)
	}
	if controllerutil.AddFinalizer(&snap, snapshotFinalizer) {
		if err := r.Update(ctx, &snap); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	volumeIdentity, err := r.resolveVolumeRef(ctx, snap.Namespace, snap.Spec.VolumeRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setSnapshotErrorCondition(ctx, &snap, "Reconcile", "VolumeNotFound", err.Error(), err)
	}
	return r.reconcileSnapshot(ctx, snap, volumeIdentity)
}

func (r *SnapshotReconciler) createSnapshot(ctx context.Context, snap iaasv1.Snapshot, volumeIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createSnapshot")

	SetStandardConditions(&snap.Status.Conditions, ConditionStateProgressing, "Creating", "Creating snapshot in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &snap); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	createReq := thalassaiaas.CreateSnapshotRequest{
		Name:             effectiveName(snap.Name, snap.Spec.Metadata),
		Description:      snap.Spec.Description,
		Labels:           effectiveLabels(snap.Spec.Metadata),
		VolumeIdentity:   volumeIdentity,
		DeleteProtection: snap.Spec.DeleteProtection,
	}
	created, err := r.IaaSClient.CreateSnapshot(ctx, createReq)
	if err != nil {
		return r.setSnapshotErrorCondition(ctx, &snap, "createSnapshot", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	snap.Status.ResourceID = identity
	snap.Status.ResourceStatus = string(created.Status)
	snap.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &snap); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	if err := r.IaaSClient.WaitUntilSnapshotIsAvailable(ctx, identity); err != nil {
		log.Error(err, "failed to wait until snapshot is available, will retry later")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	log.Info("created snapshot in Thalassa", "identity", identity)

	fetched, err := r.IaaSClient.GetSnapshot(ctx, identity)
	if err != nil {
		return r.setSnapshotErrorCondition(ctx, &snap, "createSnapshot", "GetFailed", "Failed to get snapshot after create", err)
	}
	snap.Status.ResourceStatus = string(fetched.Status)
	SetStandardConditions(&snap.Status.Conditions, ConditionStateAvailable, "Created", "Snapshot is created in Thalassa")
	snap.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &snap); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *SnapshotReconciler) reconcileSnapshot(ctx context.Context, snap iaasv1.Snapshot, volumeIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileSnapshot")
	identity := snap.Status.ResourceID
	if identity == "" {
		return r.createSnapshot(ctx, snap, volumeIdentity)
	}

	fetched, err := r.IaaSClient.GetSnapshot(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&snap.Status.Conditions, ConditionStateDegraded, "NotFound", "Snapshot not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &snap); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setSnapshotErrorCondition(ctx, &snap, "reconcileSnapshot", "NotFound", "Snapshot not found in Thalassa", err)
		}
		return r.setSnapshotErrorCondition(ctx, &snap, "reconcileSnapshot", "GetFailed", "Failed to get snapshot from Thalassa", err)
	}

	snap.Status.ResourceStatus = string(fetched.Status)
	if r.requiresUpdate(&snap, fetched) {
		SetStandardConditions(&snap.Status.Conditions, ConditionStateProgressing, "Updating", "Updating snapshot in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &snap); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		_, err = r.IaaSClient.UpdateSnapshot(ctx, identity, thalassaiaas.UpdateSnapshotRequest{
			Name:             effectiveName(snap.Name, snap.Spec.Metadata),
			Description:      snap.Spec.Description,
			Labels:           effectiveLabels(snap.Spec.Metadata),
			DeleteProtection: snap.Spec.DeleteProtection,
		})
		if err != nil {
			return r.setSnapshotErrorCondition(ctx, &snap, "reconcileSnapshot", "FailedUpdate", err.Error(), err)
		}
		fetched, err = r.IaaSClient.GetSnapshot(ctx, identity)
		if err != nil {
			return r.setSnapshotErrorCondition(ctx, &snap, "reconcileSnapshot", "GetFailed", "Failed to get snapshot after update", err)
		}
		snap.Status.ResourceStatus = string(fetched.Status)
	}

	SetStandardConditions(&snap.Status.Conditions, ConditionStateAvailable, "Reconciled", "Snapshot is up to date")
	snap.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &snap); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *SnapshotReconciler) reconcileDelete(ctx context.Context, snap *iaasv1.Snapshot) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	identity := snap.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteSnapshot(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			return r.setSnapshotErrorCondition(ctx, snap, "reconcileDelete", "DeleteFailed", err.Error(), err)
		}
		if err := r.IaaSClient.WaitUntilSnapshotIsDeleted(ctx, identity); err != nil {
			log.Error(err, "failed to wait until snapshot is deleted, will retry")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
		}
	}
	if controllerutil.RemoveFinalizer(snap, snapshotFinalizer) {
		if err := r.Update(ctx, snap); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *SnapshotReconciler) updateStatusWithRetry(ctx context.Context, snap *iaasv1.Snapshot) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.Snapshot{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: snap.Namespace, Name: snap.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest Snapshot: %w", err)
		}
		latest.Status = snap.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *SnapshotReconciler) setSnapshotErrorCondition(ctx context.Context, snap *iaasv1.Snapshot, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&snap.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		snap.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, snap); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *SnapshotReconciler) requiresUpdate(snap *iaasv1.Snapshot, fetched *thalassaiaas.Snapshot) bool {
	if effectiveName(snap.Name, snap.Spec.Metadata) != fetched.Name {
		return true
	}
	if snap.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(snap.Spec.Metadata), fetched.Labels) {
		return true
	}
	if snap.Spec.DeleteProtection != fetched.DeleteProtection {
		return true
	}
	return false
}

func (r *SnapshotReconciler) resolveVolumeRef(ctx context.Context, defaultNamespace string, ref iaasv1.VolumeRef) (string, error) {
	if ref.Id != "" {
		return ref.Id, nil
	}
	if ref.Name == "" {
		return "", errors.New("volumeRef must have identity or name set")
	}
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	var bv iaasv1.BlockVolume
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &bv); err != nil {
		return "", err
	}
	if bv.Status.ResourceID == "" {
		return "", ErrDependencyNotReady
	}
	return bv.Status.ResourceID, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.Snapshot{}).
		Named("snapshot").
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
