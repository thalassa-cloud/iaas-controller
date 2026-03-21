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

const targetGroupFinalizer = "iaas.controllers.thalassa.cloud/targetgroup"

// TargetGroupReconciler reconciles a TargetGroup object
type TargetGroupReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
	Recorder   record.EventRecorder
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=targetgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=targetgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=targetgroups/finalizers,verbs=update

// Reconcile moves the current state of a TargetGroup toward the desired spec, including attachments.
func (r *TargetGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var tg iaasv1.TargetGroup
	if err := r.Get(ctx, req.NamespacedName, &tg); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&tg) {
		return ctrl.Result{}, nil
	}

	tg.Status.ObservedGeneration, tg.Status.LastReconcileTime = ReconcileMeta(tg.Generation)

	if !tg.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &tg)
	}
	if controllerutil.AddFinalizer(&tg, targetGroupFinalizer) {
		if err := r.Update(ctx, &tg); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	vpcIdentity, err := r.resolveVPCRef(ctx, tg.Namespace, tg.Spec.VPCRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setTargetGroupErrorCondition(ctx, &tg, "Reconcile", "VPCNotFound", err.Error(), err)
	}
	return r.reconcileTargetGroup(ctx, tg, vpcIdentity)
}

func (r *TargetGroupReconciler) createTargetGroup(ctx context.Context, tg iaasv1.TargetGroup, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createTargetGroup")
	createReq := r.specToCreateTargetGroup(&tg, vpcIdentity)

	SetStandardConditions(&tg.Status.Conditions, ConditionStateProgressing, "Creating", "Creating target group in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &tg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	created, err := r.IaaSClient.CreateTargetGroup(ctx, createReq)
	if err != nil {
		return r.setTargetGroupErrorCondition(ctx, &tg, "createTargetGroup", "FailedCreate", err.Error(), errors.New(strings.ReplaceAll(err.Error(), "\n", " ")))
	}
	tg.Status.ResourceID = created.Identity
	tg.Status.ResourceStatus = ResourceStatusReady
	tg.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &tg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	log.Info("created target group in Thalassa", "identity", created.Identity)

	SetStandardConditions(&tg.Status.Conditions, ConditionStateAvailable, "Created", "Target group is created in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &tg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	r.Recorder.Eventf(&tg, corev1.EventTypeNormal, "Provisioned", "Target group provisioned in Thalassa (id: %s)", created.Identity)
	return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
}

func (r *TargetGroupReconciler) reconcileTargetGroup(ctx context.Context, tg iaasv1.TargetGroup, vpcIdentity string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileTargetGroup")
	identity := tg.Status.ResourceID
	if identity == "" {
		return r.createTargetGroup(ctx, tg, vpcIdentity)
	}

	_, err := r.IaaSClient.GetTargetGroup(ctx, thalassaiaas.GetTargetGroupRequest{Identity: identity})
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			tg.Status.ResourceID = ""
			tg.Status.ResourceStatus = ""
			if updateErr := r.updateStatusWithRetry(ctx, &tg); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
		return r.setTargetGroupErrorCondition(ctx, &tg, "reconcileTargetGroup", "GetFailed", "Failed to get target group from Thalassa", err)
	}

	updateReq := r.specToUpdateTargetGroup(&tg)
	updateReq.Identity = identity
	_, err = r.IaaSClient.UpdateTargetGroup(ctx, updateReq)
	if err != nil {
		return r.setTargetGroupErrorCondition(ctx, &tg, "reconcileTargetGroup", "UpdateFailed", err.Error(), err)
	}

	attachments := make([]thalassaiaas.AttachTarget, 0, len(tg.Spec.Attachments))
	for _, a := range tg.Spec.Attachments {
		attachments = append(attachments, thalassaiaas.AttachTarget{
			ServerIdentity:   a.ServerIdentity,
			EndpointIdentity: a.EndpointIdentity,
		})
	}

	if len(attachments) > 0 { // TODO: allow removing all attachments
		if err := r.IaaSClient.SetTargetGroupServerAttachments(ctx, thalassaiaas.TargetGroupAttachmentsBatch{
			TargetGroupID: identity,
			Attachments:   attachments,
		}); err != nil {
			log.Error(err, "failed to set target group attachments")
		}
	}

	SetStandardConditions(&tg.Status.Conditions, ConditionStateAvailable, "Synced", "Target group is synced with Thalassa")
	tg.Status.LastReconcileError = ""
	tg.Status.ResourceStatus = ResourceStatusReady
	if updateErr := r.updateStatusWithRetry(ctx, &tg); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *TargetGroupReconciler) specToCreateTargetGroup(tg *iaasv1.TargetGroup, vpcIdentity string) thalassaiaas.CreateTargetGroup {
	req := thalassaiaas.CreateTargetGroup{
		Name:                effectiveName(tg.Name, tg.Spec.Metadata),
		Description:         tg.Spec.Description,
		Labels:              effectiveLabels(tg.Spec.Metadata),
		Vpc:                 vpcIdentity,
		TargetPort:          tg.Spec.TargetPort,
		Protocol:            thalassaiaas.LoadbalancerProtocol(tg.Spec.Protocol),
		TargetSelector:      tg.Spec.TargetSelector,
		EnableProxyProtocol: tg.Spec.EnableProxyProtocol,
		LoadbalancingPolicy: nil,
		HealthCheck:         nil,
	}
	if tg.Spec.LoadbalancingPolicy != nil {
		policy := thalassaiaas.LoadbalancingPolicy(*tg.Spec.LoadbalancingPolicy)
		req.LoadbalancingPolicy = &policy
	}
	if tg.Spec.HealthCheck != nil {
		req.HealthCheck = &thalassaiaas.BackendHealthCheck{
			Protocol:           thalassaiaas.LoadbalancerProtocol(tg.Spec.HealthCheck.Protocol),
			Port:               tg.Spec.HealthCheck.Port,
			Path:               tg.Spec.HealthCheck.Path,
			PeriodSeconds:      tg.Spec.HealthCheck.PeriodSeconds,
			TimeoutSeconds:     tg.Spec.HealthCheck.TimeoutSeconds,
			UnhealthyThreshold: tg.Spec.HealthCheck.UnhealthyThreshold,
			HealthyThreshold:   tg.Spec.HealthCheck.HealthyThreshold,
		}
		if req.HealthCheck.PeriodSeconds == 0 {
			req.HealthCheck.PeriodSeconds = 10
		}
		if req.HealthCheck.TimeoutSeconds == 0 {
			req.HealthCheck.TimeoutSeconds = 5
		}
		if req.HealthCheck.UnhealthyThreshold == 0 {
			req.HealthCheck.UnhealthyThreshold = 2
		}
		if req.HealthCheck.HealthyThreshold == 0 {
			req.HealthCheck.HealthyThreshold = 2
		}
	}
	return req
}

func (r *TargetGroupReconciler) specToUpdateTargetGroup(tg *iaasv1.TargetGroup) thalassaiaas.UpdateTargetGroupRequest {
	req := thalassaiaas.UpdateTargetGroupRequest{
		UpdateTargetGroup: thalassaiaas.UpdateTargetGroup{
			Name:                effectiveName(tg.Name, tg.Spec.Metadata),
			Description:         tg.Spec.Description,
			Labels:              effectiveLabels(tg.Spec.Metadata),
			TargetPort:          tg.Spec.TargetPort,
			Protocol:            thalassaiaas.LoadbalancerProtocol(tg.Spec.Protocol),
			TargetSelector:      tg.Spec.TargetSelector,
			EnableProxyProtocol: tg.Spec.EnableProxyProtocol,
			LoadbalancingPolicy: nil,
			HealthCheck:         nil,
		},
	}
	if tg.Spec.LoadbalancingPolicy != nil {
		policy := thalassaiaas.LoadbalancingPolicy(*tg.Spec.LoadbalancingPolicy)
		req.LoadbalancingPolicy = &policy
	}
	if tg.Spec.HealthCheck != nil {
		req.HealthCheck = &thalassaiaas.BackendHealthCheck{
			Protocol:           thalassaiaas.LoadbalancerProtocol(tg.Spec.HealthCheck.Protocol),
			Port:               tg.Spec.HealthCheck.Port,
			Path:               tg.Spec.HealthCheck.Path,
			PeriodSeconds:      tg.Spec.HealthCheck.PeriodSeconds,
			TimeoutSeconds:     tg.Spec.HealthCheck.TimeoutSeconds,
			UnhealthyThreshold: tg.Spec.HealthCheck.UnhealthyThreshold,
			HealthyThreshold:   tg.Spec.HealthCheck.HealthyThreshold,
		}
	}
	return req
}

func (r *TargetGroupReconciler) reconcileDelete(ctx context.Context, tg *iaasv1.TargetGroup) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	if !controllerutil.ContainsFinalizer(tg, targetGroupFinalizer) {
		return ctrl.Result{}, nil
	}
	identity := tg.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteTargetGroup(ctx, thalassaiaas.DeleteTargetGroupRequest{Identity: identity}); err != nil && !thalassaclient.IsNotFound(err) {
			log.Error(err, "failed to delete target group in Thalassa")
			return ctrl.Result{}, err
		}
		log.Info("deleted target group in Thalassa", "identity", identity)
	}
	if controllerutil.RemoveFinalizer(tg, targetGroupFinalizer) {
		if err := r.Update(ctx, tg); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(tg, corev1.EventTypeNormal, "Deleted", "Finished deletion")
	}
	return ctrl.Result{}, nil
}

func (r *TargetGroupReconciler) updateStatusWithRetry(ctx context.Context, tg *iaasv1.TargetGroup) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.TargetGroup{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: tg.Namespace, Name: tg.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest TargetGroup: %w", err)
		}
		latest.Status = tg.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *TargetGroupReconciler) setTargetGroupErrorCondition(ctx context.Context, tg *iaasv1.TargetGroup, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&tg.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		tg.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, tg); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		r.Recorder.Eventf(tg, corev1.EventTypeWarning, reason, "%s", message)
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *TargetGroupReconciler) resolveVPCRef(ctx context.Context, defaultNamespace string, ref iaasv1.VPCRef) (string, error) {
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
func (r *TargetGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("iaas-controller.targetgroup")
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.TargetGroup{}).
		Named("targetgroup").
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
