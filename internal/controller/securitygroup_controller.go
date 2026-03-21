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

const securityGroupFinalizer = "iaas.controllers.thalassa.cloud/securitygroup"

// SecurityGroupReconciler reconciles a SecurityGroup object
type SecurityGroupReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=securitygroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=securitygroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=securitygroups/finalizers,verbs=update

// Reconcile moves the current state of a SecurityGroup toward the desired spec by creating or updating the resource in Thalassa IaaS.
func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var sg iaasv1.SecurityGroup
	if err := r.Get(ctx, req.NamespacedName, &sg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&sg) {
		return ctrl.Result{}, nil
	}

	sg.Status.ObservedGeneration, sg.Status.LastReconcileTime = ReconcileMeta(sg.Generation)

	if !sg.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &sg)
	}
	if controllerutil.AddFinalizer(&sg, securityGroupFinalizer) {
		if err := r.Update(ctx, &sg); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	vpcIdentity, err := r.resolveVPCRef(ctx, sg.Namespace, sg.Spec.VPCRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setSecurityGroupErrorCondition(ctx, &sg, "Reconcile", "VPCNotFound", err.Error(), err)
	}
	return r.reconcileSecurityGroup(ctx, sg, vpcIdentity)
}

func (r *SecurityGroupReconciler) createSecurityGroup(ctx context.Context, sg iaasv1.SecurityGroup, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createSecurityGroup")
	allowSame := true
	if sg.Spec.AllowSameGroupTraffic != nil {
		allowSame = *sg.Spec.AllowSameGroupTraffic
	}

	SetStandardConditions(&sg.Status.Conditions, ConditionStateProgressing, "Creating", "Creating security group in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	createReq := thalassaiaas.CreateSecurityGroupRequest{
		Name:                  effectiveName(sg.Name, sg.Spec.Metadata),
		Description:           sg.Spec.Description,
		Labels:                effectiveLabels(sg.Spec.Metadata),
		VpcIdentity:           vpcIdentity,
		AllowSameGroupTraffic: allowSame,
		IngressRules:          toThalassaRules(sg.Spec.IngressRules),
		EgressRules:           toThalassaRules(sg.Spec.EgressRules),
	}
	created, err := r.IaaSClient.CreateSecurityGroup(ctx, createReq)
	if err != nil {
		return r.setSecurityGroupErrorCondition(ctx, &sg, "createSecurityGroup", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	sg.Status.ResourceID = identity
	sg.Status.ResourceStatus = string(created.Status)
	sg.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	log.Info("created security group in Thalassa", "identity", identity)

	SetStandardConditions(&sg.Status.Conditions, ConditionStateAvailable, "Created", "Security group is created in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciler) reconcileSecurityGroup(ctx context.Context, sg iaasv1.SecurityGroup, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileSecurityGroup")
	identity := sg.Status.ResourceID
	if identity == "" {
		return r.createSecurityGroup(ctx, sg, vpcIdentity)
	}

	fetched, err := r.IaaSClient.GetSecurityGroup(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&sg.Status.Conditions, ConditionStateDegraded, "NotFound", "Security group not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setSecurityGroupErrorCondition(ctx, &sg, "reconcileSecurityGroup", "NotFound", "Security group not found in Thalassa", err)
		}
		return r.setSecurityGroupErrorCondition(ctx, &sg, "reconcileSecurityGroup", "GetFailed", "Failed to get security group from Thalassa", err)
	}

	if sg.Status.ResourceStatus != string(fetched.Status) {
		sg.Status.ResourceStatus = string(fetched.Status)
		sg.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}
	if r.requiresUpdate(&sg, fetched) {
		log.Info("updating security group in Thalassa", "identity", identity)
		allowSame := true
		if sg.Spec.AllowSameGroupTraffic != nil {
			allowSame = *sg.Spec.AllowSameGroupTraffic
		}
		SetStandardConditions(&sg.Status.Conditions, ConditionStateProgressing, "Updating", "Updating security group in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}

		updated, err := r.IaaSClient.UpdateSecurityGroup(ctx, identity, thalassaiaas.UpdateSecurityGroupRequest{
			Name:                  effectiveName(sg.Name, sg.Spec.Metadata),
			Description:           sg.Spec.Description,
			Labels:                effectiveLabels(sg.Spec.Metadata),
			ObjectVersion:         fetched.ObjectVersion,
			AllowSameGroupTraffic: allowSame,
			IngressRules:          toThalassaRules(sg.Spec.IngressRules),
			EgressRules:           toThalassaRules(sg.Spec.EgressRules),
		})
		if err != nil {
			return r.setSecurityGroupErrorCondition(ctx, &sg, "reconcileSecurityGroup", "UpdateFailed", err.Error(), err)
		}
		sg.Status.ResourceStatus = string(updated.Status)
		sg.Status.LastReconcileError = ""
		if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		fetched, err = r.IaaSClient.GetSecurityGroup(ctx, identity)
		if err != nil {
			return r.setSecurityGroupErrorCondition(ctx, &sg, "reconcileSecurityGroup", "GetFailed", "Failed to get security group after update", err)
		}
		sg.Status.ResourceStatus = string(fetched.Status)
		if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
	}

	SetStandardConditions(&sg.Status.Conditions, ConditionStateAvailable, "Synced", "Security group is synced with Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &sg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciler) reconcileDelete(ctx context.Context, sg *iaasv1.SecurityGroup) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	if !controllerutil.ContainsFinalizer(sg, securityGroupFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := sg.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteSecurityGroup(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			log.Error(err, "failed to delete security group in Thalassa")
			return ctrl.Result{}, err
		}
		log.Info("deleted security group in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(sg, securityGroupFinalizer) {
		if err := r.Update(ctx, sg); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciler) updateStatusWithRetry(ctx context.Context, sg *iaasv1.SecurityGroup) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.SecurityGroup{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: sg.Namespace, Name: sg.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest SecurityGroup: %w", err)
		}
		latest.Status = sg.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *SecurityGroupReconciler) setSecurityGroupErrorCondition(ctx context.Context, sg *iaasv1.SecurityGroup, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&sg.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		sg.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, sg); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *SecurityGroupReconciler) requiresUpdate(sg *iaasv1.SecurityGroup, fetched *thalassaiaas.SecurityGroup) bool {
	if effectiveName(sg.Name, sg.Spec.Metadata) != fetched.Name {
		return true
	}
	if sg.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(sg.Spec.Metadata), fetched.Labels) {
		return true
	}
	allowSame := true
	if sg.Spec.AllowSameGroupTraffic != nil {
		allowSame = *sg.Spec.AllowSameGroupTraffic
	}
	if allowSame != fetched.AllowSameGroupTraffic {
		return true
	}
	if !equality.Semantic.DeepEqual(toThalassaRules(sg.Spec.IngressRules), fetched.IngressRules) {
		return true
	}
	if !equality.Semantic.DeepEqual(toThalassaRules(sg.Spec.EgressRules), fetched.EgressRules) {
		return true
	}
	return false
}

