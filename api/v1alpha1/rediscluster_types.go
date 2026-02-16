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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types reported in RedisCluster.status.conditions.
const (
	// ConditionAvailable is True when all desired replicas are ready.
	ConditionAvailable = "Available"
	// ConditionProgressing is True while the cluster is being created, scaled or upgraded.
	ConditionProgressing = "Progressing"
	// ConditionDegraded is True when the operator cannot reconcile the cluster.
	ConditionDegraded = "Degraded"
)

// RedisClusterSpec defines the desired state of RedisCluster.
type RedisClusterSpec struct {
	// replicas is the number of Redis instances.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=7
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// version is the Redis image tag, e.g. "7.2".
	// +kubebuilder:default="7.2"
	// +kubebuilder:validation:MinLength=1
	// +optional
	Version string `json:"version,omitempty"`

	// resources are the CPU/memory requests and limits of each Redis container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// storage configures the persistent volume of each Redis instance.
	// +kubebuilder:default={}
	// +optional
	Storage StorageSpec `json:"storage,omitzero"`
}

// StorageSpec configures persistent storage.
type StorageSpec struct {
	// size is the capacity of each persistent volume.
	// +kubebuilder:default="1Gi"
	// +optional
	Size resource.Quantity `json:"size,omitzero"`
}

// RedisClusterStatus defines the observed state of RedisCluster.
type RedisClusterStatus struct {
	// readyReplicas is the number of Redis pods that are ready.
	// It is always serialised, even when zero, so `kubectl get` shows 0
	// instead of a blank Ready column.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas"`

	// observedGeneration is the .metadata.generation last processed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// currentVersion is the Redis version all replicas are running.
	// It differs from spec.version while a rolling upgrade is in progress.
	// +optional
	CurrentVersion string `json:"currentVersion,omitempty"`

	// conditions represent the current state of the RedisCluster resource.
	// Supported types are Available, Progressing and Degraded.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RedisCluster is the Schema for the redisclusters API.
type RedisCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of RedisCluster.
	// +required
	Spec RedisClusterSpec `json:"spec"`

	// status defines the observed state of RedisCluster.
	// +optional
	Status RedisClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// RedisClusterList contains a list of RedisCluster.
type RedisClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []RedisCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RedisCluster{}, &RedisClusterList{})
		return nil
	})
}
