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

// RouteTableRef references a RouteTable (by Kubernetes object or by Thalassa identity).
type RouteTableRef struct {
	// Name is the name of the RouteTable resource in Kubernetes.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the RouteTable resource. Defaults to the same namespace as the RouteTableRoute.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Identity is the Thalassa route table identity. If set, the controller uses this directly.
	// +optional
	Identity string `json:"identity,omitempty"`
}

// TargetGatewayRefKind is the kind of gateway resource that can be used as a route target.
// +kubebuilder:validation:Enum=NatGateway;VpcPeeringConnection
type TargetGatewayRefKind string

const (
	// TargetGatewayRefKindNatGateway references a NatGateway resource.
	TargetGatewayRefKindNatGateway TargetGatewayRefKind = "NatGateway"
	// TargetGatewayRefKindVpcPeeringConnection references a VpcPeeringConnection resource.
	TargetGatewayRefKindVpcPeeringConnection TargetGatewayRefKind = "VpcPeeringConnection"
)

// TargetGatewayRef references a gateway by Kubernetes resource (name, namespace, kind).
// The controller resolves it to the Thalassa identity. Mutually exclusive with
// TargetNatGatewayId, TargetGatewayId, and TargetVpcPeeringConnectionId.
type TargetGatewayRef struct {
	// Kind is the type of resource: NatGateway or VpcPeeringConnection.
	// +kubebuilder:validation:Required
	Kind TargetGatewayRefKind `json:"kind"`

	// Name is the name of the resource (NatGateway or VpcPeeringConnection).
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the resource. Defaults to the same namespace as the RouteTableRoute.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// RouteTableRouteSpec defines the desired state of RouteTableRoute (a single route entry).
type RouteTableRouteSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// RouteTableRef references the route table this route belongs to.
	// +kubebuilder:validation:Required
	RouteTableRef RouteTableRef `json:"routeTableRef"`

	// DestinationCidrBlock is the destination CIDR (e.g. 0.0.0.0/0).
	// +kubebuilder:validation:Required
	DestinationCidrBlock string `json:"destinationCidrBlock"`

	// TargetGatewayRef references a NatGateway or VpcPeeringConnection by resource name/namespace/kind.
	// When set, the controller resolves it to the Thalassa identity. Mutually exclusive with
	// targetNatGatewayId, targetGatewayId, and targetVpcPeeringConnectionId.
	// +optional
	TargetGatewayRef *TargetGatewayRef `json:"targetGatewayRef,omitempty"`

	// TargetNatGatewayId is the Thalassa NAT gateway identity. Ignored if TargetGatewayRef is set.
	// +optional
	TargetNatGatewayId string `json:"targetNatGatewayId,omitempty"`

	// TargetGatewayId is the Thalassa gateway identity. Ignored if TargetGatewayRef is set.
	// +optional
	TargetGatewayId string `json:"targetGatewayId,omitempty"`

	// TargetVpcPeeringConnectionId is the Thalassa VPC peering connection identity. Ignored if TargetGatewayRef is set.
	// +optional
	TargetVpcPeeringConnectionId *string `json:"targetVpcPeeringConnectionId,omitempty"`

	// GatewayAddress is the gateway IP address (for local/blackhole-style routes).
	// +optional
	GatewayAddress string `json:"gatewayAddress,omitempty"`
}

// RouteTableRouteStatus defines the observed state of RouteTableRoute.
type RouteTableRouteStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa identity of the route entry.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Route Table",type=string,JSONPath=`.spec.routeTableRef.name`
// +kubebuilder:printcolumn:name="Destination",type=string,JSONPath=`.spec.destinationCidrBlock`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// RouteTableRoute is the Schema for the routetableroutes API
type RouteTableRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RouteTableRouteSpec   `json:"spec"`
	Status RouteTableRouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RouteTableRouteList contains a list of RouteTableRoute
type RouteTableRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RouteTableRoute `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RouteTableRoute{}, &RouteTableRouteList{})
}
