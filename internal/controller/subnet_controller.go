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

const subnetFinalizer = "iaas.controllers.thalassa.cloud/subnet"

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=subnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=subnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=subnets/finalizers,verbs=update

// Reconcile moves the current state of a Subnet toward the desired spec by creating or updating the resource in Thalassa IaaS.
func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var subnet iaasv1.Subnet
	if err := r.Get(ctx, req.NamespacedName, &subnet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&subnet) {
		return ctrl.Result{}, nil
	}

	subnet.Status.ObservedGeneration, subnet.Status.LastReconcileTime = ReconcileMeta(subnet.Generation)

	// Handle deletion
	if !subnet.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &subnet)
	}
	// Reference to an already-provisioned subnet: adopt by ResourceID, do not create/update/delete in Thalassa
	if subnet.Spec.ResourceID != "" {
		return r.reconcileSubnetExternalReference(ctx, &subnet)
	}

	// Require VPCRef and CIDR when not using ResourceID
	if subnet.Spec.VPCRef.Name == "" || subnet.Spec.CIDR == "" {
		err := errors.New("spec.vpcRef.name and spec.cidr are required when spec.resourceId is not set")
		return r.setSubnetErrorCondition(ctx, &subnet, "Reconcile", "InvalidSpec", err.Error(), err)
	}
	if controllerutil.AddFinalizer(&subnet, subnetFinalizer) {
		if err := r.Update(ctx, &subnet); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	vpcIdentity, err := r.resolveVPCRef(ctx, subnet.Namespace, subnet.Spec.VPCRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setSubnetErrorCondition(ctx, &subnet, "Reconcile", "VPCNotFound", err.Error(), err)
	}
	return r.reconcileSubnet(ctx, subnet, vpcIdentity)
}

