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

// SecurityGroupRule defines an ingress or egress rule.
type SecurityGroupRule struct {
	// Name is the name of the rule.
	// +optional
	Name string `json:"name,omitempty"`

	// Protocol is the protocol (e.g. tcp, udp, icmp, all).
	// +kubebuilder:validation:Required
	Protocol string `json:"protocol"`

	// PortRangeMin is the minimum port (1-65534). Ignored when protocol is "all" or "icmp".
	// +optional
	PortRangeMin *int32 `json:"portRangeMin,omitempty"`

	// PortRangeMax is the maximum port (1-65534). Ignored when protocol is "all" or "icmp".
	// +optional
	PortRangeMax *int32 `json:"portRangeMax,omitempty"`

	// RemoteAddress is the IP or CIDR the rule applies to (e.g. 0.0.0.0/0).
	// +optional
	RemoteAddress *string `json:"remoteAddress,omitempty"`

	// RemoteSecurityGroupIdentity is the Thalassa security group identity when allowing traffic from/to another security group.
	// +optional
	RemoteSecurityGroupIdentity *string `json:"remoteSecurityGroupIdentity,omitempty"`

	// Policy is the rule policy: "allow" or "deny".
	// +kubebuilder:validation:Enum=allow;deny
	// +kubebuilder:default=allow
	// +optional
	Policy string `json:"policy,omitempty"`

	// Priority is the rule priority (1-199).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=199
	// +optional
	Priority *int32 `json:"priority,omitempty"`
}

// SecurityGroupSpec defines the desired state of SecurityGroup
type SecurityGroupSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the security group.
	// +optional
	Description string `json:"description,omitempty"`

	// VPCRef references the VPC this security group belongs to.
	// +kubebuilder:validation:Required
	VPCRef VPCRef `json:"vpcRef"`

	// AllowSameGroupTraffic allows traffic between instances in the same security group.
	// +kubebuilder:default=true
	// +optional
	AllowSameGroupTraffic *bool `json:"allowSameGroupTraffic,omitempty"`

	// IngressRules are the ingress rules.
	// +optional
	IngressRules []SecurityGroupRule `json:"ingressRules,omitempty"`

	// EgressRules are the egress rules.
	// +optional
	EgressRules []SecurityGroupRule `json:"egressRules,omitempty"`
}

// SecurityGroupStatus defines the observed state of SecurityGroup.
type SecurityGroupStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the security group.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// conditions represent the current state of the SecurityGroup resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="VPC",type=string,JSONPath=`.spec.vpcRef.name`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// SecurityGroup is the Schema for the securitygroups API
type SecurityGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SecurityGroupSpec   `json:"spec"`
	Status SecurityGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SecurityGroupList contains a list of SecurityGroup
type SecurityGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SecurityGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SecurityGroup{}, &SecurityGroupList{})
}
