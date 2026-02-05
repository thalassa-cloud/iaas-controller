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
	"errors"
	"strings"
	"time"

	thalassaiaas "github.com/thalassa-cloud/client-go/iaas"
	iaasv1 "github.com/thalassa-cloud/iaas-controller/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// RequeueAfterStatusUpdateFailure is the delay before requeuing when a status update fails.
const RequeueAfterStatusUpdateFailure = 15 * time.Second

// RequeueAfterDependencyNotReady is the delay when a referenced resource (VPC, Subnet, etc.) does not have ResourceID yet.
const RequeueAfterDependencyNotReady = 5 * time.Second

// ErrDependencyNotReady is returned by resolve helpers when the referenced resource exists but has no ResourceID yet. Callers should requeue instead of setting an error condition.
var ErrDependencyNotReady = errors.New("dependency does not have a resource ID yet")

// ResourceStatusReady is the value for ResourceStatus when the Thalassa resource is ready.
const ResourceStatusReady = "ready"

type ConditionState string

const (
	ConditionStateAvailable   ConditionState = "available"   // Resource is fully functional
	ConditionStateDegraded    ConditionState = "degraded"    // Resource failed to reach or maintain desired state
	ConditionStateProgressing ConditionState = "progressing" // Resource is being created or updated
)

// SetStandardConditions sets Ready and the standard condition types (Available, Progressing, Degraded)
// on the given conditions slice. state must be ConditionStateAvailable, ConditionStateDegraded, or ConditionStateProgressing.
func SetStandardConditions(conditions *[]metav1.Condition, state ConditionState, reason, message string) {
	switch state {
	case ConditionStateAvailable:
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionTrue, Reason: reason, Message: message})
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Progressing", Status: metav1.ConditionFalse, Reason: "Reconciled", Message: ""})
		meta.RemoveStatusCondition(conditions, "Degraded")
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: reason, Message: message})
	case ConditionStateDegraded:
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionFalse, Reason: reason, Message: message})
		meta.RemoveStatusCondition(conditions, "Progressing")
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Degraded", Status: metav1.ConditionTrue, Reason: reason, Message: message})
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: reason, Message: message})
	case ConditionStateProgressing:
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Available", Status: metav1.ConditionFalse, Reason: reason, Message: message})
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Progressing", Status: metav1.ConditionTrue, Reason: reason, Message: message})
		meta.RemoveStatusCondition(conditions, "Degraded")
		meta.SetStatusCondition(conditions, metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: reason, Message: message})
	}
}

// NeedStatusUpdate returns true if the Ready condition or reconcile fields differ from current status,
// so a status update should be persisted.
func NeedStatusUpdate(
	conditions []metav1.Condition,
	newReadyStatus metav1.ConditionStatus,
	newReason, newMessage string,
	newLastErr, newResourceStatus string,
	currentLastErr, currentResourceStatus string,
) bool {
	r := meta.FindStatusCondition(conditions, "Ready")
	if r == nil {
		return true
	}
	if r.Status != newReadyStatus || r.Reason != newReason || r.Message != newMessage {
		return true
	}
	if currentLastErr != newLastErr || currentResourceStatus != newResourceStatus {
		return true
	}
	return false
}

// ReconcileMeta returns ObservedGeneration and LastReconcileTime for the current reconcile.
// Assign to status at the start of reconcile: status.ObservedGeneration, status.LastReconcileTime = ReconcileMeta(obj.Generation).
func ReconcileMeta(generation int64) (observedGeneration int64, lastReconcileTime *metav1.Time) {
	now := metav1.Now()
	return generation, &now
}

// SuspendAnnotationKey is the annotation key to suspend reconciliation.
// When set to a truthy value (e.g. "true"), the controller skips reconciliation.
const SuspendAnnotationKey = "iaas.controllers.thalassa.cloud/suspend"

// IsSuspended returns true if the object has the suspend annotation set to a truthy value.
func IsSuspended(obj metav1.Object) bool {
	if obj == nil || obj.GetAnnotations() == nil {
		return false
	}
	v := obj.GetAnnotations()[SuspendAnnotationKey]
	return strings.EqualFold(v, "true") || v == "1"
}

// effectiveName returns the name to use for the Thalassa resource: spec.metadata.name if set, otherwise defaultName.
func effectiveName(defaultName string, resourceMeta *iaasv1.ResourceMetadata) string {
	if resourceMeta != nil && resourceMeta.Name != nil && *resourceMeta.Name != "" {
		return *resourceMeta.Name
	}
	return defaultName
}

// effectiveLabels returns the labels to use for the Thalassa resource: spec.metadata.labels if set, otherwise nil.
func effectiveLabels(resourceMeta *iaasv1.ResourceMetadata) thalassaiaas.Labels {
	if resourceMeta != nil && len(resourceMeta.Labels) > 0 {
		return thalassaiaas.Labels(resourceMeta.Labels)
	}
	return nil
}

// updateStatusWithRetry runs the status update in a retry loop using retry.OnError.
// The caller should pass a function that fetches the latest object, copies status from the in-memory object, and calls Status().Update.
func updateStatusWithRetry(doUpdate func() error) error {
	return retry.OnError(retry.DefaultRetry, func(err error) bool {
		return true
	}, doUpdate)
}
