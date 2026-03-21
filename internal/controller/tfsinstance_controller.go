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
	"github.com/thalassa-cloud/client-go/tfs"
	iaasv1 "github.com/thalassa-cloud/iaas-controller/api/v1"
)

const tfsinstanceFinalizer = "iaas.controllers.thalassa.cloud/tfsinstance"

// TfsInstanceReconciler reconciles a TfsInstance object.
type TfsInstanceReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	IaaSClient *thalassaiaas.Client
	TFSClient  *tfs.Client
}

// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=vpcs,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=subnets,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=securitygroups,verbs=get;list;watch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=tfsinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=tfsinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=iaas.controllers.thalassa.cloud,resources=tfsinstances/finalizers,verbs=update

// Reconcile moves the current state of a TfsInstance toward the desired spec by creating or updating the TFS instance in Thalassa.
func (r *TfsInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "Reconcile")

	var tfsInst iaasv1.TfsInstance
	if err := r.Get(ctx, req.NamespacedName, &tfsInst); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if r.TFSClient == nil {
		log.Info("TFS client not configured, skipping reconciliation")
		return ctrl.Result{}, nil
	}
	if IsSuspended(&tfsInst) {
		return ctrl.Result{}, nil
	}

	tfsInst.Status.ObservedGeneration, tfsInst.Status.LastReconcileTime = ReconcileMeta(tfsInst.Generation)

	if !tfsInst.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &tfsInst)
	}
	if controllerutil.AddFinalizer(&tfsInst, tfsinstanceFinalizer) {
		if err := r.Update(ctx, &tfsInst); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	vpcIdentity, err := r.resolveVPCRef(ctx, tfsInst.Namespace, tfsInst.Spec.VPCRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "Reconcile", "VPCNotFound", err.Error(), err)
	}
	subnetIdentity, err := r.resolveSubnetRef(ctx, tfsInst.Namespace, tfsInst.Spec.SubnetRef)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "Reconcile", "SubnetNotFound", err.Error(), err)
	}
	sgIdentities, err := r.resolveSecurityGroupRefs(ctx, tfsInst.Namespace, tfsInst.Spec.SecurityGroupRefs)
	if err != nil {
		if errors.Is(err, ErrDependencyNotReady) {
			return ctrl.Result{RequeueAfter: RequeueAfterDependencyNotReady}, nil
		}
		return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "Reconcile", "SecurityGroupNotFound", err.Error(), err)
	}
	cloudRegionIdentity, err := r.getCloudRegionIdentity(ctx, &tfsInst, vpcIdentity)
	if err != nil {
		return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "Reconcile", "RegionNotFound", err.Error(), err)
	}
	return r.reconcileTfsInstance(ctx, tfsInst, cloudRegionIdentity, vpcIdentity, subnetIdentity, sgIdentities)
}

