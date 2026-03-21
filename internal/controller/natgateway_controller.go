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
	"strings"
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

const natGatewayFinalizer = "iaas.controllers.thalassa.cloud/natgateway"

// NatGatewayReconciler reconciles a NatGateway object
type NatGatewayReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
	Recorder   record.EventRecorder
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=securitygroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=natgateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=natgateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=natgateways/finalizers,verbs=update

// Reconcile moves the current state of a NatGateway toward the desired spec by creating or updating the resource in Thalassa IaaS.
func (r *NatGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var ngw iaasv1.NatGateway
	if err := r.Get(ctx, req.NamespacedName, &ngw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&ngw) {
		return ctrl.Result{}, nil
	}

	ngw.Status.ObservedGeneration, ngw.Status.LastReconcileTime = ReconcileMeta(ngw.Generation)

	if !ngw.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &ngw)
	}
	if controllerutil.AddFinalizer(&ngw, natGatewayFinalizer) {
		if err := r.Update(ctx, &ngw); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	subnetIdentity, err := r.resolveSubnetRef(ctx, ngw.Namespace, ngw.Spec.SubnetRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setNatGatewayErrorCondition(ctx, &ngw, "Reconcile", "SubnetNotFound", err.Error(), err)
	}
	sgIdentities, err := r.resolveSecurityGroupRefs(ctx, ngw.Namespace, ngw.Spec.SecurityGroupRefs)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setNatGatewayErrorCondition(ctx, &ngw, "Reconcile", "SecurityGroupNotFound", err.Error(), err)
	}
	return r.reconcileNatGateway(ctx, ngw, subnetIdentity, sgIdentities)
}

