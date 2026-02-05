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

// ReconcileStatus holds reconcile metadata and external resource status.
// Embed in resource status types to expose last reconcile time, errors, and provider status.
type ReconcileStatus struct {
	// ObservedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastReconcileTime is when the controller last reconciled this resource.
	// +optional
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`

	// LastReconcileError is the last error message from reconcile, if any. Cleared on success.
	// +optional
	LastReconcileError string `json:"lastReconcileError,omitempty"`

	// ResourceStatus is the status of the resource in Thalassa (e.g. ready, pending).
	// +optional
	ResourceStatus string `json:"resourceStatus,omitempty"`
}

// SecurityGroupRef references a SecurityGroup by Thalassa identity or by the Kubernetes SecurityGroup resource.
type SecurityGroupRef struct {
	// Name is the name of the SecurityGroup resource in Kubernetes. Use when referencing a SecurityGroup CR.
	// +optional
	Name string `json:"name,omitempty"`

	// Namespace is the namespace of the SecurityGroup resource. Defaults to the same namespace as the referencing resource.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Identity is the Thalassa security group identity. If set, the controller uses this directly.
	// +optional
	Identity string `json:"identity,omitempty"`
}

// ResourceMetadata allows optional name and label overrides for the created/updated resource in Thalassa.
// When set, these values are applied to the resource in Thalassa instead of defaulting from the Kubernetes object.
type ResourceMetadata struct {
	// Name overrides the name of the resource in Thalassa. If not set, the Kubernetes object name is used.
	// +optional
	Name *string `json:"name,omitempty"`

	// Labels are applied to the resource in Thalassa. If not set, no labels are sent (API default applies).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}