func (r *TfsInstanceReconciler) createTfsInstance(ctx context.Context, tfsInst iaasv1.TfsInstance, cloudRegionIdentity, vpcIdentity, subnetIdentity string, sgIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "createTfsInstance")

	SetStandardConditions(&tfsInst.Status.Conditions, ConditionStateProgressing, "Creating", "Creating TFS instance in Thalassa")
	if updateErr := r.updateStatusWithRetry(ctx, &tfsInst); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}

	createReq := tfs.CreateTfsInstanceRequest{
		Name:                     effectiveName(tfsInst.Name, tfsInst.Spec.Metadata),
		Description:              tfsInst.Spec.Description,
		Labels:                   effectiveLabelsTfs(tfsInst.Spec.Metadata),
		CloudRegionIdentity:      cloudRegionIdentity,
		VpcIdentity:              vpcIdentity,
		SubnetIdentity:           subnetIdentity,
		SizeGB:                   int(tfsInst.Spec.SizeGB),
		SecurityGroupAttachments: sgIdentities,
		DeleteProtection:         tfsInst.Spec.DeleteProtection,
	}
	created, err := r.TFSClient.CreateTfsInstance(ctx, createReq)
	if err != nil {
		err := fmt.Errorf("failed to create TFS instance: %w", err)
		return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "createTfsInstance", "FailedCreate", err.Error(), err)
	}
	identity := created.Identity
	tfsInst.Status.ResourceID = identity
	tfsInst.Status.ResourceStatus = string(created.Status)
	tfsInst.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &tfsInst); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	if err := r.TFSClient.WaitUntilTfsInstanceIsAvailable(ctx, identity); err != nil {
		log.Error(err, "failed to wait until TFS instance is available, will retry later")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	log.Info("created TFS instance in Thalassa", "identity", identity)

	fetched, err := r.TFSClient.GetTfsInstance(ctx, identity)
	if err != nil {
		return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "createTfsInstance", "GetFailed", "Failed to get TFS instance after create", err)
	}
	tfsInst.Status.ResourceStatus = string(fetched.Status)
	SetStandardConditions(&tfsInst.Status.Conditions, ConditionStateAvailable, "Created", "TFS instance is created in Thalassa")
	tfsInst.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &tfsInst); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *TfsInstanceReconciler) reconcileTfsInstance(ctx context.Context, tfsInst iaasv1.TfsInstance, cloudRegionIdentity, vpcIdentity, subnetIdentity string, sgIdentities []string) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileTfsInstance")
	identity := tfsInst.Status.ResourceID
	if identity == "" {
		return r.createTfsInstance(ctx, tfsInst, cloudRegionIdentity, vpcIdentity, subnetIdentity, sgIdentities)
	}

	fetched, err := r.TFSClient.GetTfsInstance(ctx, identity)
	if err != nil {
		if thalassaclient.IsNotFound(err) {
			SetStandardConditions(&tfsInst.Status.Conditions, ConditionStateDegraded, "NotFound", "TFS instance not found in Thalassa")
			if updateErr := r.updateStatusWithRetry(ctx, &tfsInst); updateErr != nil {
				log.Error(updateErr, "failed to update status")
				return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
			}
			return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "reconcileTfsInstance", "NotFound", "TFS instance not found in Thalassa", err)
		}
		return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "reconcileTfsInstance", "GetFailed", "Failed to get TFS instance from Thalassa", err)
	}

	tfsInst.Status.ResourceStatus = string(fetched.Status)
	if r.requiresUpdate(&tfsInst, fetched, sgIdentities) {
		SetStandardConditions(&tfsInst.Status.Conditions, ConditionStateProgressing, "Updating", "Updating TFS instance in Thalassa")
		if updateErr := r.updateStatusWithRetry(ctx, &tfsInst); updateErr != nil {
			log.Error(updateErr, "failed to update status")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		_, err = r.TFSClient.UpdateTfsInstance(ctx, identity, tfs.UpdateTfsInstanceRequest{
			Name:                     effectiveName(tfsInst.Name, tfsInst.Spec.Metadata),
			Description:              tfsInst.Spec.Description,
			Labels:                   effectiveLabelsTfs(tfsInst.Spec.Metadata),
			SizeGB:                   int(tfsInst.Spec.SizeGB),
			SecurityGroupAttachments: sgIdentities,
			DeleteProtection:         tfsInst.Spec.DeleteProtection,
		})
		if err != nil {
			return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "reconcileTfsInstance", "FailedUpdate", err.Error(), err)
		}
		fetched, err = r.TFSClient.GetTfsInstance(ctx, identity)
		if err != nil {
			return r.setTfsInstanceErrorCondition(ctx, &tfsInst, "reconcileTfsInstance", "GetFailed", "Failed to get TFS instance after update", err)
		}
		tfsInst.Status.ResourceStatus = string(fetched.Status)
	}

	SetStandardConditions(&tfsInst.Status.Conditions, ConditionStateAvailable, "Reconciled", "TFS instance is up to date")
	tfsInst.Status.LastReconcileError = ""
	if updateErr := r.updateStatusWithRetry(ctx, &tfsInst); updateErr != nil {
		log.Error(updateErr, "failed to update status")
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
	}
	return ctrl.Result{}, nil
}

