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

// Package v1alpha1 contains API Schema definitions for the haproxytemplate v1alpha1 API group.
//
// This package defines the HAProxyTemplateConfig custom resource, which replaces
// the previous ConfigMap-based configuration approach. The CRD provides:
//   - Type-safe configuration with validation
//   - Embedded validation tests for templates
//   - Kubernetes-native admission webhook support
//
// +k8s:deepcopy-gen=package
// +groupName=haproxy-template-ic.github.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=htplcfg;haptpl,scope=Namespaced
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.validationStatus`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyTemplateConfig defines the configuration for the HAProxy Template Ingress Controller.
//
// This custom resource replaces the previous ConfigMap-based configuration approach,
// providing better validation, type safety, and support for embedded validation tests.
type HAProxyTemplateConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyTemplateConfigSpec   `json:"spec,omitempty"`
	Status HAProxyTemplateConfigStatus `json:"status,omitempty"`
}

// HAProxyTemplateConfigSpec defines the desired state of HAProxyTemplateConfig.
type HAProxyTemplateConfigSpec struct {
	// CredentialsSecretRef references the Secret containing HAProxy Dataplane API credentials.
	//
	// The Secret must contain the following keys:
	//   - dataplane_username: Username for production HAProxy Dataplane API
	//   - dataplane_password: Password for production HAProxy Dataplane API
	//   - validation_username: Username for validation HAProxy instance
	//   - validation_password: Password for validation HAProxy instance
	//
	// If the namespace is omitted, it defaults to the same namespace as this config resource.
	// +kubebuilder:validation:Required
	CredentialsSecretRef SecretReference `json:"credentialsSecretRef"`

	// PodSelector identifies which HAProxy pods to configure.
	// +kubebuilder:validation:Required
	PodSelector PodSelector `json:"podSelector"`

	// Controller contains controller-level settings (ports, leader election, etc.).
	// +optional
	Controller ControllerConfig `json:"controller,omitempty"`

	// Logging configures logging behavior.
	// +optional
	Logging LoggingConfig `json:"logging,omitempty"`

	// Dataplane configures the Dataplane API for production HAProxy instances.
	// +optional
	Dataplane DataplaneConfig `json:"dataplane,omitempty"`

	// TemplatingSettings configures template rendering behavior and custom variables.
	// +optional
	TemplatingSettings TemplatingSettings `json:"templatingSettings,omitempty"`

	// WatchedResourcesIgnoreFields specifies JSONPath expressions for fields
	// to remove from all watched resources to reduce memory usage.
	//
	// Example: ["metadata.managedFields", "metadata.resourceVersion"]
	// +optional
	WatchedResourcesIgnoreFields []string `json:"watchedResourcesIgnoreFields,omitempty"`

	// WatchedResources maps resource type names to their watch configuration.
	//
	// Each key is a user-defined name for the resource type (e.g., "ingresses", "services").
	// This name is used in templates to access the resources.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	WatchedResources map[string]WatchedResource `json:"watchedResources"`

	// TemplateSnippets maps snippet names to reusable template fragments.
	//
	// Snippets can be included in other templates using {% include "name" %}.
	// +optional
	TemplateSnippets map[string]TemplateSnippet `json:"templateSnippets,omitempty"`

	// Maps maps map file names to their template definitions.
	//
	// These generate HAProxy map files for backend routing and other features.
	// +optional
	Maps map[string]MapFile `json:"maps,omitempty"`

	// Files maps file names to their template definitions.
	//
	// These generate auxiliary files like custom error pages.
	// +optional
	Files map[string]GeneralFile `json:"files,omitempty"`

	// SSLCertificates maps certificate names to their template definitions.
	//
	// These generate SSL certificate files for HAProxy.
	// +optional
	SSLCertificates map[string]SSLCertificate `json:"sslCertificates,omitempty"`

	// HAProxyConfig contains the main HAProxy configuration template.
	// +kubebuilder:validation:Required
	HAProxyConfig HAProxyConfig `json:"haproxyConfig"`

	// ValidationTests contains embedded validation test definitions.
	//
	// The map key is the test name, which must be unique.
	//
	// These tests are executed:
	//   - During admission webhook validation (before resource is saved)
	//   - Via the "controller validate" CLI command (pre-apply validation)
	//
	// Tests ensure templates generate valid HAProxy configurations before deployment.
	// +optional
	ValidationTests map[string]ValidationTest `json:"validationTests,omitempty"`
}

// SecretReference references a Secret by name and optional namespace.
type SecretReference struct {
	// Name is the name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace is the namespace of the Secret.
	//
	// If empty, defaults to the same namespace as the HAProxyTemplateConfig.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// PodSelector identifies which HAProxy pods to configure.
type PodSelector struct {
	// MatchLabels are the labels to match HAProxy pods.
	//
	// Example:
	//   app: haproxy
	//   component: loadbalancer
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	MatchLabels map[string]string `json:"matchLabels"`
}

// ControllerConfig contains controller-level configuration.
type ControllerConfig struct {
	// HealthzPort is the port for health check endpoints.
	//
	// Default: 8080
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	HealthzPort int `json:"healthzPort,omitempty"`

	// MetricsPort is the port for Prometheus metrics.
	//
	// Default: 9090
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	MetricsPort int `json:"metricsPort,omitempty"`

	// LeaderElection configures leader election for high availability.
	// +optional
	LeaderElection LeaderElectionConfig `json:"leaderElection,omitempty"`
}

// LeaderElectionConfig configures leader election for running multiple replicas.
type LeaderElectionConfig struct {
	// Enabled determines whether leader election is active.
	//
	// If false, the controller assumes it is the sole instance (single-replica mode).
	// Default: true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// LeaseName is the name of the Lease resource used for coordination.
	//
	// Default: haproxy-template-ic-leader
	// +kubebuilder:validation:MinLength=1
	// +optional
	LeaseName string `json:"leaseName,omitempty"`

	// LeaseDuration is the duration that non-leader candidates will wait
	// to force acquire leadership (measured against time of last observed ack).
	//
	// Format: Go duration string (e.g., "60s", "1m")
	// Default: 60s
	// Minimum: 15s
	// +optional
	LeaseDuration string `json:"leaseDuration,omitempty"`

	// RenewDeadline is the duration that the acting leader will retry
	// refreshing leadership before giving up.
	//
	// Format: Go duration string (e.g., "15s")
	// Default: 15s
	// Must be less than LeaseDuration
	// +optional
	RenewDeadline string `json:"renewDeadline,omitempty"`

	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions.
	//
	// Format: Go duration string (e.g., "5s")
	// Default: 5s
	// Must be less than RenewDeadline
	// +optional
	RetryPeriod string `json:"retryPeriod,omitempty"`
}

// LoggingConfig configures logging behavior.
type LoggingConfig struct {
	// Verbose controls log level.
	//
	// Values:
	//   0: WARNING
	//   1: INFO
	//   2: DEBUG
	//
	// Default: 1
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2
	// +optional
	Verbose int `json:"verbose,omitempty"`
}

// DataplaneConfig configures the Dataplane API for production HAProxy instances.
type DataplaneConfig struct {
	// Port is the Dataplane API port for production HAProxy pods.
	//
	// Default: 5555
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int `json:"port,omitempty"`

	// MinDeploymentInterval enforces minimum time between consecutive deployments.
	//
	// This prevents rapid-fire deployments from hammering HAProxy instances.
	// Format: Go duration string (e.g., "2s", "500ms")
	// Default: 2s
	// +optional
	MinDeploymentInterval string `json:"minDeploymentInterval,omitempty"`

	// DriftPreventionInterval triggers periodic deployments to prevent configuration drift.
	//
	// A deployment is automatically triggered if no deployment has occurred within this interval.
	// This detects and corrects drift caused by external Dataplane API clients.
	// Format: Go duration string (e.g., "60s", "5m")
	// Default: 60s
	// +optional
	DriftPreventionInterval string `json:"driftPreventionInterval,omitempty"`

	// MapsDir is the directory for HAProxy map files.
	//
	// Used for both validation and deployment.
	// Default: /etc/haproxy/maps
	// +optional
	MapsDir string `json:"mapsDir,omitempty"`

	// SSLCertsDir is the directory for SSL certificates.
	//
	// Used for both validation and deployment.
	// Default: /etc/haproxy/ssl
	// +optional
	SSLCertsDir string `json:"sslCertsDir,omitempty"`

	// GeneralStorageDir is the directory for general files (error pages, etc.).
	//
	// Used for both validation and deployment.
	// Default: /etc/haproxy/general
	// +optional
	GeneralStorageDir string `json:"generalStorageDir,omitempty"`

	// ConfigFile is the path to the main HAProxy configuration file.
	//
	// Used for validation.
	// Default: /etc/haproxy/haproxy.cfg
	// +optional
	ConfigFile string `json:"configFile,omitempty"`
}

// TemplatingSettings configures template rendering behavior.
type TemplatingSettings struct {
	// ExtraContext provides custom variables that are passed to all templates.
	//
	// This allows users to add arbitrary data to the template context without
	// modifying controller code. Values can be any valid JSON type (string, number,
	// boolean, object, array).
	//
	// Example:
	//   extraContext:
	//     debug:
	//       enabled: true
	//     environment: production
	//     customValue: 42
	//
	// Templates can then reference these as: {{ debug.enabled }}, {{ environment }}, etc.
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	ExtraContext runtime.RawExtension `json:"extraContext,omitempty"`
}

// WatchedResource configures watching for a specific Kubernetes resource type.
type WatchedResource struct {
	// APIVersion is the Kubernetes API version (e.g., "networking.k8s.io/v1").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	APIVersion string `json:"apiVersion"`

	// Resources is the plural form of the Kubernetes resource type (e.g., "ingresses", "services").
	//
	// This is the name used in RBAC rules and API paths.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Resources string `json:"resources"`

	// EnableValidationWebhook enables admission webhook validation for this resource.
	//
	// When enabled, the controller will validate resources of this type before they're saved.
	// Default: false
	// +optional
	EnableValidationWebhook bool `json:"enableValidationWebhook,omitempty"`

	// IndexBy specifies JSONPath expressions for extracting index keys.
	//
	// Resources are indexed by these values for O(1) lookup in templates.
	//
	// Examples:
	//   - ["metadata.namespace", "metadata.name"]
	//   - ["metadata.labels['kubernetes.io/service-name']"]
	// +optional
	IndexBy []string `json:"indexBy,omitempty"`

	// LabelSelector filters resources by labels (server-side filtering).
	//
	// Example: "app=nginx,environment=production"
	// +optional
	LabelSelector string `json:"labelSelector,omitempty"`

	// FieldSelector filters resources by fields (server-side filtering).
	//
	// Example: "metadata.namespace=default"
	// Note: Not all fields support field selectors. Use label selectors when possible.
	// +optional
	FieldSelector string `json:"fieldSelector,omitempty"`

	// NamespaceSelector filters resources by namespace labels.
	//
	// Example: "environment=production"
	// If empty, watches resources in all namespaces (requires cluster-wide RBAC).
	// +optional
	NamespaceSelector string `json:"namespaceSelector,omitempty"`

	// Store specifies the storage backend for this resource type.
	//
	// Valid values:
	//   - "full": MemoryStore - keeps all resources in memory (faster, higher memory usage)
	//   - "on-demand": CachedStore - fetches resources on-demand with caching (slower, lower memory usage)
	//
	// Default: "full"
	//
	// Use "on-demand" for large resources accessed infrequently (e.g., Secrets).
	// Use "full" for frequently accessed resources (e.g., Ingress, Service, EndpointSlice).
	// +kubebuilder:validation:Enum=full;on-demand
	// +optional
	Store string `json:"store,omitempty"`
}

// TemplateSnippet defines a reusable template fragment.
type TemplateSnippet struct {
	// Template is the Gonja template content.
	//
	// Can be included in other templates using {% include "snippet_name" %}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`

	// Priority determines the rendering order when multiple snippets are included.
	//
	// Lower values are rendered first. Snippets with the same priority are sorted alphabetically by name.
	// Default: 500
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	// +optional
	Priority *int `json:"priority,omitempty"`
}