func (r *SecurityGroupReconciler) resolveVPCRef(ctx context.Context, defaultNamespace string, ref iaasv1.VPCRef) (string, error) {
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

func toThalassaRules(rules []iaasv1.SecurityGroupRule) []thalassaiaas.SecurityGroupRule {
	if len(rules) == 0 {
		return nil
	}
	out := make([]thalassaiaas.SecurityGroupRule, 0, len(rules))
	for _, r := range rules {
		remoteType := thalassaiaas.SecurityGroupRuleRemoteTypeAddress
		if r.RemoteSecurityGroupIdentity != nil && *r.RemoteSecurityGroupIdentity != "" {
			remoteType = thalassaiaas.SecurityGroupRuleRemoteTypeSecurityGroup
		}
		protocol := thalassaiaas.SecurityGroupRuleProtocol(r.Protocol)
		if protocol == "" {
			protocol = thalassaiaas.SecurityGroupRuleProtocolAll
		}
		policy := thalassaiaas.SecurityGroupRulePolicyAllow
		if r.Policy == "deny" {
			policy = thalassaiaas.SecurityGroupRulePolicyDrop
		}
		priority := int32(100)
		if r.Priority != nil {
			priority = *r.Priority
		}
		portMin, portMax := int32(0), int32(0)
		if r.PortRangeMin != nil {
			portMin = *r.PortRangeMin
		}
		if r.PortRangeMax != nil {
			portMax = *r.PortRangeMax
		}
		out = append(out, thalassaiaas.SecurityGroupRule{
			Name:                        r.Name,
			IPVersion:                   thalassaiaas.SecurityGroupIPVersionIPv4,
			Protocol:                    protocol,
			Priority:                    priority,
			RemoteType:                  remoteType,
			RemoteAddress:               r.RemoteAddress,
			RemoteSecurityGroupIdentity: r.RemoteSecurityGroupIdentity,
			PortRangeMin:                portMin,
			PortRangeMax:                portMax,
			Policy:                      policy,
		})
	}
	return out
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecurityGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.SecurityGroup{}).
		Named("securitygroup").
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
