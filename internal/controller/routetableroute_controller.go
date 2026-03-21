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

const routeTableRouteFinalizer = "iaas.controllers.thalassa.cloud/routetableroute"

// RouteTableRouteReconciler reconciles a RouteTableRoute object
type RouteTableRouteReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=routetables,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=natgateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcpeeringconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=routetableroutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=routetableroutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=routetableroutes/finalizers,verbs=update

// Reconcile moves the current state of a RouteTableRoute toward the desired spec.
func (r *RouteTableRouteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var route iaasv1.RouteTableRoute
	if err := r.Get(ctx, req.NamespacedName, &route); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&route) {
		return ctrl.Result{}, nil
	}

	route.Status.ObservedGeneration, route.Status.LastReconcileTime = ReconcileMeta(route.Generation)

	if !route.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &route)
	}
	if controllerutil.AddFinalizer(&route, routeTableRouteFinalizer) {
		if err := r.Update(ctx, &route); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	rtIdentity, err := r.resolveRouteTableRef(ctx, route.Namespace, route.Spec.RouteTableRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setRouteTableRouteErrorCondition(ctx, &route, "Reconcile", "RouteTableNotFound", err.Error(), err)
	}
	var targetNatGatewayId, targetGatewayId string
	var targetVpcPeeringConnectionId *string
	if route.Spec.TargetGatewayRef != nil {
		var resolvedVpcPeeringId *string
		targetNatGatewayId, resolvedVpcPeeringId, err = r.resolveTargetGatewayRef(ctx, route.Namespace, *route.Spec.TargetGatewayRef)
		if err != nil {
			if errors.Is(err, ErrDependencyNotReady) {
				return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
			}
			return r.setRouteTableRouteErrorCondition(ctx, &route, "Reconcile", "TargetGatewayNotFound", err.Error(), err)
		}
		targetVpcPeeringConnectionId = resolvedVpcPeeringId
	} else {
		targetNatGatewayId = route.Spec.TargetNatGatewayId
		targetGatewayId = route.Spec.TargetGatewayId
		targetVpcPeeringConnectionId = route.Spec.TargetVpcPeeringConnectionId
	}
	return r.reconcileRouteTableRoute(ctx, route, rtIdentity, targetNatGatewayId, targetGatewayId, targetVpcPeeringConnectionId)
}

func (r *RouteTableRouteReconciler) createRouteTableRoute(ctx context.Context, route iaasv1.RouteTableRoute, rtIdentity, targetNatGatewayId, targetGatewayId string, targetVpcPeeringConnectionId *string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createRouteTableRoute")
	createReq := thalassaiaas.CreateRouteTableRoute{
		DestinationCidrBlock:         route.Spec.DestinationCidrBlock,
		TargetNatGatewayIdentity:     targetNatGatewayId,
		TargetGatewayIdentity:        targetGatewayId,
		TargetVpcPeeringConnectionId: targetVpcPeeringConnectionId,
		GatewayAddress:               route.Spec.GatewayAddress,
	}

	SetStandardConditions(&route.Status.Conditions, ConditionStateProgressing, "Creating", "Creating route table route in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &route); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	created, err := r.IaaSClient.CreateRouteTableRoute(ctx, rtIdentity, createReq)
	if err != nil {
		return r.setRouteTableRouteErrorCondition(ctx, &route, "createRouteTableRoute", "FailedCreate", err.Error(), err)
	}
	route.Status.ResourceID = created.Identity
	route.Status.ResourceStatus = ResourceStatusReady
	route.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &route); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	log.Info("created route table route in Thalassa", "identity", created.Identity)

	SetStandardConditions(&route.Status.Conditions, ConditionStateAvailable, "Created", "Route table route is created in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &route); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *RouteTableRouteReconciler) reconcileRouteTableRoute(ctx context.Context, route iaasv1.RouteTableRoute, rtIdentity, targetNatGatewayId, targetGatewayId string, targetVpcPeeringConnectionId *string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileRouteTableRoute")
	routeIdentity := route.Status.ResourceID
	if routeIdentity == "" {
		return r.createRouteTableRoute(ctx, route, rtIdentity, targetNatGatewayId, targetGatewayId, targetVpcPeeringConnectionId)
	}

	updateReq := thalassaiaas.UpdateRouteTableRoute{
		DestinationCidrBlock:         route.Spec.DestinationCidrBlock,
		TargetNatGatewayIdentity:     targetNatGatewayId,
		TargetGatewayIdentity:        targetGatewayId,
		TargetVpcPeeringConnectionId: targetVpcPeeringConnectionId,
		GatewayAddress:               route.Spec.GatewayAddress,
	}
	_, err := r.IaaSClient.UpdateRouteTableRoute(ctx, rtIdentity, routeIdentity, updateReq)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			route.Status.ResourceID = ""
			route.Status.ResourceStatus = ""
			if updateErr := r.updateStatusWithRetry(ctx, &route); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
		return r.setRouteTableRouteErrorCondition(ctx, &route, "reconcileRouteTableRoute", "UpdateFailed", err.Error(), err)
	}

	SetStandardConditions(&route.Status.Conditions, ConditionStateAvailable, "Synced", "Route table route is synced with Thalassa")
	route.Status.LastReconcileError = ""
	route.Status.ResourceStatus = ResourceStatusReady
	if updateErr := r.updateStatusWithRetry(ctx, &route); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	log.Info("synced route table route in Thalassa", "identity", routeIdentity)
	return ctrl.Result{}, nil
}