// PostProcessorConfig defines a post-processor to apply to rendered template output.
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (PostProcessorConfig)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function)
//
// The converter.go file transforms CRD types to internal config types used by the controller.
type PostProcessorConfig struct {
	// Type specifies the post-processor type (e.g., "regex_replace").
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// Params contains post-processor-specific parameters.
	//
	// For "regex_replace":
	//   - pattern: Regular expression pattern to match
	//   - replace: Replacement string
	// +kubebuilder:validation:Required
	Params map[string]string `json:"params"`
}

// MapFile defines a HAProxy map file generated from a template.
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (MapFile)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function - maps section)
type MapFile struct {
	// Template is the Gonja template for generating the map file content.
	//
	// The rendered output should be in HAProxy map file format (key-value pairs).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	//
	// Post-processors run in the order specified and can transform the rendered output.
	// +optional
	PostProcessing []PostProcessorConfig `json:"postProcessing,omitempty"`
}

// GeneralFile defines a general file generated from a template.
//
// The filename is derived from the map key in the configuration.
// The full path is constructed using the pathResolver.GetPath() method in templates:
//
//	Example: pathResolver.GetPath("503.http", "file") returns /etc/haproxy/general/503.http
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (GeneralFile)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function - files section)
type GeneralFile struct {
	// Template is the Gonja template for generating the file content.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	//
	// Post-processors run in the order specified and can transform the rendered output.
	// +optional
	PostProcessing []PostProcessorConfig `json:"postProcessing,omitempty"`
}