func (r *NatGatewayReconciler) createNatGateway(ctx context.Context, ngw iaasv1.NatGateway, subnetIdentity string, sgIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createNatGateway")
	configureDefaultRoute := true
	if ngw.Spec.ConfigureDefaultRoute != nil {
		configureDefaultRoute = *ngw.Spec.ConfigureDefaultRoute
	}

	SetStandardConditions(&ngw.Status.Conditions, ConditionStateProgressing, "Creating", "Creating NAT gateway in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	createReq := thalassaiaas.CreateVpcNatGateway{
		Name:                     effectiveName(ngw.Name, ngw.Spec.Metadata),
		Description:              ngw.Spec.Description,
		Labels:                   effectiveLabels(ngw.Spec.Metadata),
		SubnetIdentity:           subnetIdentity,
		SecurityGroupAttachments: sgIdentities,
		ConfigureDefaultRoute:    configureDefaultRoute,
	}
	created, err := r.IaaSClient.CreateNatGateway(ctx, createReq)
	if err != nil {
		return r.setNatGatewayErrorCondition(ctx, &ngw, "createNatGateway", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	ngw.Status.ResourceID = identity
	ngw.Status.ResourceStatus = created.Status
	ngw.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	if _, err := r.IaaSClient.WaitUntilNatGatewayHasEndpoint(ctx, identity); err != nil {
		log.Error(err, "failed to wait until NAT gateway has endpoint, will retry later")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	log.Info("created NAT gateway in Thalassa", "identity", identity)

	// Re-fetch to get EndpointIP now that the gateway has an endpoint
	fetched, err := r.IaaSClient.GetNatGateway(ctx, identity)
	if err != nil {
		return r.setNatGatewayErrorCondition(ctx, &ngw, "createNatGateway", "GetFailed", "Failed to get NAT gateway after create", err)
	}
	ngw.Status.ResourceStatus = fetched.Status
	ngw.Status.EndpointIP = fetched.EndpointIP
	ngw.Status.V4IP = fetched.V4IP
	ngw.Status.V6IP = fetched.V6IP
	SetStandardConditions(&ngw.Status.Conditions, ConditionStateAvailable, "Created", "NAT gateway is created in Thalassa")
	ngw.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	r.Recorder.Eventf(&ngw, corev1.EventTypeNormal, "Provisioned", "NAT gateway provisioned in Thalassa (id: %s)", identity)
	return ctrl.Result{}, nil
}

func (r *NatGatewayReconciler) reconcileNatGateway(ctx context.Context, ngw iaasv1.NatGateway, subnetIdentity string, sgIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileNatGateway")
	identity := ngw.Status.ResourceID
	if identity == "" {
		return r.createNatGateway(ctx, ngw, subnetIdentity, sgIdentities)
	}

	fetched, err := r.IaaSClient.GetNatGateway(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&ngw.Status.Conditions, ConditionStateDegraded, "NotFound", "NAT gateway not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setNatGatewayErrorCondition(ctx, &ngw, "reconcileNatGateway", "NotFound", "NAT gateway not found in Thalassa", err)
		}
		return r.setNatGatewayErrorCondition(ctx, &ngw, "reconcileNatGateway", "GetFailed", "Failed to get NAT gateway from Thalassa", err)
	}

	// ensure the natgatewy has an endpoint
	if strings.EqualFold(fetched.Status, ResourceStatusReady) && fetched.EndpointIP == "" {
		if _, err := r.IaaSClient.WaitUntilNatGatewayHasEndpoint(ctx, identity); err != nil {
			log.Error(err, "failed to wait until NAT gateway has endpoint, will retry later")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
		}
	}

	if ngw.Status.ResourceStatus != fetched.Status || ngw.Status.EndpointIP != fetched.EndpointIP || ngw.Status.V4IP != fetched.V4IP || ngw.Status.V6IP != fetched.V6IP {
		ngw.Status.ResourceStatus = fetched.Status
		ngw.Status.EndpointIP = fetched.EndpointIP
		ngw.Status.V4IP = fetched.V4IP
		ngw.Status.V6IP = fetched.V6IP
		ngw.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	if r.requiresUpdate(&ngw, fetched, sgIdentities) {
		log.Info("updating NAT gateway in Thalassa", "identity", identity)
		SetStandardConditions(&ngw.Status.Conditions, ConditionStateProgressing, "Updating", "Updating NAT gateway in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		updated, err := r.IaaSClient.UpdateNatGateway(ctx, identity, thalassaiaas.UpdateVpcNatGateway{
			Name:                     effectiveName(ngw.Name, ngw.Spec.Metadata),
			Description:              ngw.Spec.Description,
			Labels:                   effectiveLabels(ngw.Spec.Metadata),
			SecurityGroupAttachments: sgIdentities,
		})
		if err != nil {
			return r.setNatGatewayErrorCondition(ctx, &ngw, "reconcileNatGateway", "UpdateFailed", err.Error(), err)
		}
		ngw.Status.ResourceStatus = updated.Status
		ngw.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		fetched, err = r.IaaSClient.GetNatGateway(ctx, identity)
		if err != nil {
			return r.setNatGatewayErrorCondition(ctx, &ngw, "reconcileNatGateway", "GetFailed", "Failed to get NAT gateway after update", err)
		}
		ngw.Status.ResourceStatus = fetched.Status
		ngw.Status.EndpointIP = fetched.EndpointIP
		ngw.Status.V4IP = fetched.V4IP
		ngw.Status.V6IP = fetched.V6IP
		if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	SetStandardConditions(&ngw.Status.Conditions, ConditionStateAvailable, "Synced", "NAT gateway is synced with Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &ngw); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *NatGatewayReconciler) reconcileDelete(ctx context.Context, ngw *iaasv1.NatGateway) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	if !controllerutil.ContainsFinalizer(ngw, natGatewayFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := ngw.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteNatGateway(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			log.Error(err, "failed to delete NAT gateway in Thalassa")
			return ctrl.Result{}, err
		}
		if err := r.IaaSClient.WaitUntilNatGatewayDeleted(ctx, identity); err != nil {
			log.Error(err, "failed waiting for NAT gateway deletion")
			return ctrl.Result{}, err
		}
		log.Info("deleted NAT gateway in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(ngw, natGatewayFinalizer) {
		if err := r.Update(ctx, ngw); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(ngw, corev1.EventTypeNormal, "Deleted", "Finished deletion")
	}
	return ctrl.Result{}, nil
}

func (r *NatGatewayReconciler) updateStatusWithRetry(ctx context.Context, ngw *iaasv1.NatGateway) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.NatGateway{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: ngw.Namespace, Name: ngw.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest NatGateway: %w", err)
		}
		latest.Status = ngw.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *NatGatewayReconciler) setNatGatewayErrorCondition(ctx context.Context, ngw *iaasv1.NatGateway, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&ngw.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		ngw.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, ngw); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		r.Recorder.Eventf(ngw, corev1.EventTypeWarning, reason, "%s", message)
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *NatGatewayReconciler) requiresUpdate(ngw *iaasv1.NatGateway, fetched *thalassaiaas.VpcNatGateway, sgIdentities []string) bool {
	if effectiveName(ngw.Name, ngw.Spec.Metadata) != fetched.Name {
		return true
	}
	if ngw.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(ngw.Spec.Metadata), thalassaiaas.Labels(fetched.Labels)) {
		return true
	}
	fetchedIDs := make([]string, 0, len(fetched.SecurityGroups))
	for _, sg := range fetched.SecurityGroups {
		fetchedIDs = append(fetchedIDs, sg.Identity)
	}
	return !equality.Semantic.DeepEqual(sgIdentities, fetchedIDs)
}

func (r *NatGatewayReconciler) resolveSecurityGroupRef(ctx context.Context, defaultNamespace string, ref iaasv1.SecurityGroupRef) (string, error) {
	if ref.Identity != "" {
		return ref.Identity, nil
	}
	if ref.Name == "" {
		return "", fmt.Errorf("security group ref must have identity or name set")
	}
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	var sg iaasv1.SecurityGroup
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &sg); err != nil {
		return "", err
	}
	if sg.Status.ResourceID == "" {
		return "", ErrDependencyNotReady
	}
	return sg.Status.ResourceID, nil
}

func (r *NatGatewayReconciler) resolveSecurityGroupRefs(ctx context.Context, defaultNamespace string, refs []iaasv1.SecurityGroupRef) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	identities := make([]string, 0, len(refs))
	for _, ref := range refs {
		id, err := r.resolveSecurityGroupRef(ctx, defaultNamespace, ref)
		if err != nil {
			return nil, err
		}
		identities = append(identities, id)
	}
	return identities, nil
}

func (r *NatGatewayReconciler) resolveSubnetRef(ctx context.Context, defaultNamespace string, ref iaasv1.SubnetRef) (string, error) {
	if ref.Identity != "" {
		return ref.Identity, nil
	}
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	var subnet iaasv1.Subnet
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &subnet); err != nil {
		return "", err
	}
	if subnet.Status.ResourceID == "" {
		return "", ErrDependencyNotReady
	}
	return subnet.Status.ResourceID, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NatGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("iaas-controller.natgateway")
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.NatGateway{}).
		Named("natgateway").
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
