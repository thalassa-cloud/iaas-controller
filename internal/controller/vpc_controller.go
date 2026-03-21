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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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

const vpcFinalizer = "iaas.controllers.thalassa.cloud/vpc"

// VPCReconciler reconciles a VPC object
type VPCReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
	Recorder   record.EventRecorder
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs/finalizers,verbs=update

// Reconcile moves the current state of a VPC toward the desired spec by creating or updating the resource in Thalassa IaaS.
func (r *VPCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var vpc iaasv1.VPC
	if err := r.Get(ctx, req.NamespacedName, &vpc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&vpc) {
		return ctrl.Result{}, nil
	}

	vpc.Status.ObservedGeneration, vpc.Status.LastReconcileTime = ReconcileMeta(vpc.Generation)

	// Handle deletion
	if !vpc.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &vpc)
	}
	// Reference to an already-provisioned VPC: adopt by ResourceID, do not create/update/delete in Thalassa
	if vpc.Spec.ResourceID != "" {
		return r.reconcileVPCExternalReference(ctx, &vpc)
	}

	// Require Region and CIDRBlocks when not using ResourceID
	if vpc.Spec.Region == "" || len(vpc.Spec.CIDRBlocks) == 0 {
		err := fmt.Errorf("spec.region and spec.cidrBlocks are required when spec.vpcRef is not set")
		return r.setVpcErrorCondition(ctx, &vpc, "Reconcile", "InvalidSpec", err.Error(), err)
	}
	if controllerutil.AddFinalizer(&vpc, vpcFinalizer) {
		if err := r.Update(ctx, &vpc); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}
	return r.reconcileVPC(ctx, vpc)
}