// SSLCertificate defines an SSL certificate generated from a template.
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (SSLCertificate)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function - sslCertificates section)
type SSLCertificate struct {
	// Template is the Gonja template for generating the certificate content.
	//
	// The rendered output should be in PEM format (certificate + private key).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	//
	// Post-processors run in the order specified and can transform the rendered output.
	// +optional
	PostProcessing []PostProcessorConfig `json:"postProcessing,omitempty"`
}

// HAProxyConfig defines the main HAProxy configuration.
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (HAProxyConfig)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function - haproxyConfig section)
type HAProxyConfig struct {
	// Template is the Gonja template for generating haproxy.cfg.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	//
	// Post-processors run in the order specified and can transform the rendered output.
	// Common use case: Normalize indentation with regex_replace.
	// +optional
	PostProcessing []PostProcessorConfig `json:"postProcessing,omitempty"`
}

// ValidationTest defines a validation test with fixtures and assertions.
//
// The test name is provided by the map key in ValidationTests.
type ValidationTest struct {
	// Description explains what this test validates.
	// +optional
	Description string `json:"description,omitempty"`

	// Fixtures defines the Kubernetes resources to use for this test.
	//
	// Keys are resource type names (matching WatchedResources keys).
	// Values are arrays of resources as raw JSON.
	//
	// Example:
	//   ingresses:
	//     - apiVersion: networking.k8s.io/v1
	//       kind: Ingress
	//       metadata:
	//         name: test-ingress
	// +kubebuilder:validation:Required
	// +kubebuilder:pruning:PreserveUnknownFields
	Fixtures map[string][]runtime.RawExtension `json:"fixtures"`

	// HTTPResources defines mock HTTP content for this test.
	//
	// These fixtures are used when templates call http.Fetch() to provide
	// pre-defined content without making actual HTTP requests.
	//
	// Example:
	//   httpResources:
	//     - url: http://blocklist.example.com/list.txt
	//       content: |
	//         blocked-value-1
	//         blocked-value-2
	// +optional
	HTTPResources []HTTPResourceFixture `json:"httpResources,omitempty"`

	// Assertions defines the validation checks to perform.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Assertions []ValidationAssertion `json:"assertions"`
}

