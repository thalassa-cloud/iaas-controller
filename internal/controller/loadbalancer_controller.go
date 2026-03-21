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

const loadbalancerFinalizer = "iaas.controllers.thalassa.cloud/loadbalancer"

// LoadbalancerReconciler reconciles a Loadbalancer object
type LoadbalancerReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
	Recorder   record.EventRecorder
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=targetgroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=securitygroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=loadbalancers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=loadbalancers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=loadbalancers/finalizers,verbs=update

// Reconcile moves the current state of a Loadbalancer toward the desired spec by creating or updating the resource in Thalassa IaaS.
func (r *LoadbalancerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var lb iaasv1.Loadbalancer
	if err := r.Get(ctx, req.NamespacedName, &lb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&lb) {
		return ctrl.Result{}, nil
	}

	lb.Status.ObservedGeneration, lb.Status.LastReconcileTime = ReconcileMeta(lb.Generation)

	if !lb.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &lb)
	}
	if controllerutil.AddFinalizer(&lb, loadbalancerFinalizer) {
		if err := r.Update(ctx, &lb); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	subnetIdentity, err := r.resolveSubnetRef(ctx, lb.Namespace, lb.Spec.SubnetRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setLoadbalancerErrorCondition(ctx, &lb, "Reconcile", "SubnetNotFound", err.Error(), err)
	}
	sgIdentities, err := r.resolveSecurityGroupRefs(ctx, lb.Namespace, lb.Spec.SecurityGroupRefs)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setLoadbalancerErrorCondition(ctx, &lb, "Reconcile", "SecurityGroupNotFound", err.Error(), err)
	}
	return r.reconcileLoadbalancer(ctx, lb, subnetIdentity, sgIdentities)
}

