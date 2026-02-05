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

// VPCRef references a VPC (by Kubernetes object or by Thalassa identity).
type VPCRef struct {
	// Name is the name of the VPC resource in Kubernetes.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the VPC resource. Defaults to the same namespace as the Subnet.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Identity is the Thalassa VPC identity. If set, the controller uses this directly instead of resolving from the VPC resource.
	// +optional
	Identity string `json:"identity,omitempty"`
}

// SubnetSpec defines the desired state of Subnet
type SubnetSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the subnet.
	// +optional
	Description string `json:"description,omitempty"`

	// ResourceID is the Thalassa IaaS identity of an already-provisioned subnet. When set, this Subnet resource is a
	// reference to that external resource; the controller does not create, update, or delete the cloud subnet.
	// Mutually exclusive with VPCRef and CIDR.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// VPCRef references the VPC this subnet belongs to. Required when ResourceID is not set.
	// +optional
	VPCRef VPCRef `json:"vpcRef,omitempty"`

	// CIDR is the CIDR block for the subnet (e.g. 10.0.1.0/24). Required when ResourceID is not set.
	// +optional
	CIDR string `json:"cidr,omitempty"`
}

// SubnetStatus defines the observed state of Subnet.
type SubnetStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the subnet.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// conditions represent the current state of the Subnet resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="VPC",type=string,JSONPath=`.spec.vpcRef.name`
// +kubebuilder:printcolumn:name="CIDR",type=string,JSONPath=`.spec.cidr`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// Subnet is the Schema for the subnets API
type Subnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetSpec   `json:"spec"`
	Status SubnetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SubnetList contains a list of Subnet
type SubnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subnet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Subnet{}, &SubnetList{})
}