func (r *SubnetReconciler) createSubnet(ctx context.Context, subnet iaasv1.Subnet, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	SetStandardConditions(&subnet.Status.Conditions, ConditionStateProgressing, "Creating", "Creating subnet in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	created, err := r.IaaSClient.CreateSubnet(ctx, thalassaiaas.CreateSubnet{
		Name:        effectiveName(subnet.Name, subnet.Spec.Metadata),
		Description: subnet.Spec.Description,
		Labels:      effectiveLabels(subnet.Spec.Metadata),
		VpcIdentity: vpcIdentity,
		Cidr:        subnet.Spec.CIDR,
	})
	if err != nil {
		return r.setSubnetErrorCondition(ctx, &subnet, "createSubnet", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	subnet.Status.ResourceID = identity
	subnet.Status.ResourceStatus = string(created.Status)
	subnet.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	if _, err := r.IaaSClient.WaitUntilSubnetReady(ctx, identity); err != nil {
		log.Error(err, "failed to wait until subnet is ready, will retry later")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	log.Info("created subnet in Thalassa", "identity", identity)

	SetStandardConditions(&subnet.Status.Conditions, ConditionStateAvailable, "Created", "Subnet is created in Thalassa")
	subnet.Status.ResourceStatus = string(created.Status)
	subnet.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *SubnetReconciler) reconcileSubnet(ctx context.Context, subnet iaasv1.Subnet, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	identity := subnet.Status.ResourceID
	if identity == "" {
		return r.createSubnet(ctx, subnet, vpcIdentity)
	}

	fetched, err := r.IaaSClient.GetSubnet(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&subnet.Status.Conditions, ConditionStateDegraded, "NotFound", "Subnet not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setSubnetErrorCondition(ctx, &subnet, "reconcileSubnet", "NotFound", "Subnet not found in Thalassa", err)
		}
		return r.setSubnetErrorCondition(ctx, &subnet, "reconcileSubnet", "GetFailed", "Failed to get subnet from Thalassa", err)
	}

	// Sync status
	if subnet.Status.ResourceStatus != string(fetched.Status) {
		subnet.Status.ResourceStatus = string(fetched.Status)
		subnet.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	if r.requiresUpdate(&subnet, fetched) {
		log.Info("updating subnet in Thalassa", "identity", identity)
		SetStandardConditions(&subnet.Status.Conditions, ConditionStateProgressing, "Updating", "Updating subnet in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		updated, err := r.IaaSClient.UpdateSubnet(ctx, identity, thalassaiaas.UpdateSubnet{
			Name:        effectiveName(subnet.Name, subnet.Spec.Metadata),
			Description: subnet.Spec.Description,
			Labels:      effectiveLabels(subnet.Spec.Metadata),
		})
		if err != nil {
			return r.setSubnetErrorCondition(ctx, &subnet, "reconcileSubnet", "UpdateFailed", err.Error(), err)
		}
		subnet.Status.ResourceStatus = string(updated.Status)
		subnet.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		fetched, err = r.IaaSClient.GetSubnet(ctx, identity)
		if err != nil {
			return r.setSubnetErrorCondition(ctx, &subnet, "reconcileSubnet", "GetFailed", "Failed to get subnet after update", err)
		}
		subnet.Status.ResourceStatus = string(fetched.Status)
		if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	SetStandardConditions(&subnet.Status.Conditions, ConditionStateAvailable, "Synced", "Subnet is synced with Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &subnet); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

// reconcileSubnetExternalReference adopts an already-provisioned subnet by spec.ResourceID. No create/update/delete in Thalassa.
func (r *SubnetReconciler) reconcileSubnetExternalReference(ctx context.Context, subnet *iaasv1.Subnet) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileSubnetExternalReference")
	fetched, err := r.IaaSClient.GetSubnet(ctx, subnet.Spec.ResourceID)
	if err != nil {
		log.Error(err, "failed to get subnet from Thalassa")
		return ctrl.Result{}, err
	}
	subnet.Status.ResourceID = subnet.Spec.ResourceID
	subnet.Status.LastReconcileError = ""
	if subnet.Status.ResourceStatus != string(fetched.Status) {
		subnet.Status.ResourceStatus = string(fetched.Status)
		if updateErr := r.updateStatusWithRetry(ctx, subnet); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	if meta.SetStatusCondition(&subnet.Status.Conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Synced", Message: ""}) {
		if updateErr := r.updateStatusWithRetry(ctx, subnet); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	return ctrl.Result{}, nil
}

func (r *SubnetReconciler) reconcileDelete(ctx context.Context, subnet *iaasv1.Subnet) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(subnet, subnetFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := subnet.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteSubnet(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			log.Error(err, "failed to delete subnet in Thalassa")
			return ctrl.Result{}, err
		}
		if err := r.IaaSClient.WaitUntilSubnetDeleted(ctx, identity); err != nil {
			log.Error(err, "failed waiting for subnet deletion")
			return ctrl.Result{}, err
		}
		log.Info("deleted subnet in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(subnet, subnetFinalizer) {
		if err := r.Update(ctx, subnet); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// updateStatusWithRetry fetches the latest Subnet, copies status, and updates using retry.OnError.
func (r *SubnetReconciler) updateStatusWithRetry(ctx context.Context, subnet *iaasv1.Subnet) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.Subnet{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: subnet.Namespace, Name: subnet.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest Subnet: %w", err)
		}
		latest.Status = subnet.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *SubnetReconciler) setSubnetErrorCondition(ctx context.Context, subnet *iaasv1.Subnet, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&subnet.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		subnet.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, subnet); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *SubnetReconciler) requiresUpdate(subnet *iaasv1.Subnet, fetched *thalassaiaas.Subnet) bool {
	if effectiveName(subnet.Name, subnet.Spec.Metadata) != fetched.Name {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(subnet.Spec.Metadata), fetched.Labels) {
		return true
	}
	if subnet.Spec.Description != fetched.Description {
		return true
	}
	return false
}

// resolveVPCRef returns the Thalassa VPC identity for the given VPCRef.
func (r *SubnetReconciler) resolveVPCRef(ctx context.Context, defaultNamespace string, ref iaasv1.VPCRef) (string, error) {
	if ref.Identity != "" {
		return ref.Identity, nil
	}
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	var vpc iaasv1.VPC
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &vpc); err != nil {
		return "", err
	}
	if vpc.Status.ResourceID == "" {
		return "", ErrDependencyNotReady
	}
	return vpc.Status.ResourceID, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.Subnet{}).
		Named("subnet").
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
