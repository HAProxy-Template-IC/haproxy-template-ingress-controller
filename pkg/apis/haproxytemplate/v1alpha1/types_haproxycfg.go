// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hpcfg,scope=Namespaced
// +kubebuilder:printcolumn:name="Checksum",type=string,JSONPath=`.spec.checksum`
// +kubebuilder:printcolumn:name="Size",type=integer,JSONPath=`.status.metadata.totalSize`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyCfg contains the rendered HAProxy configuration for a specific
// HAProxyTemplateConfig.
//
// This is a read-only resource automatically created and updated by the controller
// to expose the actual runtime configuration applied to HAProxy pods.
type HAProxyCfg struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyCfgSpec   `json:"spec,omitempty"`
	Status HAProxyCfgStatus `json:"status,omitempty"`
}

// HAProxyCfgSpec contains the rendered configuration content.
type HAProxyCfgSpec struct {
	// Path is the file system path where this configuration is stored.
	//
	// Default: /etc/haproxy/haproxy.cfg
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Content is the rendered HAProxy configuration file content.
	//
	// This is the actual haproxy.cfg content that was validated and deployed
	// to HAProxy pods.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Content string `json:"content"`

	// Checksum is the SHA-256 hash of the original (uncompressed) configuration content.
	//
	// Used to detect configuration changes and verify consistency across pods.
	// Format: sha256:<hex-digest>
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Checksum string `json:"checksum"`

	// Compressed indicates the content is zstd+base64 encoded.
	//
	// When true, consumers must decompress before use.
	// +optional
	Compressed bool `json:"compressed,omitempty"`
}

