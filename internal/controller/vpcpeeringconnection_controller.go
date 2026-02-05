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

const vpcPeeringConnectionFinalizer = "iaas.controllers.thalassa.cloud/vpcpeeringconnection"

// VpcPeeringConnectionReconciler reconciles a VpcPeeringConnection object
type VpcPeeringConnectionReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcpeeringconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcpeeringconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcpeeringconnections/finalizers,verbs=update

// Reconcile moves the current state of a VpcPeeringConnection toward the desired spec; handles accept/reject when pending.
func (r *VpcPeeringConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var conn iaasv1.VpcPeeringConnection
	if err := r.Get(ctx, req.NamespacedName, &conn); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&conn) {
		return ctrl.Result{}, nil
	}

	conn.Status.ObservedGeneration, conn.Status.LastReconcileTime = ReconcileMeta(conn.Generation)

	if !conn.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &conn)
	}
	if controllerutil.AddFinalizer(&conn, vpcPeeringConnectionFinalizer) {
		if err := r.Update(ctx, &conn); err != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	requesterVPCIdentity, err := r.resolveVPCRef(ctx, conn.Namespace, conn.Spec.RequesterVPCRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setVpcPeeringConnectionErrorCondition(ctx, &conn, "RequesterVPCNotFound", err.Error(), err)
	}
	return r.reconcileVpcPeeringConnection(ctx, conn, requesterVPCIdentity)
}