func (r *VPCReconciler) createVpc(ctx context.Context, vpc iaasv1.VPC) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createVpc")

	SetStandardConditions(&vpc.Status.Conditions, ConditionStateProgressing, "Creating", "Creating VPC in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	created, err := r.IaaSClient.CreateVpc(ctx, thalassaiaas.CreateVpc{
		Name:                effectiveName(vpc.Name, vpc.Spec.Metadata),
		Description:         vpc.Spec.Description,
		Labels:              effectiveLabels(vpc.Spec.Metadata),
		CloudRegionIdentity: vpc.Spec.Region,
		VpcCidrs:            vpc.Spec.CIDRBlocks,
	})
	if err != nil {
		return r.setVpcErrorCondition(ctx, &vpc, "createVpc", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	vpc.Status.ResourceID = identity
	vpc.Status.ResourceStatus = created.Status
	vpc.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	if err := r.IaaSClient.WaitUntilVpcIsReady(ctx, identity); err != nil {
		// log error but don't fail, we will retry later
		log.Error(err, "failed to wait until VPC is ready, will retry later")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	log.Info("created VPC in Thalassa", "identity", identity)

	// update the status
	SetStandardConditions(&vpc.Status.Conditions, ConditionStateAvailable, "Created", "VPC is created in Thalassa")
	vpc.Status.ResourceStatus = created.Status
	vpc.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	r.Recorder.Eventf(&vpc, corev1.EventTypeNormal, "Provisioned", "VPC provisioned in Thalassa (id: %s)", identity)
	return ctrl.Result{}, nil
}

func (r *VPCReconciler) reconcileVPC(ctx context.Context, vpc iaasv1.VPC) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileVPC")
	// Create or update in Thalassa
	identity := vpc.Status.ResourceID
	if identity == "" {
		return r.createVpc(ctx, vpc)
	}

	// fetch the VPC from Thalassa
	fetched, err := r.IaaSClient.GetVpc(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&vpc.Status.Conditions, ConditionStateDegraded, "NotFound", "VPC not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setVpcErrorCondition(ctx, &vpc, "reconcileVPC", "NotFound", "VPC not found in Thalassa", err)
		}
		return r.setVpcErrorCondition(ctx, &vpc, "reconcileVPC", "GetFailed", "Failed to get VPC from Thalassa", err)
	}

	// sync the status
	if vpc.Status.ResourceStatus != fetched.Status {
		vpc.Status.ResourceStatus = fetched.Status
		vpc.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	// check if we require updating the VPC spec
	if r.requiresUpdate(&vpc, fetched) {
		log.Info("updating VPC in Thalassa", "identity", identity)
		SetStandardConditions(&vpc.Status.Conditions, ConditionStateProgressing, "Updating", "Updating VPC in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		updated, err := r.IaaSClient.UpdateVpc(ctx, identity, thalassaiaas.UpdateVpc{
			Name:        effectiveName(vpc.Name, vpc.Spec.Metadata),
			Description: vpc.Spec.Description,
			Labels:      effectiveLabels(vpc.Spec.Metadata),
			VpcCidrs:    vpc.Spec.CIDRBlocks,
		})
		if err != nil {
			log.Error(err, "failed to update VPC in Thalassa")
			return ctrl.Result{}, err
		}
		vpc.Status.ResourceStatus = updated.Status
		vpc.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		// wait until the VPC is ready
		if err := r.IaaSClient.WaitUntilVpcIsReady(ctx, identity); err != nil {
			log.Error(err, "failed to wait until VPC is ready")
			return ctrl.Result{}, err
		}
		fetched, err = r.IaaSClient.GetVpc(ctx, identity)
		if err != nil {
			log.Error(err, "failed to get VPC from Thalassa")
			return ctrl.Result{}, err
		}
		vpc.Status.ResourceStatus = fetched.Status
		if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	SetStandardConditions(&vpc.Status.Conditions, ConditionStateAvailable, "Synced", "VPC is synced with Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &vpc); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

// reconcileVPCExternalReference adopts an already-provisioned VPC by spec.ResourceID. No create/update/delete in Thalassa.
func (r *VPCReconciler) reconcileVPCExternalReference(ctx context.Context, vpc *iaasv1.VPC) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileVPCExternalReference")
	// fetch the VPC from Thalassa
	fetched, err := r.IaaSClient.GetVpc(ctx, vpc.Spec.ResourceID)
	if err != nil {
		log.Error(err, "failed to get VPC from Thalassa")
		return ctrl.Result{}, err
	}
	vpc.Status.ResourceID = vpc.Spec.ResourceID
	vpc.Status.LastReconcileError = ""
	if vpc.Status.ResourceStatus != fetched.Status { // sync the status
		vpc.Status.ResourceStatus = fetched.Status
		if updateErr := r.updateStatusWithRetry(ctx, vpc); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	// set to ready
	if meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Synced", Message: ""}) {
		if updateErr := r.updateStatusWithRetry(ctx, vpc); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	return ctrl.Result{}, nil
}

func (r *VPCReconciler) reconcileDelete(ctx context.Context, vpc *iaasv1.VPC) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	if !controllerutil.ContainsFinalizer(vpc, vpcFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := vpc.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteVpc(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			log.Error(err, "failed to delete VPC in Thalassa")
			return ctrl.Result{}, err
		}
		if err := r.IaaSClient.WaitUntilVpcIsDeleted(ctx, identity); err != nil {
			log.Error(err, "failed waiting for VPC deletion")
			return ctrl.Result{}, err
		}
		log.Info("deleted VPC in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(vpc, vpcFinalizer) {
		if err := r.Update(ctx, vpc); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(vpc, corev1.EventTypeNormal, "Deleted", "Finished deletion")
	}
	return ctrl.Result{}, nil
}

// updateStatusWithRetry fetches the latest VPC, copies status, and updates using retry.OnError.
func (r *VPCReconciler) updateStatusWithRetry(ctx context.Context, vpc *iaasv1.VPC) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.VPC{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: vpc.Namespace, Name: vpc.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest VPC: %w", err)
		}
		latest.Status = vpc.Status
		return r.Status().Update(ctx, latest)
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *VPCReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("iaas-controller.vpc")
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.VPC{}).
		Named("vpc").
		WithOptions(controller.Options{
			// MaxConcurrentReconciles: r.MaxConcurrentReconciles,
			RateLimiter: workqueue.NewTypedItemFastSlowRateLimiter[reconcile.Request](1*time.Second, 10*time.Second, 15),
		}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// The reconciler adds a finalizer so we perform clean-up
				// when the delete timestamp is added
				// Suppress Delete events to avoid filtering them out in the Reconcile function
				return false
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldGeneration := e.ObjectOld.GetGeneration()
				newGeneration := e.ObjectNew.GetGeneration()
				// Generation is only updated on spec changes (also on deletion), not metadata or status
				// Filter out events where the generation hasn't changed to avoid being triggered by status updates
				return oldGeneration != newGeneration
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return false
			},
		}).
		Complete(r)
}

func (r *VPCReconciler) setVpcErrorCondition(ctx context.Context, vpc *iaasv1.VPC, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&vpc.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		vpc.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, vpc); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		r.Recorder.Eventf(vpc, corev1.EventTypeWarning, reason, "%s", message)
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *VPCReconciler) requiresUpdate(vpc *iaasv1.VPC, fetched *thalassaiaas.Vpc) bool {
	if effectiveName(vpc.Name, vpc.Spec.Metadata) != fetched.Name {
		return true
	}
	// use semantic deep equal to compare the labels
	if !equality.Semantic.DeepEqual(effectiveLabels(vpc.Spec.Metadata), fetched.Labels) {
		return true
	}
	if vpc.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(vpc.Spec.CIDRBlocks, fetched.CIDRs) {
		return true
	}
	return false
}
