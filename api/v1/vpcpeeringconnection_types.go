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

// VpcPeeringConnectionSpec defines the desired state of VpcPeeringConnection
type VpcPeeringConnectionSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description.
	// +optional
	Description string `json:"description,omitempty"`

	// RequesterVPCRef is the VPC that initiates the peering request (local / requester).
	// +kubebuilder:validation:Required
	RequesterVPCRef VPCRef `json:"requesterVpcRef"`

	// AccepterVpcId is the Thalassa ID of the VPC that will accept the request (remote side).
	// +kubebuilder:validation:Required
	AccepterVpcId string `json:"accepterVpcId"`

	// AccepterOrganisationId is the ID of the organisation that owns the accepter VPC.
	// +kubebuilder:validation:Required
	AccepterOrganisationId string `json:"accepterOrganisationId"`

	// AutoAccept, when true, accepts the peering automatically (same org/region only).
	// +optional
	AutoAccept bool `json:"autoAccept,omitempty"`

	// Accept, when true, tells the controller to call Accept on the peering connection when it is pending.
	// Set to true to accept an incoming or outgoing pending peering.
	// +optional
	Accept *bool `json:"accept,omitempty"`

	// Reject, when true, tells the controller to call Reject on the peering connection when it is pending.
	// +optional
	Reject *bool `json:"reject,omitempty"`

	// RejectReason is an optional reason when rejecting (used when Reject is true).
	// +optional
	RejectReason string `json:"rejectReason,omitempty"`
}

// VpcPeeringConnectionStatus defines the observed state of VpcPeeringConnection.
type VpcPeeringConnectionStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the peering connection.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// Status is the Thalassa peering status (e.g. pending, active, rejected).
	// +optional
	Status string `json:"status,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Requester VPC",type=string,JSONPath=`.spec.requesterVpcRef.name`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.status`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// VpcPeeringConnection is the Schema for the vpcpeeringconnections API
type VpcPeeringConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   VpcPeeringConnectionSpec   `json:"spec"`
	Status VpcPeeringConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VpcPeeringConnectionList contains a list of VpcPeeringConnection
type VpcPeeringConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VpcPeeringConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&VpcPeeringConnection{}, &VpcPeeringConnectionList{})
}
