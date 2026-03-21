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

const snapshotpolicyFinalizer = "iaas.controllers.thalassa.cloud/snapshotpolicy"

// SnapshotPolicyReconciler reconciles a SnapshotPolicy object.
type SnapshotPolicyReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
	Recorder   record.EventRecorder
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=blockvolumes,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=snapshotpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=snapshotpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=snapshotpolicies/finalizers,verbs=update

// Reconcile moves the current state of a SnapshotPolicy toward the desired spec by creating or updating the policy in Thalassa IaaS.
func (r *SnapshotPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var sp iaasv1.SnapshotPolicy
	if err := r.Get(ctx, req.NamespacedName, &sp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.IaaSClient == nil {
		log.Info("IaaS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&sp) {
		return ctrl.Result{}, nil
	}

	sp.Status.ObservedGeneration, sp.Status.LastReconcileTime = ReconcileMeta(sp.Generation)

	if !sp.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &sp)
	}
	if controllerutil.AddFinalizer(&sp, snapshotpolicyFinalizer) {
		if err := r.Update(ctx, &sp); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	var volumeIdentities []string
	if sp.Spec.Target.Type == iaasv1.SnapshotPolicyTargetTypeExplicit && len(sp.Spec.Target.Volumes) > 0 {
		var err error
		volumeIdentities, err = r.resolveVolumeRefs(ctx, sp.Namespace, sp.Spec.Target.Volumes)
		if err != nil {
			if errors.Is(err, ErrDependencyNotReady) {
				return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
			}
			return r.setSnapshotPolicyErrorCondition(ctx, &sp, "Reconcile", "VolumeNotFound", err.Error(), err)
		}
	}

	return r.reconcileSnapshotPolicy(ctx, sp, volumeIdentities)
}

// toThalassaTarget builds the Thalassa target. When type is explicit, volumeIdentities must be the resolved identities from spec.target.volumes.
func toThalassaTarget(t iaasv1.SnapshotPolicyTarget, volumeIdentities []string) thalassaiaas.SnapshotPolicyTarget {
	out := thalassaiaas.SnapshotPolicyTarget{
		Type:             thalassaiaas.SnapshotPolicyTargetType(t.Type),
		Selector:         t.Selector,
		VolumeIdentities: volumeIdentities,
	}
	return out
}

func (r *SnapshotPolicyReconciler) createSnapshotPolicy(ctx context.Context, sp iaasv1.SnapshotPolicy, volumeIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createSnapshotPolicy")

	SetStandardConditions(&sp.Status.Conditions, ConditionStateProgressing, "Creating", "Creating snapshot policy in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &sp); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	keepCount := (*int)(nil)
	if sp.Spec.KeepCount != nil {
		k := int(*sp.Spec.KeepCount)
		keepCount = &k
	}
	createReq := thalassaiaas.CreateSnapshotPolicyRequest{
		Name:        effectiveName(sp.Name, sp.Spec.Metadata),
		Description: sp.Spec.Description,
		Labels:      effectiveLabels(sp.Spec.Metadata),
		Region:      sp.Spec.Region,
		Ttl:         sp.Spec.TTL.Duration,
		KeepCount:   keepCount,
		Enabled:     sp.Spec.Enabled,
		Schedule:    sp.Spec.Schedule,
		Timezone:    sp.Spec.Timezone,
		Target:      toThalassaTarget(sp.Spec.Target, volumeIdentities),
	}
	created, err := r.IaaSClient.CreateSnapshotPolicy(ctx, createReq)
	if err != nil {
		return r.setSnapshotPolicyErrorCondition(ctx, &sp, "createSnapshotPolicy", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	sp.Status.ResourceID = identity
	sp.Status.LastReconcileError = ""
	SetStandardConditions(&sp.Status.Conditions, ConditionStateAvailable, "Created", "Snapshot policy is created in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &sp); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	r.Recorder.Eventf(&sp, corev1.EventTypeNormal, "Provisioned", "Snapshot policy provisioned in Thalassa (id: %s)", identity)
	log.Info("created snapshot policy in Thalassa", "identity", identity)
	return ctrl.Result{}, nil
}

func (r *SnapshotPolicyReconciler) reconcileSnapshotPolicy(ctx context.Context, sp iaasv1.SnapshotPolicy, volumeIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileSnapshotPolicy")
	identity := sp.Status.ResourceID
	if identity == "" {
		return r.createSnapshotPolicy(ctx, sp, volumeIdentities)
	}

	fetched, err := r.IaaSClient.GetSnapshotPolicy(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&sp.Status.Conditions, ConditionStateDegraded, "NotFound", "Snapshot policy not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &sp); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setSnapshotPolicyErrorCondition(ctx, &sp, "reconcileSnapshotPolicy", "NotFound", "Snapshot policy not found in Thalassa", err)
		}
		return r.setSnapshotPolicyErrorCondition(ctx, &sp, "reconcileSnapshotPolicy", "GetFailed", "Failed to get snapshot policy from Thalassa", err)
	}

	if r.requiresUpdate(&sp, fetched, volumeIdentities) {
		SetStandardConditions(&sp.Status.Conditions, ConditionStateProgressing, "Updating", "Updating snapshot policy in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &sp); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		keepCount := (*int)(nil)
		if sp.Spec.KeepCount != nil {
			k := int(*sp.Spec.KeepCount)
			keepCount = &k
		}
		_, err = r.IaaSClient.UpdateSnapshotPolicy(ctx, identity, thalassaiaas.UpdateSnapshotPolicyRequest{
			Name:        effectiveName(sp.Name, sp.Spec.Metadata),
			Description: sp.Spec.Description,
			Labels:      effectiveLabels(sp.Spec.Metadata),
			Ttl:         sp.Spec.TTL.Duration,
			KeepCount:   keepCount,
			Enabled:     sp.Spec.Enabled,
			Schedule:    sp.Spec.Schedule,
			Timezone:    sp.Spec.Timezone,
			Target:      toThalassaTarget(sp.Spec.Target, volumeIdentities),
		})
		if err != nil {
			return r.setSnapshotPolicyErrorCondition(ctx, &sp, "reconcileSnapshotPolicy", "FailedUpdate", err.Error(), err)
		}
		log.Info("updated snapshot policy in Thalassa", "identity", identity)
	}

	SetStandardConditions(&sp.Status.Conditions, ConditionStateAvailable, "Reconciled", "Snapshot policy is up to date")
	sp.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &sp); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *SnapshotPolicyReconciler) reconcileDelete(ctx context.Context, sp *iaasv1.SnapshotPolicy) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	identity := sp.Status.ResourceID
	if identity != "" {
		if err := r.IaaSClient.DeleteSnapshotPolicy(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			return r.setSnapshotPolicyErrorCondition(ctx, sp, "reconcileDelete", "DeleteFailed", err.Error(), err)
		}
	}
	if controllerutil.RemoveFinalizer(sp, snapshotpolicyFinalizer) {
		if err := r.Update(ctx, sp); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(sp, corev1.EventTypeNormal, "Deleted", "Finished deletion")
	}
	log.Info("deleted snapshot policy in Thalassa", "identity", identity)
	return ctrl.Result{}, nil
}

func (r *SnapshotPolicyReconciler) updateStatusWithRetry(ctx context.Context, sp *iaasv1.SnapshotPolicy) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.SnapshotPolicy{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: sp.Namespace, Name: sp.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest SnapshotPolicy: %w", err)
		}
		latest.Status = sp.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *SnapshotPolicyReconciler) setSnapshotPolicyErrorCondition(ctx context.Context, sp *iaasv1.SnapshotPolicy, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&sp.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		sp.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, sp); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		r.Recorder.Eventf(sp, corev1.EventTypeWarning, reason, "%s", message)
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *SnapshotPolicyReconciler) requiresUpdate(sp *iaasv1.SnapshotPolicy, fetched *thalassaiaas.SnapshotPolicy, volumeIdentities []string) bool {
	if effectiveName(sp.Name, sp.Spec.Metadata) != fetched.Name {
		return true
	}
	if sp.Spec.Description != fetched.Description {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabels(sp.Spec.Metadata), fetched.Labels) {
		return true
	}
	if sp.Spec.TTL.Duration != fetched.Ttl {
		return true
	}
	if !equality.Semantic.DeepEqual(ptrToInt32FromIntPtr(fetched.KeepCount), sp.Spec.KeepCount) {
		return true
	}
	if sp.Spec.Enabled != fetched.Enabled {
		return true
	}
	if sp.Spec.Schedule != fetched.Schedule {
		return true
	}
	if sp.Spec.Timezone != fetched.Timezone {
		return true
	}
	// Compare target
	target := sp.Spec.Target
	if string(target.Type) != string(fetched.Target.Type) {
		return true
	}
	if target.Type == iaasv1.SnapshotPolicyTargetTypeSelector {
		if !equality.Semantic.DeepEqual(target.Selector, fetched.Target.Selector) {
			return true
		}
	} else {
		if !equality.Semantic.DeepEqual(volumeIdentities, fetched.Target.VolumeIdentities) {
			return true
		}
	}
	return false
}

func (r *SnapshotPolicyReconciler) resolveVolumeRef(ctx context.Context, defaultNamespace string, ref iaasv1.VolumeRef) (string, error) {
	if ref.Id != "" {
		return ref.Id, nil
	}
	if ref.Name == "" {
		return "", errors.New("volumeRef must have id or name set")
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

func (r *SnapshotPolicyReconciler) resolveVolumeRefs(ctx context.Context, defaultNamespace string, refs []iaasv1.VolumeRef) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	identities := make([]string, 0, len(refs))
	for _, ref := range refs {
		id, err := r.resolveVolumeRef(ctx, defaultNamespace, ref)
		if err != nil {
			return nil, err
		}
		identities = append(identities, id)
	}
	return identities, nil
}

// ptrToInt32FromIntPtr returns *int32 from *int for comparison with spec.keepCount (*int32).
func ptrToInt32FromIntPtr(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

// SetupWithManager sets up the controller with the Manager.
func (r *SnapshotPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("iaas-controller.snapshotpolicy")
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.SnapshotPolicy{}).
		Named("snapshotpolicy").
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
