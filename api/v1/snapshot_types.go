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

// VolumeRef references a BlockVolume by Kubernetes object or by Thalassa identity.
type VolumeRef struct {
	// Name is the name of the BlockVolume resource in Kubernetes.
	// +optional
	Name string `json:"name,omitempty"`

	// Namespace is the namespace of the BlockVolume resource. Defaults to the same namespace as the referencing resource.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// id is the Thalassa volume Id. If set, the controller uses this directly.
	// +optional
	Id string `json:"id,omitempty"`
}

// SnapshotSpec defines the desired state of a Snapshot.
type SnapshotSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the snapshot.
	// +optional
	Description string `json:"description,omitempty"`

	// VolumeRef references the BlockVolume (or volume by identity) to snapshot. Exactly one of VolumeRef.Identity or VolumeRef.Name must be set.
	// +kubebuilder:validation:Required
	VolumeRef VolumeRef `json:"volumeRef"`

	// DeleteProtection prevents the snapshot from being deleted in Thalassa when true.
	// +optional
	DeleteProtection bool `json:"deleteProtection,omitempty"`
}

// SnapshotStatus defines the observed state of Snapshot.
type SnapshotStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the snapshot.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// conditions represent the current state of the Snapshot resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Volume",type=string,JSONPath=`.spec.volumeRef.name`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// Snapshot is the Schema for the snapshots API.
type Snapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SnapshotSpec   `json:"spec"`
	Status SnapshotStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SnapshotList contains a list of Snapshot.
type SnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Snapshot `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Snapshot{}, &SnapshotList{})
}
