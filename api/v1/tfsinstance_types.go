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

// TfsInstanceSpec defines the desired state of a TFS (Thalassa Filesystem Service) instance.
type TfsInstanceSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the TFS instance.
	// +optional
	Description string `json:"description,omitempty"`

	// Region is the Thalassa region identity or slug where the TFS instance is created.
	// When empty, the controller derives it from the VPC's region.
	// +optional
	Region string `json:"region,omitempty"`

	// VPCRef references the VPC to create the TFS instance in.
	// +kubebuilder:validation:Required
	VPCRef VPCRef `json:"vpcRef"`

	// SubnetRef references the subnet to create the TFS instance in.
	// +kubebuilder:validation:Required
	SubnetRef SubnetRef `json:"subnetRef"`

	// SizeGB is the size of the TFS instance in GB.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	SizeGB int32 `json:"sizeGB"`

	// SecurityGroupRefs reference security groups to attach, by Thalassa identity or by Kubernetes SecurityGroup resource.
	// +optional
	SecurityGroupRefs []SecurityGroupRef `json:"securityGroupRefs,omitempty"`

	// DeleteProtection prevents the TFS instance from being deleted in Thalassa when true.
	// +optional
	DeleteProtection bool `json:"deleteProtection,omitempty"`
}

// TfsInstanceStatus defines the observed state of TfsInstance.
type TfsInstanceStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa identity of the TFS instance.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// conditions represent the current state of the TfsInstance resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Size(GB)",type=integer,JSONPath=`.spec.sizeGB`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// TfsInstance is the Schema for the TFS (Thalassa Filesystem Service) instances API.
type TfsInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TfsInstanceSpec   `json:"spec"`
	Status TfsInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TfsInstanceList contains a list of TfsInstance.
type TfsInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TfsInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TfsInstance{}, &TfsInstanceList{})
}
