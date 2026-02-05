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

// SubnetRef references a Subnet (by Kubernetes object or by Thalassa identity).
type SubnetRef struct {
	// Name is the name of the Subnet resource in Kubernetes.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Subnet resource. Defaults to the same namespace as the NatGateway.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Identity is the Thalassa subnet identity. If set, the controller uses this directly instead of resolving from the Subnet resource.
	// +optional
	Identity string `json:"identity,omitempty"`
}

// NatGatewaySpec defines the desired state of NatGateway
type NatGatewaySpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the NAT gateway.
	// +optional
	Description string `json:"description,omitempty"`

	// SubnetRef references the subnet this NAT gateway is created in.
	// +kubebuilder:validation:Required
	SubnetRef SubnetRef `json:"subnetRef"`

	// SecurityGroupRefs reference security groups to attach, either by Thalassa identity or by Kubernetes SecurityGroup resource.
	// +optional
	SecurityGroupRefs []SecurityGroupRef `json:"securityGroupRefs,omitempty"`

	// ConfigureDefaultRoute configures the default route for the NAT gateway in the subnet's route table.
	// +kubebuilder:default=true
	// +optional
	ConfigureDefaultRoute *bool `json:"configureDefaultRoute,omitempty"`
}

// NatGatewayStatus defines the observed state of NatGateway.
type NatGatewayStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the NAT gateway.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// EndpointIP is the gateway endpoint target in the VPC (the address used as the next hop for NAT traffic).
	// +optional
	EndpointIP string `json:"endpointIP,omitempty"`

	// V4IP is the IPv4 outbound address of the NAT gateway.
	// +optional
	V4IP string `json:"v4IP,omitempty"`

	// V6IP is the IPv6 outbound address of the NAT gateway.
	// +optional
	V6IP string `json:"v6IP,omitempty"`

	// conditions represent the current state of the NatGateway resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Subnet",type=string,JSONPath=`.spec.subnetRef.name`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Endpoint IP",type=string,JSONPath=`.status.endpointIP`
// +kubebuilder:printcolumn:name="V4 IP",type=string,JSONPath=`.status.v4IP`
// +kubebuilder:printcolumn:name="V6 IP",type=string,JSONPath=`.status.v6IP`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// NatGateway is the Schema for the natgateways API
type NatGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NatGatewaySpec   `json:"spec"`
	Status NatGatewayStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NatGatewayList contains a list of NatGateway
type NatGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NatGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NatGateway{}, &NatGatewayList{})
}