func (r *VpcPeeringConnectionReconciler) createVpcPeeringConnection(ctx context.Context, conn iaasv1.VpcPeeringConnection, requesterVPCIdentity string) (ctrl.Result, error) {
	createReq := thalassaiaas.CreateVpcPeeringConnectionRequest{
		Name:                         effectiveName(conn.Name, conn.Spec.Metadata),
		Description:                  conn.Spec.Description,
		Labels:                       effectiveLabels(conn.Spec.Metadata),
		RequesterVpcIdentity:         requesterVPCIdentity,
		AccepterVpcIdentity:          conn.Spec.AccepterVpcId,
		AccepterOrganisationIdentity: conn.Spec.AccepterOrganisationId,
		AutoAccept:                   conn.Spec.AutoAccept,
	}
	created, err := r.IaaSClient.CreateVpcPeeringConnection(ctx, createReq)
	if err != nil {
		return r.setVpcPeeringConnectionErrorCondition(ctx, &conn, "FailedCreate", err.Error(), err)
	}
	conn.Status.ResourceID = created.Identity
	conn.Status.Status = string(created.Status)
	conn.Status.ResourceStatus = string(created.Status)
	conn.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &conn); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	setReadyFromPeeringStatus(&conn.Status.Conditions, created.Status)
	if updateErr := r.updateStatusWithRetry(ctx, &conn); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *VpcPeeringConnectionReconciler) reconcileVpcPeeringConnection(ctx context.Context, conn iaasv1.VpcPeeringConnection, requesterVPCIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	identity := conn.Status.ResourceID
	if identity == "" {
		return r.createVpcPeeringConnection(ctx, conn, requesterVPCIdentity)
	}

	existing, err := r.IaaSClient.GetVpcPeeringConnection(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			conn.Status.ResourceID = ""
			conn.Status.Status = ""
			conn.Status.ResourceStatus = ""
			if updateErr := r.updateStatusWithRetry(ctx, &conn); updateErr != nil {
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
		return r.setVpcPeeringConnectionErrorCondition(ctx, &conn, "GetFailed", "Failed to get VPC peering connection from Thalassa", err)
	}

	if conn.Status.Status != string(existing.Status) || conn.Status.ResourceStatus != string(existing.Status) {
		conn.Status.Status = string(existing.Status)
		conn.Status.ResourceStatus = string(existing.Status)
		conn.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &conn); updateErr != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	accept := conn.Spec.Accept != nil && *conn.Spec.Accept
	reject := conn.Spec.Reject != nil && *conn.Spec.Reject
	if existing.Status == thalassaiaas.VpcPeeringConnectionStatusPending {
		if accept {
			_, err = r.IaaSClient.AcceptVpcPeeringConnection(ctx, identity, thalassaiaas.AcceptVpcPeeringConnectionRequest{})
			if err != nil {
				return r.setVpcPeeringConnectionErrorCondition(ctx, &conn, "AcceptFailed", err.Error(), err)
			}
			log.Info("accepted VPC peering connection", "identity", identity)
			conn.Status.LastReconcileError = ""
			conn.Status.Status = string(thalassaiaas.VpcPeeringConnectionStatusActive)
			conn.Status.ResourceStatus = string(thalassaiaas.VpcPeeringConnectionStatusActive)
			setReadyFromPeeringStatus(&conn.Status.Conditions, thalassaiaas.VpcPeeringConnectionStatusActive)
			if updateErr := r.updateStatusWithRetry(ctx, &conn); updateErr != nil {
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return ctrl.Result{}, nil
		}
		if reject {
			_, err = r.IaaSClient.RejectVpcPeeringConnection(ctx, identity, thalassaiaas.RejectVpcPeeringConnectionRequest{
				Reason: conn.Spec.RejectReason,
			})
			if err != nil {
				return r.setVpcPeeringConnectionErrorCondition(ctx, &conn, "RejectFailed", err.Error(), err)
			}
			log.Info("rejected VPC peering connection", "identity", identity)
			conn.Status.LastReconcileError = ""
			conn.Status.Status = string(thalassaiaas.VpcPeeringConnectionStatusRejected)
			conn.Status.ResourceStatus = string(thalassaiaas.VpcPeeringConnectionStatusRejected)
			setReadyFromPeeringStatus(&conn.Status.Conditions, thalassaiaas.VpcPeeringConnectionStatusRejected)
			if updateErr := r.updateStatusWithRetry(ctx, &conn); updateErr != nil {
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return ctrl.Result{}, nil
		}
	}

	if r.requiresUpdate(&conn, existing) {
		_, err = r.IaaSClient.UpdateVpcPeeringConnection(ctx, identity, thalassaiaas.UpdateVpcPeeringConnectionRequest{
			Name:        effectiveName(conn.Name, conn.Spec.Metadata),
			Description: conn.Spec.Description,
			Labels:      effectiveLabels(conn.Spec.Metadata),
		})
		if err != nil {
			return r.setVpcPeeringConnectionErrorCondition(ctx, &conn, "UpdateFailed", err.Error(), err)
		}
		existing, err = r.IaaSClient.GetVpcPeeringConnection(ctx, identity)
		if err != nil {
			return r.setVpcPeeringConnectionErrorCondition(ctx, &conn, "GetFailed", "Failed to get VPC peering connection after update", err)
		}
		conn.Status.Status = string(existing.Status)
		conn.Status.ResourceStatus = string(existing.Status)
	}

	conn.Status.LastReconcileError = ""
	setReadyFromPeeringStatus(&conn.Status.Conditions, existing.Status)
	if updateErr := r.updateStatusWithRetry(ctx, &conn); updateErr != nil {
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func setReadyFromPeeringStatus(conditions *[]metav1.Condition, status thalassaiaas.VpcPeeringConnectionStatus) {
	ready := metav1.ConditionFalse
	var reason string
	switch status {
	case thalassaiaas.VpcPeeringConnectionStatusActive:
		ready = metav1.ConditionTrue
		reason = "Active"
	case thalassaiaas.VpcPeeringConnectionStatusRejected:
		reason = "Rejected"
	default:
		reason = string(status)
	}
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:   "Ready",
		Status: ready,
		Reason: reason,
	})
}

func (r *VpcPeeringConnectionReconciler) reconcileDelete(ctx context.Context, conn *iaasv1.VpcPeeringConnection) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(conn, vpcPeeringConnectionFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := conn.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteVpcPeeringConnection(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		log.Info("deleted VPC peering connection in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(conn, vpcPeeringConnectionFinalizer) {
		if err := r.Update(ctx, conn); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *VpcPeeringConnectionReconciler) updateStatusWithRetry(ctx context.Context, conn *iaasv1.VpcPeeringConnection) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.VpcPeeringConnection{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: conn.Namespace, Name: conn.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest VpcPeeringConnection: %w", err)
		}
		latest.Status = conn.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *VpcPeeringConnectionReconciler) setVpcPeeringConnectionErrorCondition(ctx context.Context, conn *iaasv1.VpcPeeringConnection, reason, message string, err error) (ctrl.Result, error) {
	if meta.SetStatusCondition(&conn.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		conn.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, conn); updateErr != nil {
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *VpcPeeringConnectionReconciler) requiresUpdate(conn *iaasv1.VpcPeeringConnection, fetched *thalassaiaas.VpcPeeringConnection) bool {
	if effectiveName(conn.Name, conn.Spec.Metadata) != fetched.Name {
		return true
	}
	if conn.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(conn.Spec.Metadata), fetched.Labels) {
		return true
	}
	return false
}

func (r *VpcPeeringConnectionReconciler) resolveVPCRef(ctx context.Context, defaultNamespace string, ref iaasv1.VPCRef) (string, error) {
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
func (r *VpcPeeringConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.VpcPeeringConnection{}).
		Named("vpcpeeringconnection").
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