func (r *RouteTableRouteReconciler) reconcileDelete(ctx context.Context, route *iaasv1.RouteTableRoute) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	if !controllerutil.ContainsFinalizer(route, routeTableRouteFinalizer) {
		return ctrl.Result{}, nil
	}
	rtIdentity, err := r.resolveRouteTableRef(ctx, route.Namespace, route.Spec.RouteTableRef)
	if err != nil {
		if controllerutil.RemoveFinalizer(route, routeTableRouteFinalizer) {
			_ = r.Update(ctx, route)
		}
		return ctrl.Result{}, nil
	}
	routeIdentity := route.Status.ResourceID
	if routeIdentity != "" {
		if err := r.IaaSClient.DeleteRouteTableRoute(ctx, rtIdentity, routeIdentity); err != nil && !thalassaclient.IsNotFound(err) {
			log.Error(err, "failed to delete route table route in Thalassa")
			return ctrl.Result{}, err
		}
		log.Info("deleted route table route in Thalassa", "identity", routeIdentity)
	}
	if controllerutil.RemoveFinalizer(route, routeTableRouteFinalizer) {
		if err := r.Update(ctx, route); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *RouteTableRouteReconciler) updateStatusWithRetry(ctx context.Context, route *iaasv1.RouteTableRoute) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.RouteTableRoute{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: route.Namespace, Name: route.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest RouteTableRoute: %w", err)
		}
		latest.Status = route.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *RouteTableRouteReconciler) setRouteTableRouteErrorCondition(ctx context.Context, route *iaasv1.RouteTableRoute, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&route.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		route.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, route); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *RouteTableRouteReconciler) resolveRouteTableRef(ctx context.Context, defaultNamespace string, ref iaasv1.RouteTableRef) (string, error) {
	if ref.Identity != "" {
		return ref.Identity, nil
	}
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	var rt iaasv1.RouteTable
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &rt); err != nil {
		return "", err
	}
	if rt.Status.ResourceID == "" {
		return "", ErrDependencyNotReady
	}
	return rt.Status.ResourceID, nil
}

// resolveTargetGatewayRef resolves a TargetGatewayRef to Thalassa identities.
// Returns (targetNatGatewayIdentity, targetVpcPeeringConnectionId); exactly one of the two is set.
func (r *RouteTableRouteReconciler) resolveTargetGatewayRef(ctx context.Context, defaultNamespace string, ref iaasv1.TargetGatewayRef) (natGatewayId string, vpcPeeringId *string, err error) {
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	key := client.ObjectKey{Namespace: ns, Name: ref.Name}
	switch ref.Kind {
	case iaasv1.TargetGatewayRefKindNatGateway:
		var ngw iaasv1.NatGateway
		if getErr := r.Get(ctx, key, &ngw); getErr != nil {
			return "", nil, getErr
		}
		if ngw.Status.ResourceID == "" {
			return "", nil, ErrDependencyNotReady
		}
		return ngw.Status.ResourceID, nil, nil
	case iaasv1.TargetGatewayRefKindVpcPeeringConnection:
		var conn iaasv1.VpcPeeringConnection
		if getErr := r.Get(ctx, key, &conn); getErr != nil {
			return "", nil, getErr
		}
		if conn.Status.ResourceID == "" {
			return "", nil, ErrDependencyNotReady
		}
		id := conn.Status.ResourceID
		return "", &id, nil
	default:
		return "", nil, fmt.Errorf("unsupported target gateway kind %q", ref.Kind)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *RouteTableRouteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.RouteTableRoute{}).
		Named("routetableroute").
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
