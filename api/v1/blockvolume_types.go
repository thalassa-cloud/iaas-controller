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

// BlockVolumeSpec defines the desired state of a block volume (Thalassa Volume).
type BlockVolumeSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the volume.
	// +optional
	Description string `json:"description,omitempty"`

	// Size is the size of the volume in GB.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Size int32 `json:"size"`

	// Region is the Thalassa region identity or slug where the volume is created.
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// VolumeTypeId is the Thalassa volume type identity (e.g. SSD, HDD).
	// +kubebuilder:validation:Required
	VolumeTypeId string `json:"volumeTypeId"`

	// Type is the volume type kind sent to Thalassa (e.g. "block"). If empty, the API default is used.
	// +optional
	Type string `json:"type,omitempty"`

	// RestoreFromSnapshotIdentity is the identity of a snapshot to restore the volume from. The snapshot region must match Region.
	// +optional
	RestoreFromSnapshotIdentity *string `json:"restoreFromSnapshotIdentity,omitempty"`

	// DeleteProtection prevents the volume from being deleted in Thalassa when true.
	// +optional
	DeleteProtection bool `json:"deleteProtection,omitempty"`
}

// BlockVolumeStatus defines the observed state of BlockVolume.
type BlockVolumeStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the volume.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// conditions represent the current state of the BlockVolume resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Size",type=integer,JSONPath=`.spec.size`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// BlockVolume is the Schema for the block volumes API (Thalassa Volume).
type BlockVolume struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BlockVolumeSpec   `json:"spec"`
	Status BlockVolumeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BlockVolumeList contains a list of BlockVolume.
type BlockVolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BlockVolume `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BlockVolume{}, &BlockVolumeList{})
}
