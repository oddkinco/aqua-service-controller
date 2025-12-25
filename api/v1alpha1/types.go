// Package v1alpha1 contains API Schema definitions for the migration v1alpha1 API group
// +kubebuilder:object:generate=true
// +groupName=migration.aqua.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MigrationPhase represents the current phase of the migration
type MigrationPhase string

const (
	// PhasePending indicates the migration has been created but not started
	PhasePending MigrationPhase = "Pending"
	// PhasePreFlightChecks indicates pre-flight validation is in progress
	PhasePreFlightChecks MigrationPhase = "PreFlightChecks"
	// PhaseFreezingSource indicates the source cluster is being prepared
	PhaseFreezingSource MigrationPhase = "FreezingSource"
	// PhaseMigratingPods indicates pods are being migrated one by one
	PhaseMigratingPods MigrationPhase = "MigratingPods"
	// PhaseFinalizing indicates cleanup and finalization is in progress
	PhaseFinalizing MigrationPhase = "Finalizing"
	// PhaseCompleted indicates the migration completed successfully
	PhaseCompleted MigrationPhase = "Completed"
	// PhaseFailed indicates the migration has failed
	PhaseFailed MigrationPhase = "Failed"
)

// ContextRef references a kubeconfig stored in a Secret
type ContextRef struct {
	// KubeConfigSecret is the name of the Secret containing the kubeconfig
	// The secret must have a key named "kubeconfig"
	KubeConfigSecret string `json:"kubeConfigSecret"`

	// KubeConfigKey is the key in the secret containing the kubeconfig (default: "kubeconfig")
	// +optional
	KubeConfigKey string `json:"kubeConfigKey,omitempty"`
}

// StatefulSetMigrationSpec defines the desired state of StatefulSetMigration
type StatefulSetMigrationSpec struct {
	// MigrationID is a unique identifier for this migration
	MigrationID string `json:"migrationId"`

	// SourceCluster contains the reference to the source cluster kubeconfig
	SourceCluster ContextRef `json:"sourceCluster"`

	// SourceNamespace is the namespace of the StatefulSet in the source cluster
	SourceNamespace string `json:"sourceNamespace"`

	// StatefulSetName is the name of the StatefulSet to migrate
	StatefulSetName string `json:"statefulSetName"`

	// DestCluster contains the reference to the destination cluster kubeconfig
	DestCluster ContextRef `json:"destCluster"`

	// DestNamespace is the namespace to migrate to in the destination cluster
	DestNamespace string `json:"destNamespace"`

	// Force ignores non-critical pre-flight warnings
	// +optional
	Force bool `json:"force,omitempty"`

	// StorageClassMapping maps source StorageClass names to destination StorageClass names
	// If not specified, the same StorageClass name will be used
	// +optional
	StorageClassMapping map[string]string `json:"storageClassMapping,omitempty"`

	// VolumeDetachTimeout is the maximum time to wait for a volume to detach (default: 5m)
	// +optional
	VolumeDetachTimeout *metav1.Duration `json:"volumeDetachTimeout,omitempty"`

	// PodReadyTimeout is the maximum time to wait for a pod to become ready (default: 10m)
	// +optional
	PodReadyTimeout *metav1.Duration `json:"podReadyTimeout,omitempty"`
}

// MigratedPodInfo contains information about a migrated pod
type MigratedPodInfo struct {
	// Index is the StatefulSet pod index
	Index int `json:"index"`

	// PodName is the name of the pod
	PodName string `json:"podName"`

	// VolumeID is the EBS volume ID
	VolumeID string `json:"volumeId"`

	// MigratedAt is when this pod was migrated
	MigratedAt metav1.Time `json:"migratedAt"`
}

// StatefulSetMigrationStatus defines the observed state of StatefulSetMigration
type StatefulSetMigrationStatus struct {
	// Phase is the current phase of the migration
	Phase MigrationPhase `json:"phase,omitempty"`

	// CurrentIndex is the index of the pod currently being migrated (0-based)
	CurrentIndex int `json:"currentIndex,omitempty"`

	// TotalReplicas is the total number of replicas to migrate
	TotalReplicas int `json:"totalReplicas,omitempty"`

	// MigratedPods contains information about successfully migrated pods
	// +optional
	MigratedPods []MigratedPodInfo `json:"migratedPods,omitempty"`

	// Conditions represent the latest available observations of the migration's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastError contains the last error message if Phase is Failed
	// +optional
	LastError string `json:"lastError,omitempty"`

	// StartTime is when the migration started
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the migration completed (successfully or failed)
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// SourceStatefulSetUID is the UID of the source StatefulSet (for verification)
	// +optional
	SourceStatefulSetUID string `json:"sourceStatefulSetUID,omitempty"`

	// PreservedPVs contains the list of PV names that have been set to Retain
	// +optional
	PreservedPVs []string `json:"preservedPVs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Progress",type=string,JSONPath=`.status.currentIndex`
// +kubebuilder:printcolumn:name="Total",type=string,JSONPath=`.status.totalReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// StatefulSetMigration is the Schema for the statefulsetmigrations API
type StatefulSetMigration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   StatefulSetMigrationSpec   `json:"spec,omitempty"`
	Status StatefulSetMigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// StatefulSetMigrationList contains a list of StatefulSetMigration
type StatefulSetMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StatefulSetMigration `json:"items"`
}
