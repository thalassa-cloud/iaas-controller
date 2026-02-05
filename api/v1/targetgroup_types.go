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

// TargetGroupAttachment defines a server/endpoint to attach to the target group.
type TargetGroupAttachment struct {
	// ServerIdentity is the Thalassa server (machine) identity to attach.
	// +kubebuilder:validation:Required
	ServerIdentity string `json:"serverIdentity"`

	// EndpointIdentity is the endpoint identity on the server (optional).
	// +optional
	EndpointIdentity string `json:"endpointIdentity,omitempty"`
}

// HealthCheck defines health check settings for the target group.
type HealthCheck struct {
	// Protocol is the protocol (TCP, HTTP, HTTPS).
	// +kubebuilder:validation:Required
	Protocol string `json:"protocol"`

	// Port is the health check port (1-65535).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Path is the path for HTTP(S) health checks.
	// +optional
	Path string `json:"path,omitempty"`

	// PeriodSeconds is the interval between checks (5-300).
	// +optional
	PeriodSeconds int `json:"periodSeconds,omitempty"`

	// TimeoutSeconds is the timeout (1-300).
	// +optional
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// UnhealthyThreshold is the number of failures before marking unhealthy (1-10).
	// +optional
	UnhealthyThreshold int32 `json:"unhealthyThreshold,omitempty"`

	// HealthyThreshold is the number of successes before marking healthy (1-10).
	// +optional
	HealthyThreshold int32 `json:"healthyThreshold,omitempty"`
}

// TargetGroupSpec defines the desired state of TargetGroup
type TargetGroupSpec struct {
	// Metadata allows optional name and label overrides for the created resource in Thalassa.
	// +optional
	Metadata *ResourceMetadata `json:"metadata,omitempty"`

	// Description is an optional description of the target group.
	// +optional
	Description string `json:"description,omitempty"`

	// VPCRef references the VPC this target group belongs to.
	// +kubebuilder:validation:Required
	VPCRef VPCRef `json:"vpcRef"`

	// TargetPort is the port for the target group (1-65535).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	TargetPort int `json:"targetPort"`

	// Protocol is the protocol (TCP, UDP).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=tcp;udp
	Protocol string `json:"protocol"`

	// TargetSelector is a map of labels to match servers; matching servers are attached automatically.
	// +optional
	TargetSelector map[string]string `json:"targetSelector,omitempty"`

	// Attachments are the explicit server/endpoint attachments. Reconcile sets the target group attachments to this list.
	// +optional
	Attachments []TargetGroupAttachment `json:"attachments,omitempty"`

	// EnableProxyProtocol enables proxy protocol.
	// +optional
	EnableProxyProtocol *bool `json:"enableProxyProtocol,omitempty"`

	// LoadbalancingPolicy is the policy: ROUND_ROBIN, RANDOM, MAGLEV.
	// +optional
	LoadbalancingPolicy *string `json:"loadbalancingPolicy,omitempty"`

	// HealthCheck configures the health check.
	// +optional
	HealthCheck *HealthCheck `json:"healthCheck,omitempty"`
}

// TargetGroupStatus defines the observed state of TargetGroup.
type TargetGroupStatus struct {
	ReconcileStatus `json:",inline"`

	// ResourceID is the Thalassa IaaS identity of the target group.
	// +optional
	ResourceID string `json:"resourceId,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="VPC",type=string,JSONPath=`.spec.vpcRef.name`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.targetPort`
// +kubebuilder:printcolumn:name="Resource ID",type=string,JSONPath=`.status.resourceId`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// TargetGroup is the Schema for the targetgroups API
type TargetGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TargetGroupSpec   `json:"spec"`
	Status TargetGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TargetGroupList contains a list of TargetGroup
type TargetGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TargetGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TargetGroup{}, &TargetGroupList{})
}