// HTTPResourceFixture defines mock HTTP content for validation tests.
//
// When templates call http.Fetch() for the specified URL, the pre-defined
// content is returned instead of making an actual HTTP request.
type HTTPResourceFixture struct {
	// URL is the HTTP URL to mock.
	//
	// When http.Fetch() is called with this URL during test execution,
	// the Content field is returned instead of fetching from the network.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Content is the mock response body to return.
	//
	// This content is returned when http.Fetch() is called with the
	// matching URL during test execution.
	// +kubebuilder:validation:Required
	Content string `json:"content"`
}

// ValidationAssertion defines a single validation check.
type ValidationAssertion struct {
	// Type is the assertion type.
	//
	// Supported types:
	//   - haproxy_valid: Validates that generated HAProxy config is syntactically valid
	//   - contains: Checks if target contains pattern (regex)
	//   - not_contains: Checks if target does not contain pattern (regex)
	//   - equals: Checks if target equals expected value
	//   - jsonpath: Evaluates JSONPath expression against target
	//   - match_count: Counts how many times pattern matches in target (regex)
	//   - match_order: Validates that patterns appear in specified order
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=haproxy_valid;contains;not_contains;equals;jsonpath;match_count;match_order
	Type string `json:"type"`

	// Description explains what this assertion validates.
	// +optional
	Description string `json:"description,omitempty"`

	// Target specifies what to validate.
	//
	// Format depends on assertion type:
	//   - haproxy_valid: not used
	//   - contains/not_contains/equals: "haproxy_config", "maps.<name>", "files.<name>", "sslCertificates.<name>"
	//   - jsonpath: the resource to query
	// +optional
	Target string `json:"target,omitempty"`

	// Pattern is the regex pattern for contains/not_contains assertions.
	// +optional
	Pattern string `json:"pattern,omitempty"`

	// Expected is the expected value for equals assertions.
	// +optional
	Expected string `json:"expected,omitempty"`

	// JSONPath is the JSONPath expression for jsonpath assertions.
	// +optional
	JSONPath string `json:"jsonpath,omitempty"`

	// Patterns is a list of regex patterns for match_order assertions.
	// The patterns must appear in the target in the order specified.
	// +optional
	Patterns []string `json:"patterns,omitempty"`
}