func (r *TfsInstanceReconciler) reconcileDelete(ctx context.Context, tfsInst *iaasv1.TfsInstance) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", "reconcileDelete")
	identity := tfsInst.Status.ResourceID
	if identity != "" {
		if err := r.TFSClient.DeleteTfsInstance(ctx, identity); err != nil && !thalassaclient.IsNotFound(err) {
			return r.setTfsInstanceErrorCondition(ctx, tfsInst, "reconcileDelete", "DeleteFailed", err.Error(), err)
		}
		if err := r.TFSClient.WaitUntilTfsInstanceIsDeleted(ctx, identity); err != nil {
			log.Error(err, "failed to wait until TFS instance is deleted, will retry")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
		}
	}
	if controllerutil.RemoveFinalizer(tfsInst, tfsinstanceFinalizer) {
		if err := r.Update(ctx, tfsInst); err != nil {
			log.Error(err, "failed to remove finalizer")
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *TfsInstanceReconciler) updateStatusWithRetry(ctx context.Context, tfsInst *iaasv1.TfsInstance) error {
	return updateStatusWithRetry(func() error {
		latest := &iaasv1.TfsInstance{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: tfsInst.Namespace, Name: tfsInst.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest TfsInstance: %w", err)
		}
		latest.Status = tfsInst.Status
		return r.Status().Update(ctx, latest)
	})
}

func (r *TfsInstanceReconciler) setTfsInstanceErrorCondition(ctx context.Context, tfsInst *iaasv1.TfsInstance, method, reason, message string, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("method", method)
	log.Error(err, "reconciliation error", "reason", reason)
	if meta.SetStatusCondition(&tfsInst.Status.Conditions, metav1.Condition{Type: "Error", Status: metav1.ConditionFalse, Reason: reason, Message: message}) {
		tfsInst.Status.LastReconcileError = err.Error()
		if updateErr := r.updateStatusWithRetry(ctx, tfsInst); updateErr != nil {
			log.Error(updateErr, "failed to persist error condition")
			return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, updateErr
		}
		return ctrl.Result{RequeueAfter: RequeueAfterStatusUpdateFailure}, nil
	}
	return ctrl.Result{}, nil
}

func (r *TfsInstanceReconciler) requiresUpdate(tfsInst *iaasv1.TfsInstance, fetched *tfs.TfsInstance, sgIdentities []string) bool {
	if effectiveName(tfsInst.Name, tfsInst.Spec.Metadata) != fetched.Name {
		return true
	}
	if tfsInst.Spec.Description != stringPtrVal(fetched.Description) {
		return true
	}
	if !equality.Semantic.DeepEqual(effectiveLabelsTfs(tfsInst.Spec.Metadata), fetched.Labels) {
		return true
	}
	if int(tfsInst.Spec.SizeGB) != fetched.SizeGB {
		return true
	}
	if tfsInst.Spec.DeleteProtection != fetched.DeleteProtection {
		return true
	}
	fetchedSGs := make([]string, 0, len(fetched.SecurityGroups))
	for _, sg := range fetched.SecurityGroups {
		fetchedSGs = append(fetchedSGs, sg.Identity)
	}
	return !equality.Semantic.DeepEqual(sgIdentities, fetchedSGs)
}

func stringPtrVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// effectiveLabelsTfs returns labels for the TFS client from ResourceMetadata.
func effectiveLabelsTfs(resourceMeta *iaasv1.ResourceMetadata) tfs.Labels {
	if resourceMeta != nil && len(resourceMeta.Labels) > 0 {
		return tfs.Labels(resourceMeta.Labels)
	}
	return nil
}

// getCloudRegionIdentity returns spec.CloudRegionIdentity if set, otherwise the region identity of the VPC.
func (r *TfsInstanceReconciler) getCloudRegionIdentity(ctx context.Context, tfsInst *iaasv1.TfsInstance, vpcIdentity string) (string, error) {
	if tfsInst.Spec.Region != "" {
		return tfsInst.Spec.Region, nil
	}
	if r.IaaSClient == nil {
		return "", fmt.Errorf("IaaS client not configured; set cloudRegionIdentity in spec or configure IaaS client")
	}
	vpc, err := r.IaaSClient.GetVpc(ctx, vpcIdentity)
	if err != nil {
		return "", fmt.Errorf("failed to get VPC for region: %w", err)
	}
	if vpc.CloudRegion == nil {
		return "", fmt.Errorf("VPC has no cloud region; set spec.cloudRegionIdentity explicitly")
	}
	if vpc.CloudRegion.Identity != "" {
		return vpc.CloudRegion.Identity, nil
	}
	if vpc.CloudRegion.Slug != "" {
		return vpc.CloudRegion.Slug, nil
	}
	return "", fmt.Errorf("VPC region has no identity or slug; set spec.cloudRegionIdentity explicitly")
}

func (r *TfsInstanceReconciler) resolveVPCRef(ctx context.Context, defaultNamespace string, ref iaasv1.VPCRef) (string, error) {
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

func (r *TfsInstanceReconciler) resolveSubnetRef(ctx context.Context, defaultNamespace string, ref iaasv1.SubnetRef) (string, error) {
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

func (r *TfsInstanceReconciler) resolveSecurityGroupRef(ctx context.Context, defaultNamespace string, ref iaasv1.SecurityGroupRef) (string, error) {
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

func (r *TfsInstanceReconciler) resolveSecurityGroupRefs(ctx context.Context, defaultNamespace string, refs []iaasv1.SecurityGroupRef) ([]string, error) {
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

// SetupWithManager sets up the controller with the Manager.
func (r *TfsInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&iaasv1.TfsInstance{}).
		Named("tfsinstance").
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
