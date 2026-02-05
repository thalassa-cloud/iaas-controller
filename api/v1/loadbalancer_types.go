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

// TargetGroupRef references a TargetGroup (by Kubernetes object or by Thalassa identity).
type TargetGroupRef struct {
	// Name is the name of the TargetGroup resource in Kubernetes.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the TargetGroup resource. Defaults to the same namespace as the Loadbalancer.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Identity is the Thalassa target group identity. If set, the controller uses this directly.
	// +optional
	Identity string `json:"identity,omitempty"`
}

// LoadbalancerListenerSpec defines a listener to create on the loadbalancer.
type LoadbalancerListenerSpec struct {
	// Name is the name of the listener.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Description is an optional description.
	// +optional
	Description string `json:"description,omitempty"`

	// Port is the port the listener listens on (1-65535).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Protocol is the protocol (e.g. TCP, HTTP, HTTPS).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=tcp;udp
	Protocol string `json:"protocol"`

	// TargetGroupRef references the target group for this listener.
	// +kubebuilder:validation:Required
	TargetGroupRef TargetGroupRef `json:"targetGroupRef"`

	// MaxConnections is the maximum number of connections the listener can handle.
	// +optional
	MaxConnections *uint32 `json:"maxConnections,omitempty"`

	// ConnectionIdleTimeout is the idle connection timeout in seconds.
	// +optional
	ConnectionIdleTimeout *uint32 `json:"connectionIdleTimeout,omitempty"`

	// AllowedSources is a list of CIDR blocks allowed to connect to the listener.
	// +optional
	AllowedSources []string `json:"allowedSources,omitempty"`
}

// LoadbalancerSpec defines the desired state of Loadbalancer
type LoadbalancerSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the loadbalancer.
	// +optional
	Description string `json:"description,omitempty"`

	// SubnetRef references the subnet in which the loadbalancer is deployed.
	// +kubebuilder:validation:Required
	SubnetRef SubnetRef `json:"subnetRef"`

	// InternalLoadbalancer, when true, creates an internal loadbalancer without a public IP.
	// +optional
	InternalLoadbalancer bool `json:"internalLoadbalancer,omitempty"`

	// DeleteProtection, when true, prevents deletion until explicitly disabled.
	// +optional
	DeleteProtection bool `json:"deleteProtection,omitempty"`

	// Listeners are the listeners to create on the loadbalancer.
	// +optional
	Listeners []LoadbalancerListenerSpec `json:"listeners,omitempty"`

	// SecurityGroupRefs reference security groups to attach, either by Thalassa identity or by Kubernetes SecurityGroup resource.
	// +optional
	SecurityGroupRefs []SecurityGroupRef `json:"securityGroupRefs,omitempty"`
}

// LoadbalancerStatus defines the observed state of Loadbalancer.
type LoadbalancerStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the loadbalancer.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// ExternalIPAddresses are the external IP addresses of the loadbalancer.
	// +optional
	ExternalIPAddresses []string `json:"externalIpAddresses,omitempty"`

	// InternalIPAddresses are the internal IP addresses of the loadbalancer.
	// +optional
	InternalIPAddresses []string `json:"internalIpAddresses,omitempty"`

	// Hostname is the hostname of the loadbalancer.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// conditions represent the current state of the Loadbalancer resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Subnet",type=string,JSONPath=`.spec.subnetRef.name`
// +kubebuilder:printcolumn:name="External IPs",type=string,JSONPath=`.status.externalIpAddresses`
// +kubebuilder:printcolumn:name="Internal IPs",type=string,JSONPath=`.status.internalIpAddresses`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Hostname",type=string,JSONPath=`.status.hostname`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// Loadbalancer is the Schema for the loadbalancers API
type Loadbalancer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LoadbalancerSpec   `json:"spec"`
	Status LoadbalancerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// LoadbalancerList contains a list of Loadbalancer
type LoadbalancerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Loadbalancer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Loadbalancer{}, &LoadbalancerList{})
}
