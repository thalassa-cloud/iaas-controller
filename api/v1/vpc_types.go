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

// VPCSpec defines the desired state of VPC
type VPCSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the VPC.
	// +optional
	Description string `json:"description,omitempty"`

	// ResourceID is the Thalassa IaaS identity of an already-provisioned VPC. When set, this VPC resource is a
	// reference to that external resource; the controller does not create, update, or delete the cloud VPC.
	// Mutually exclusive with Region and CIDRBlocks.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// Region is the Thalassa cloud region identity where the VPC will be created. Required when ResourceID is not set.
	// +optional
	Region string `json:"region,omitempty"`

	// CIDRBlocks are the CIDR blocks for the VPC (e.g. 10.0.0.0/16). Required when ResourceID is not set.
	// +optional
	CIDRBlocks []string `json:"cidrBlocks,omitempty"`
}

// VPCStatus defines the observed state of VPC.
type VPCStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the VPC.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// conditions represent the current state of the VPC resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Region",type=string,JSONPath=`.spec.region`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// VPC is the Schema for the vpcs API
type VPC struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VPCSpec   `json:"spec"`
	Status VPCStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VPCList contains a list of VPC
type VPCList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VPC `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VPC{}, &VPCList{})
}