func (r *LoadbalancerReconciler) createLoadbalancer(ctx context.Context, lb iaasv1.Loadbalancer, subnetIdentity string, sgIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createLoadbalancer")
	listeners, err := r.resolveListeners(ctx, lb.Namespace, lb.Spec.Listeners)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setLoadbalancerErrorCondition(ctx, &lb, "createLoadbalancer", "TargetGroupNotFound", err.Error(), err)
	}

	SetStandardConditions(&lb.Status.Conditions, ConditionStateProgressing, "Creating", "Creating loadbalancer in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	createReq := thalassaiaas.CreateLoadbalancer{
		Name:                     effectiveName(lb.Name, lb.Spec.Metadata),
		Description:              lb.Spec.Description,
		Labels:                   effectiveLabels(lb.Spec.Metadata),
		Subnet:                   subnetIdentity,
		InternalLoadbalancer:     lb.Spec.InternalLoadbalancer,
		DeleteProtection:         lb.Spec.DeleteProtection,
		Listeners:                listeners,
		SecurityGroupAttachments: sgIdentities,
	}
	created, err := r.IaaSClient.CreateLoadbalancer(ctx, createReq)
	if err != nil {
		return r.setLoadbalancerErrorCondition(ctx, &lb, "createLoadbalancer", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	lb.Status.ResourceID = identity
	lb.Status.ResourceStatus = created.Status
	lb.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	if err := r.IaaSClient.WaitUntilLoadbalancerIsReady(ctx, identity); err != nil {
		log.Error(err, "failed to wait until loadbalancer is ready, will retry later")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	log.Info("created loadbalancer in Thalassa", "identity", identity)

	fetched, err := r.IaaSClient.GetLoadbalancer(ctx, identity)
	if err != nil {
		return r.setLoadbalancerErrorCondition(ctx, &lb, "createLoadbalancer", "GetFailed", "Failed to get loadbalancer after create", err)
	}
	lb.Status.ResourceStatus = fetched.Status
	lb.Status.ExternalIPAddresses = fetched.ExternalIpAddresses
	lb.Status.InternalIPAddresses = fetched.InternalIpAddresses
	lb.Status.Hostname = fetched.Hostname
	SetStandardConditions(&lb.Status.Conditions, ConditionStateAvailable, "Created", "Loadbalancer is created in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	r.Recorder.Eventf(&lb, corev1.EventTypeNormal, "Provisioned", "Load balancer provisioned in Thalassa (id: %s)", identity)
	return ctrl.Result{}, nil
}

func (r *LoadbalancerReconciler) reconcileLoadbalancer(ctx context.Context, lb iaasv1.Loadbalancer, subnetIdentity string, sgIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileLoadbalancer")
	identity := lb.Status.ResourceID
	if identity == "" {
		return r.createLoadbalancer(ctx, lb, subnetIdentity, sgIdentities)
	}

	fetched, err := r.IaaSClient.GetLoadbalancer(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&lb.Status.Conditions, ConditionStateDegraded, "NotFound", "Loadbalancer not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setLoadbalancerErrorCondition(ctx, &lb, "reconcileLoadbalancer", "NotFound", "Loadbalancer not found in Thalassa", err)
		}
		return r.setLoadbalancerErrorCondition(ctx, &lb, "reconcileLoadbalancer", "GetFailed", "Failed to get loadbalancer from Thalassa", err)
	}

	if lb.Status.ResourceStatus != fetched.Status ||
		!equality.Semantic.DeepEqual(lb.Status.ExternalIPAddresses, fetched.ExternalIpAddresses) ||
		!equality.Semantic.DeepEqual(lb.Status.InternalIPAddresses, fetched.InternalIpAddresses) ||
		lb.Status.Hostname != fetched.Hostname {
		lb.Status.ResourceStatus = fetched.Status
		lb.Status.ExternalIPAddresses = fetched.ExternalIpAddresses
		lb.Status.InternalIPAddresses = fetched.InternalIpAddresses
		lb.Status.Hostname = fetched.Hostname
		lb.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	if r.requiresUpdate(&lb, fetched, subnetIdentity, sgIdentities) {
		log.Info("updating loadbalancer in Thalassa", "identity", identity)
		SetStandardConditions(&lb.Status.Conditions, ConditionStateProgressing, "Updating", "Updating loadbalancer in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		var subnetPtr *string
		if subnetIdentity != "" && fetched.SubnetIdentity != subnetIdentity {
			subnetPtr = &subnetIdentity
		}
		updated, err := r.IaaSClient.UpdateLoadbalancer(ctx, identity, thalassaiaas.UpdateLoadbalancer{
			Name:                     effectiveName(lb.Name, lb.Spec.Metadata),
			Description:              lb.Spec.Description,
			Labels:                   effectiveLabels(lb.Spec.Metadata),
			DeleteProtection:         lb.Spec.DeleteProtection,
			Subnet:                   subnetPtr,
			SecurityGroupAttachments: sgIdentities,
		})
		if err != nil {
			return r.setLoadbalancerErrorCondition(ctx, &lb, "reconcileLoadbalancer", "UpdateFailed", err.Error(), err)
		}
		lb.Status.ResourceStatus = updated.Status
		lb.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		fetched, err = r.IaaSClient.GetLoadbalancer(ctx, identity)
		if err != nil {
			return r.setLoadbalancerErrorCondition(ctx, &lb, "reconcileLoadbalancer", "GetFailed", "Failed to get loadbalancer after update", err)
		}
		lb.Status.ResourceStatus = fetched.Status
		lb.Status.ExternalIPAddresses = fetched.ExternalIpAddresses
		lb.Status.InternalIPAddresses = fetched.InternalIpAddresses
		lb.Status.Hostname = fetched.Hostname
		if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	SetStandardConditions(&lb.Status.Conditions, ConditionStateAvailable, "Synced", "Loadbalancer is synced with Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &lb); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *LoadbalancerReconciler) reconcileDelete(ctx context.Context, lb *iaasv1.Loadbalancer) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	if !controllerutil.ContainsFinalizer(lb, loadbalancerFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := lb.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteLoadbalancer(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			log.Error(err, "failed to delete loadbalancer in Thalassa")
			return ctrl.Result{}, err
		}
		if err := r.IaaSClient.WaitUntilLoadbalancerIsDeleted(ctx, identity); err != nil {
			log.Error(err, "failed waiting for loadbalancer deletion")
			return ctrl.Result{}, err
		}
		log.Info("deleted loadbalancer in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(lb, loadbalancerFinalizer) {
		if err := r.Update(ctx, lb); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(lb, corev1.EventTypeNormal, "Deleted", "Finished deletion")
	}
	return ctrl.Result{}, nil
}

func (r *LoadbalancerReconciler) updateStatusWithRetry(ctx context.Context, lb *iaasv1.Loadbalancer) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.Loadbalancer{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: lb.Namespace, Name: lb.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest Loadbalancer: %w", err)
		}
		latest.Status = lb.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *LoadbalancerReconciler) setLoadbalancerErrorCondition(ctx context.Context, lb *iaasv1.Loadbalancer, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&lb.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		lb.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, lb); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		r.Recorder.Eventf(lb, corev1.EventTypeWarning, reason, "%s", message)
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *LoadbalancerReconciler) requiresUpdate(lb *iaasv1.Loadbalancer, fetched *thalassaiaas.VpcLoadbalancer, subnetIdentity string, sgIdentities []string) bool {
	if effectiveName(lb.Name, lb.Spec.Metadata) != fetched.Name {
		return true
	}
	if lb.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(lb.Spec.Metadata), fetched.Labels) {
		return true
	}
	if lb.Spec.DeleteProtection != fetched.DeleteProtection {
		return true
	}
	if subnetIdentity != "" && fetched.SubnetIdentity != subnetIdentity {
		return true
	}
	fetchedIDs := make([]string, 0, len(fetched.SecurityGroups))
	for _, sg := range fetched.SecurityGroups {
		fetchedIDs = append(fetchedIDs, sg.Identity)
	}
	return !equality.Semantic.DeepEqual(sgIdentities, fetchedIDs)
}

func (r *LoadbalancerReconciler) resolveSecurityGroupRef(ctx context.Context, defaultNamespace string, ref iaasv1.SecurityGroupRef) (string, error) {
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

func (r *LoadbalancerReconciler) resolveSecurityGroupRefs(ctx context.Context, defaultNamespace string, refs []iaasv1.SecurityGroupRef) ([]string, error) {
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

func (r *LoadbalancerReconciler) resolveSubnetRef(ctx context.Context, defaultNamespace string, ref iaasv1.SubnetRef) (string, error) {
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

func (r *LoadbalancerReconciler) resolveTargetGroupRef(ctx context.Context, defaultNamespace string, ref iaasv1.TargetGroupRef) (string, error) {
	if ref.Identity != "" {
		return ref.Identity, nil
	}
	ns := ref.Namespace
	if ns == "" {
		ns = defaultNamespace
	}
	var tg iaasv1.TargetGroup
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, &tg); err != nil {
		return "", err
	}
	if tg.Status.ResourceID == "" {
		return "", ErrDependencyNotReady
	}
	return tg.Status.ResourceID, nil
}

func (r *LoadbalancerReconciler) resolveListeners(ctx context.Context, defaultNamespace string, specs []iaasv1.LoadbalancerListenerSpec) ([]thalassaiaas.CreateListener, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]thalassaiaas.CreateListener, 0, len(specs))
	for _, s := range specs {
		tgIdentity, err := r.resolveTargetGroupRef(ctx, defaultNamespace, s.TargetGroupRef)
		if err != nil {
			return nil, err
		}
		out = append(out, thalassaiaas.CreateListener{
			Name:                  s.Name,
			Description:           s.Description,
			Port:                  int(s.Port),
			Protocol:              thalassaiaas.LoadbalancerProtocol(s.Protocol),
			TargetGroup:           tgIdentity,
			MaxConnections:        s.MaxConnections,
			ConnectionIdleTimeout: s.ConnectionIdleTimeout,
			AllowedSources:        s.AllowedSources,
		})
	}
	return out, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LoadbalancerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("iaas-controller.loadbalancer")
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.Loadbalancer{}).
		Named("loadbalancer").
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
