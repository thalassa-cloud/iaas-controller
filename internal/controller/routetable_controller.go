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

const routeTableFinalizer = "iaas.controllers.thalassa.cloud/routetable"

// RouteTableReconciler reconciles a RouteTable object
type RouteTableReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=routetables,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=routetables/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=routetables/finalizers,verbs=update

// Reconcile moves the current state of a RouteTable toward the desired spec.
func (r *RouteTableReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var rt iaasv1.RouteTable
	if err := r.Get(ctx, req.NamespacedName, &rt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&rt) {
		return ctrl.Result{}, nil
	}

	rt.Status.ObservedGeneration, rt.Status.LastReconcileTime = ReconcileMeta(rt.Generation)

	if !rt.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &rt)
	}
	if controllerutil.AddFinalizer(&rt, routeTableFinalizer) {
		if err := r.Update(ctx, &rt); err != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	vpcIdentity, err := r.resolveVPCRef(ctx, rt.Namespace, rt.Spec.VPCRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setRouteTableErrorCondition(ctx, &rt, "VPCNotFound", err.Error(), err)
	}
	return r.reconcileRouteTable(ctx, rt, vpcIdentity)
}

func (r *RouteTableReconciler) createRouteTable(ctx context.Context, rt iaasv1.RouteTable, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var desc *string
	if rt.Spec.Description != "" {
		desc = &rt.Spec.Description
	}

	SetStandardConditions(&rt.Status.Conditions, ConditionStateProgressing, "Creating", "Creating route table in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &rt); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	created, err := r.IaaSClient.CreateRouteTable(ctx, thalassaiaas.CreateRouteTable{
		Name:        effectiveName(rt.Name, rt.Spec.Metadata),
		Description: desc,
		Labels:      effectiveLabels(rt.Spec.Metadata),
		VpcIdentity: vpcIdentity,
	})
	if err != nil {
		return r.setRouteTableErrorCondition(ctx, &rt, "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	rt.Status.ResourceID = identity
	rt.Status.ResourceStatus = ResourceStatusReady
	rt.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &rt); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	log.Info("created route table in Thalassa", "identity", identity)

	SetStandardConditions(&rt.Status.Conditions, ConditionStateAvailable, "Created", "Route table is created in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &rt); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *RouteTableReconciler) reconcileRouteTable(ctx context.Context, rt iaasv1.RouteTable, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	identity := rt.Status.ResourceID
	if identity == "" {
		return r.createRouteTable(ctx, rt, vpcIdentity)
	}

	fetched, err := r.IaaSClient.GetRouteTable(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&rt.Status.Conditions, ConditionStateDegraded, "NotFound", "Route table not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &rt); updateErr != nil {
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setRouteTableErrorCondition(ctx, &rt, "NotFound", "Route table not found in Thalassa", err)
		}
		return r.setRouteTableErrorCondition(ctx, &rt, "GetFailed", "Failed to get route table from Thalassa", err)
	}

	if r.requiresUpdate(&rt, fetched) {
		log.Info("updating route table in Thalassa", "identity", identity)
		SetStandardConditions(&rt.Status.Conditions, ConditionStateProgressing, "Updating", "Updating route table in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &rt); updateErr != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		name := effectiveName(rt.Name, rt.Spec.Metadata)
		var desc *string
		if rt.Spec.Description != "" {
			desc = &rt.Spec.Description
		}
		_, err = r.IaaSClient.UpdateRouteTable(ctx, identity, thalassaiaas.UpdateRouteTable{
			Name:        &name,
			Description: desc,
			Labels:      effectiveLabels(rt.Spec.Metadata),
		})
		if err != nil {
			return r.setRouteTableErrorCondition(ctx, &rt, "UpdateFailed", err.Error(), err)
		}
		rt.Status.ResourceStatus = ResourceStatusReady
		rt.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &rt); updateErr != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	SetStandardConditions(&rt.Status.Conditions, ConditionStateAvailable, "Synced", "Route table is synced with Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &rt); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *RouteTableReconciler) reconcileDelete(ctx context.Context, rt *iaasv1.RouteTable) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(rt, routeTableFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := rt.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteRouteTable(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		log.Info("deleted route table in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(rt, routeTableFinalizer) {
		if err := r.Update(ctx, rt); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *RouteTableReconciler) updateStatusWithRetry(ctx context.Context, rt *iaasv1.RouteTable) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.RouteTable{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: rt.Namespace, Name: rt.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest RouteTable: %w", err)
		}
		latest.Status = rt.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *RouteTableReconciler) setRouteTableErrorCondition(ctx context.Context, rt *iaasv1.RouteTable, reason, message string, err error) (ctrl.Result, error) {
	if meta.SetStatusCondition(&rt.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		rt.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, rt); updateErr != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *RouteTableReconciler) requiresUpdate(rt *iaasv1.RouteTable, fetched *thalassaiaas.RouteTable) bool {
	if effectiveName(rt.Name, rt.Spec.Metadata) != fetched.Name {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(rt.Spec.Metadata), fetched.Labels) {
		return true
	}
	var specDesc *string
	if rt.Spec.Description != "" {
		specDesc = &rt.Spec.Description
	}
	if (specDesc == nil) != (fetched.Description == nil) {
		return true
	}
	if specDesc != nil && fetched.Description != nil && *specDesc != *fetched.Description {
		return true
	}
	return false
}

func (r *RouteTableReconciler) resolveVPCRef(ctx context.Context, defaultNamespace string, ref iaasv1.VPCRef) (string, error) {
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
func (r *RouteTableReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.RouteTable{}).
		Named("routetable").
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
