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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SnapshotPolicyTargetType defines how target volumes are selected.
// +kubebuilder:validation:Enum=selector;explicit
type SnapshotPolicyTargetType string

const (
	// SnapshotPolicyTargetTypeSelector targets volumes by label selector.
	SnapshotPolicyTargetTypeSelector SnapshotPolicyTargetType = "selector"
	// SnapshotPolicyTargetTypeExplicit targets volumes by explicit identities.
	SnapshotPolicyTargetTypeExplicit SnapshotPolicyTargetType = "explicit"
)

// SnapshotPolicyTarget defines which volumes are included in the policy.
type SnapshotPolicyTarget struct {
	// Type is how targets are identified: "selector" (by labels) or "explicit" (by volume identities).
	// +kubebuilder:validation:Required
	Type SnapshotPolicyTargetType `json:"type"`

	// Selector is a map of label key-value pairs when Type is "selector". Only volumes matching all labels are included.
	// +optional
	Selector map[string]string `json:"selector,omitempty"`

	// Volumes is a list of volume references (BlockVolume by name or Thalassa identity) when Type is "explicit".
	// +optional
	Volumes []VolumeRef `json:"volumes,omitempty"`
}

// SnapshotPolicySpec defines the desired state of a SnapshotPolicy.
type SnapshotPolicySpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the policy.
	// +optional
	Description string `json:"description,omitempty"`

	// Region is the Thalassa region identity or slug where the policy operates. Volumes must be in this region.
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// Schedule is a cron expression for when to create snapshots (e.g. "0 2 * * *" for daily at 2 AM).
	// +kubebuilder:validation:Required
	Schedule string `json:"schedule"`

	// Timezone is the IANA timezone for the schedule (e.g. "UTC", "Europe/Amsterdam").
	// +kubebuilder:validation:Required
	Timezone string `json:"timezone"`

	// TTL is how long snapshots created by this policy are retained before automatic deletion.
	// +kubebuilder:validation:Required
	TTL metav1.Duration `json:"ttl"`

	// KeepCount is the maximum number of snapshots to retain. When reached, oldest snapshots are deleted. If not set, only TTL applies.
	// +optional
	KeepCount *int32 `json:"keepCount,omitempty"`

	// Enabled controls whether the policy creates new snapshots.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Target defines which volumes are included in the policy.
	// +kubebuilder:validation:Required
	Target SnapshotPolicyTarget `json:"target"`
}

// SnapshotPolicyStatus defines the observed state of SnapshotPolicy.
type SnapshotPolicyStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the snapshot policy.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// conditions represent the current state of the SnapshotPolicy resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// SnapshotPolicy is the Schema for the snapshot policies API.
type SnapshotPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SnapshotPolicySpec   `json:"spec"`
	Status SnapshotPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SnapshotPolicyList contains a list of SnapshotPolicy.
type SnapshotPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SnapshotPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SnapshotPolicy{}, &SnapshotPolicyList{})
}