// HAProxyTemplateConfigStatus defines the observed state of HAProxyTemplateConfig.
type HAProxyTemplateConfigStatus struct {
	// ObservedGeneration reflects the generation most recently observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastValidated is the timestamp of the last successful validation.
	// +optional
	LastValidated *metav1.Time `json:"lastValidated,omitempty"`

	// ValidationStatus indicates the overall validation status.
	// +kubebuilder:validation:Enum=Valid;Invalid;Unknown
	// +optional
	ValidationStatus string `json:"validationStatus,omitempty"`

	// ValidationMessage contains human-readable validation details.
	// +optional
	ValidationMessage string `json:"validationMessage,omitempty"`

	// Conditions represent the latest available observations of the config's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true

// HAProxyTemplateConfigList contains a list of HAProxyTemplateConfig.
type HAProxyTemplateConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HAProxyTemplateConfig `json:"items"`
}

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

	// Checksum is the SHA-256 hash of the configuration content.
	//
	// Used to detect configuration changes and verify consistency across pods.
	// Format: sha256:<hex-digest>
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Checksum string `json:"checksum"`
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

	// DeployedAt is the timestamp when configuration was last changed on this pod.
	//
	// This is only updated when actual operations are performed (TotalOperations > 0).
	// To see when config was last verified (including no-op checks), use LastCheckedAt.
	// +kubebuilder:validation:Required
	DeployedAt metav1.Time `json:"deployedAt"`

	// Checksum of the configuration deployed to this pod.
	// +optional
	Checksum string `json:"checksum,omitempty"`

	// LastCheckedAt is the timestamp of the last successful sync operation.
	//
	// Updated on every successful sync (reconciliation or drift-prevention),
	// regardless of whether operations were performed. Use this to verify
	// when the config was last checked against HAProxy's current state.
	// +optional
	LastCheckedAt *metav1.Time `json:"lastCheckedAt,omitempty"`

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

// AuxiliaryFileReferences references the associated map files and certificates.
type AuxiliaryFileReferences struct {
	// MapFiles lists the HAProxyMapFile resources associated with this config.
	// +optional
	MapFiles []ResourceReference `json:"mapFiles,omitempty"`

	// SSLCertificates lists the Secret resources containing SSL certificates.
	// +optional
	SSLCertificates []ResourceReference `json:"sslCertificates,omitempty"`
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
