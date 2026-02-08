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
// +kubebuilder:resource:shortName=hpmap,scope=Namespaced
// +kubebuilder:printcolumn:name="Map Name",type=string,JSONPath=`.spec.mapName`
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.spec.path`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyMapFile contains a rendered HAProxy map file.
//
// This is a read-only resource automatically created and updated by the controller
// to expose map files generated from templates. Each HAProxyMapFile is owned by
// a HAProxyConfig resource.
type HAProxyMapFile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyMapFileSpec   `json:"spec,omitempty"`
	Status HAProxyMapFileStatus `json:"status,omitempty"`
}

// HAProxyMapFileSpec contains the map file content.
type HAProxyMapFileSpec struct {
	// MapName is the logical name of the map file.
	//
	// This corresponds to the key in HAProxyTemplateConfig.spec.maps.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	MapName string `json:"mapName"`

	// Path is the file system path where this map file is stored.
	//
	// Example: /etc/haproxy/maps/path-prefix.map
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Entries is the map file content in HAProxy map format.
	//
	// Each line typically contains a key-value pair separated by whitespace.
	// Example:
	//   /api backend-api
	//   /web backend-web
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Entries string `json:"entries"`

	// Checksum is the SHA-256 hash of the map file entries.
	//
	// Used to detect changes and verify consistency across pods.
	// Format: sha256:<hex-digest>
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Checksum string `json:"checksum"`

	// Compressed indicates the entries are zstd+base64 encoded.
	//
	// When true, consumers must decompress before use.
	// +optional
	Compressed bool `json:"compressed,omitempty"`
}

// HAProxyMapFileStatus tracks deployment state to HAProxy pods.
type HAProxyMapFileStatus struct {
	// DeployedToPods tracks which HAProxy pods currently have this map file.
	//
	// Pods are automatically added when the map file is applied and removed when
	// the pod terminates.
	// +optional
	DeployedToPods []PodDeploymentStatus `json:"deployedToPods,omitempty"`

	// ObservedGeneration reflects the generation of the spec that was most recently processed.
	//
	// This is used to track whether status is up-to-date with latest spec changes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	//
	// Standard conditions include:
	// - "Synced": Map file has been successfully applied to all target pods
	// - "Ready": Resource is ready for use
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// HAProxyMapFileList contains a list of HAProxyMapFile.
type HAProxyMapFileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HAProxyMapFile `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hpgf,scope=Namespaced
// +kubebuilder:printcolumn:name="File Name",type=string,JSONPath=`.spec.fileName`
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.spec.path`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyGeneralFile contains a rendered HAProxy general file (error pages, custom responses, etc.).
//
// This is a read-only resource automatically created and updated by the controller
// to expose general files generated from templates. Each HAProxyGeneralFile is owned by
// a HAProxyCfg resource.
type HAProxyGeneralFile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyGeneralFileSpec   `json:"spec,omitempty"`
	Status HAProxyGeneralFileStatus `json:"status,omitempty"`
}

// HAProxyGeneralFileSpec contains the general file content.
type HAProxyGeneralFileSpec struct {
	// FileName is the logical name of the file.
	//
	// This corresponds to the key in HAProxyTemplateConfig.spec.files.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	FileName string `json:"fileName"`

	// Path is the file system path where this file is stored.
	//
	// Example: /etc/haproxy/general/503.http
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Content is the file content.
	//
	// This can be any content the HAProxy configuration references,
	// such as custom error pages or response files.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Content string `json:"content"`

	// Checksum is the SHA-256 hash of the file content.
	//
	// Used to detect changes and verify consistency across pods.
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

// HAProxyGeneralFileStatus tracks deployment state to HAProxy pods.
type HAProxyGeneralFileStatus struct {
	// DeployedToPods tracks which HAProxy pods currently have this file.
	//
	// Pods are automatically added when the file is applied and removed when
	// the pod terminates.
	// +optional
	DeployedToPods []PodDeploymentStatus `json:"deployedToPods,omitempty"`

	// ObservedGeneration reflects the generation of the spec that was most recently processed.
	//
	// This is used to track whether status is up-to-date with latest spec changes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	//
	// Standard conditions include:
	// - "Synced": File has been successfully applied to all target pods
	// - "Ready": Resource is ready for use
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// HAProxyGeneralFileList contains a list of HAProxyGeneralFile.
type HAProxyGeneralFileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HAProxyGeneralFile `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=hpcl,scope=Namespaced
// +kubebuilder:printcolumn:name="List Name",type=string,JSONPath=`.spec.listName`
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.spec.path`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyCRTListFile contains a rendered HAProxy crt-list file.
//
// CRT-list files contain SSL certificate references with per-certificate options
// and SNI filters. This is a read-only resource automatically created and updated
// by the controller. Each HAProxyCRTListFile is owned by a HAProxyCfg resource.
type HAProxyCRTListFile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyCRTListFileSpec   `json:"spec,omitempty"`
	Status HAProxyCRTListFileStatus `json:"status,omitempty"`
}

// HAProxyCRTListFileSpec contains the crt-list file content.
type HAProxyCRTListFileSpec struct {
	// ListName is the logical name of the crt-list file.
	//
	// Example: "frontend-certs" or "backend-client-certs"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ListName string `json:"listName"`

	// Path is the file system path where this crt-list file is stored.
	//
	// Example: /etc/haproxy/crt-lists/frontend.crtlist
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Entries is the crt-list file content.
	//
	// Each line contains a certificate path with optional SSL options and SNI filters.
	// Format: <cert-path> [ssl-options] [sni-filter]
	// Example:
	//   /etc/haproxy/ssl/example.com.pem [ocsp-update on] example.com
	//   /etc/haproxy/ssl/wildcard.pem *.example.org
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Entries string `json:"entries"`

	// Checksum is the SHA-256 hash of the crt-list entries.
	//
	// Used to detect changes and verify consistency across pods.
	// Format: sha256:<hex-digest>
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Checksum string `json:"checksum"`

	// Compressed indicates the entries are zstd+base64 encoded.
	//
	// When true, consumers must decompress before use.
	// +optional
	Compressed bool `json:"compressed,omitempty"`
}

// HAProxyCRTListFileStatus tracks deployment state to HAProxy pods.
type HAProxyCRTListFileStatus struct {
	// DeployedToPods tracks which HAProxy pods currently have this crt-list file.
	//
	// Pods are automatically added when the crt-list file is applied and removed when
	// the pod terminates.
	// +optional
	DeployedToPods []PodDeploymentStatus `json:"deployedToPods,omitempty"`

	// ObservedGeneration reflects the generation of the spec that was most recently processed.
	//
	// This is used to track whether status is up-to-date with latest spec changes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the resource's state.
	//
	// Standard conditions include:
	// - "Synced": CRT-list file has been successfully applied to all target pods
	// - "Ready": Resource is ready for use
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// HAProxyCRTListFileList contains a list of HAProxyCRTListFile.
type HAProxyCRTListFileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HAProxyCRTListFile `json:"items"`
}