// HAProxyCfgStatus tracks deployment state and auxiliary files.
type HAProxyCfgStatus struct {
	// DeployedToPods tracks which HAProxy pods currently have this configuration.
	//
	// Pods are automatically added when configuration is applied and removed when
	// the pod terminates.
	// +optional
	DeployedToPods []PodDeploymentStatus `json:"deployedToPods,omitempty"`

	// AuxiliaryFiles references the associated map files and certificates.
	// +optional
	AuxiliaryFiles *AuxiliaryFileReferences `json:"auxiliaryFiles,omitempty"`

	// Metadata contains information about the configuration rendering and validation.
	// +optional
	Metadata *ConfigMetadata `json:"metadata,omitempty"`

	// ValidationError contains the error message if this configuration failed validation.
	//
	// Only populated for HAProxyCfg resources published with the -invalid suffix.
	// When present, this configuration was not deployed to HAProxy instances.
	// +optional
	ValidationError string `json:"validationError,omitempty"`

	// ObservedGeneration reflects the generation of the spec that was most recently processed.
	//
	// This is used to track whether status is up-to-date with latest spec changes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	//
	// Standard conditions include:
	// - "Synced": Configuration has been successfully applied to all target pods
	// - "Ready": Resource is ready for use
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// PodDeploymentStatus tracks deployment to a specific pod.
type PodDeploymentStatus struct {
	// PodName is the name of the HAProxy pod.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	PodName string `json:"podName"`

	// DeployedAt is the timestamp when configuration was last successfully
	// synchronized to this pod.
	//
	// Updated when actual operations are performed (TotalOperations > 0) or when
	// drift prevention verifies configuration matches expected state.
	// +kubebuilder:validation:Required
	DeployedAt metav1.Time `json:"deployedAt"`

	// Checksum of the configuration deployed to this pod.
	// +optional
	Checksum string `json:"checksum,omitempty"`

	// LastReloadAt is the timestamp when HAProxy was last reloaded for this pod.
	//
	// HAProxy reloads occur when structural configuration changes are made via
	// the transaction API (status 202). Runtime-only changes do not trigger reloads.
	// +optional
	LastReloadAt *metav1.Time `json:"lastReloadAt,omitempty"`

	// LastReloadID is the reload identifier from the most recent HAProxy reload.
	//
	// This corresponds to the Reload-ID header returned by the HAProxy dataplane
	// API when a reload is triggered.
	// +optional
	LastReloadID string `json:"lastReloadID,omitempty"`

	// SyncDuration is the duration of the most recent sync operation.
	//
	// This tracks how long it took to apply configuration changes to this pod,
	// useful for performance monitoring and troubleshooting.
	// +optional
	SyncDuration *metav1.Duration `json:"syncDuration,omitempty"`

	// VersionConflictRetries is the number of version conflict retries during the last sync.
	//
	// HAProxy's dataplane API uses optimistic concurrency control. This counter
	// tracks retries due to version conflicts, indicating contention or race conditions.
	// +optional
	// +kubebuilder:validation:Minimum=0
	VersionConflictRetries int `json:"versionConflictRetries,omitempty"`

	// FallbackUsed indicates whether the last sync used fallback mode.
	//
	// When true, indicates that incremental sync failed and a full raw configuration
	// push was used instead. Frequent fallbacks may indicate sync logic issues.
	// +optional
	FallbackUsed bool `json:"fallbackUsed,omitempty"`

	// LastOperationSummary provides a breakdown of operations performed in the last sync.
	//
	// This shows the number of backends, servers, and other resources that were
	// added, removed, or modified during synchronization.
	// +optional
	LastOperationSummary *OperationSummary `json:"lastOperationSummary,omitempty"`

	// LastError contains the error message from the most recent failed sync attempt.
	//
	// This field is cleared when a sync succeeds. Combined with ConsecutiveErrors,
	// this helps identify persistent vs transient issues.
	// +optional
	LastError string `json:"lastError,omitempty"`

	// ConsecutiveErrors is the count of consecutive sync failures.
	//
	// This counter increments on each failure and resets to 0 on success.
	// High values indicate persistent problems requiring investigation.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ConsecutiveErrors int `json:"consecutiveErrors,omitempty"`

	// LastErrorAt is the timestamp of the most recent sync error.
	//
	// Used to determine how long a pod has been in an error state.
	// +optional
	LastErrorAt *metav1.Time `json:"lastErrorAt,omitempty"`
}

// OperationSummary provides statistics about sync operations.
type OperationSummary struct {
	// TotalAPIOperations is the total count of HAProxy Dataplane API operations
	// across ALL configuration sections (includes globals, defaults, acls, binds, etc.).
	//
	// This may be higher than the sum of specific operations shown below, as it includes
	// operations on sections not individually tracked in this summary.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TotalAPIOperations int `json:"totalAPIOperations,omitempty"`

	// BackendsAdded is the number of backends added.
	// +optional
	// +kubebuilder:validation:Minimum=0
	BackendsAdded int `json:"backendsAdded,omitempty"`

	// BackendsRemoved is the number of backends removed.
	// +optional
	// +kubebuilder:validation:Minimum=0
	BackendsRemoved int `json:"backendsRemoved,omitempty"`

	// BackendsModified is the number of backends modified.
	// +optional
	// +kubebuilder:validation:Minimum=0
	BackendsModified int `json:"backendsModified,omitempty"`

	// ServersAdded is the number of servers added across all backends.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ServersAdded int `json:"serversAdded,omitempty"`

	// ServersRemoved is the number of servers removed across all backends.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ServersRemoved int `json:"serversRemoved,omitempty"`

	// ServersModified is the number of servers modified across all backends.
	// +optional
	// +kubebuilder:validation:Minimum=0
	ServersModified int `json:"serversModified,omitempty"`

	// FrontendsAdded is the number of frontends added.
	// +optional
	// +kubebuilder:validation:Minimum=0
	FrontendsAdded int `json:"frontendsAdded,omitempty"`

	// FrontendsRemoved is the number of frontends removed.
	// +optional
	// +kubebuilder:validation:Minimum=0
	FrontendsRemoved int `json:"frontendsRemoved,omitempty"`

	// FrontendsModified is the number of frontends modified.
	// +optional
	// +kubebuilder:validation:Minimum=0
	FrontendsModified int `json:"frontendsModified,omitempty"`
}

// AuxiliaryFileReferences references the associated map files, certificates, general files, and crt-lists.
type AuxiliaryFileReferences struct {
	// MapFiles lists the HAProxyMapFile resources associated with this config.
	// +optional
	MapFiles []ResourceReference `json:"mapFiles,omitempty"`

	// SSLCertificates lists the Secret resources containing SSL certificates.
	// +optional
	SSLCertificates []ResourceReference `json:"sslCertificates,omitempty"`

	// GeneralFiles lists the HAProxyGeneralFile resources associated with this config.
	// +optional
	GeneralFiles []ResourceReference `json:"generalFiles,omitempty"`

	// CRTListFiles lists the HAProxyCRTListFile resources associated with this config.
	// +optional
	CRTListFiles []ResourceReference `json:"crtListFiles,omitempty"`
}

// ResourceReference identifies a related Kubernetes resource.
type ResourceReference struct {
	// Kind is the resource type (e.g., HAProxyMapFile, Secret).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// Name is the resource name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace is the resource namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ConfigMetadata contains information about the configuration rendering and validation.
type ConfigMetadata struct {
	// TotalSize is the total size of all configuration data in bytes.
	//
	// Includes main config, map files, and certificates.
	// +kubebuilder:validation:Minimum=0
	// +optional
	TotalSize int64 `json:"totalSize,omitempty"`

	// ContentSize is the size of the main configuration content in bytes.
	// +kubebuilder:validation:Minimum=0
	// +optional
	ContentSize int64 `json:"contentSize,omitempty"`

	// RenderedAt is the timestamp when the configuration was rendered.
	// +optional
	RenderedAt *metav1.Time `json:"renderedAt,omitempty"`

	// ValidatedAt is the timestamp when the configuration was successfully validated.
	// +optional
	ValidatedAt *metav1.Time `json:"validatedAt,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// HAProxyCfgList contains a list of HAProxyCfg.
type HAProxyCfgList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HAProxyCfg `json:"items"`
}
