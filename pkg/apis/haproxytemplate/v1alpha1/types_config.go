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
	//   - dataplane_username: Username for HAProxy Dataplane API
	//   - dataplane_password: Password for HAProxy Dataplane API
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
	// Snippets can be included in other templates using {{ render "name" }}.
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
	// LeaderElection configures leader election for high availability.
	// +optional
	LeaderElection LeaderElectionConfig `json:"leaderElection,omitempty"`

	// ConfigPublishing configures how rendered configs are stored in CRDs.
	// +optional
	ConfigPublishing ConfigPublishingConfig `json:"configPublishing,omitempty"`
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
	// Default: haptic-leader
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

// ConfigPublishingConfig configures how rendered configs are stored in CRDs.
type ConfigPublishingConfig struct {
	// CompressionThreshold is the minimum size in bytes at which configs are compressed.
	//
	// Configs smaller than this threshold are stored uncompressed.
	// Compression uses zstd+base64 encoding.
	//
	// Default: 1048576 (1 MiB)
	// Set to 0 to disable compression.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1048576
	// +optional
	CompressionThreshold int64 `json:"compressionThreshold,omitempty"`
}

// LoggingConfig configures logging behavior.
type LoggingConfig struct {
	// Level controls the log level.
	//
	// Values: TRACE, DEBUG, INFO, WARN, ERROR (case-insensitive)
	//
	// If not set, the LOG_LEVEL environment variable is used.
	// If neither is set, defaults to INFO.
	// +kubebuilder:validation:Enum=TRACE;DEBUG;INFO;WARN;ERROR;trace;debug;info;warn;error;""
	// +optional
	Level string `json:"level,omitempty"`
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

	// DeploymentTimeout is the maximum time to wait for a deployment to complete.
	// If exceeded, the scheduler assumes the deployment was lost and retries.
	// This is a safety net for race conditions during leadership transitions.
	// Format: Go duration string (e.g., "30s", "1m")
	// Default: 30s
	// +optional
	DeploymentTimeout string `json:"deploymentTimeout,omitempty"`

	// MaxParallel limits concurrent Dataplane API operations during sync.
	// This prevents overwhelming the API when syncing large configurations.
	//
	// Values:
	//   - 0 or unset: auto-calculated as dataplane GOMAXPROCS * 10
	//   - Positive integer: use that exact limit
	//
	// The auto-calculation uses the dataplane container's CPU allocation to determine
	// an appropriate parallelism level that balances throughput and API stability.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxParallel int `json:"maxParallel,omitempty"`

	// RawPushThreshold sets the number of configuration changes that triggers
	// a raw config push instead of fine-grained sync.
	//
	// Fine-grained sync applies individual API operations (create/update/delete)
	// which is efficient for small changes but slow for large deployments.
	// When changes exceed this threshold, the controller pushes the complete
	// configuration as a raw config, which is faster for bulk updates.
	//
	// Additionally, when the remote HAProxy config version is 1 (initial state),
	// raw push is always used regardless of this threshold.
	//
	// Default: 100
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=100
	// +optional
	RawPushThreshold int `json:"rawPushThreshold,omitempty"`
}

// TemplatingSettings configures template rendering behavior.
type TemplatingSettings struct {
	// Engine specifies which template engine to use for rendering.
	//
	// Available engines:
	//   - "scriggo" (default): Go template syntax, high performance rendering
	//
	// Default: scriggo
	// +kubebuilder:validation:Enum=scriggo
	// +optional
	Engine string `json:"engine,omitempty"`

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

	// FieldSelector filters resources using client-side JSONPath evaluation.
	// Unlike Kubernetes' native fieldSelector (which only supports limited fields),
	// this supports any JSONPath expression.
	//
	// Format: "field.path=value"
	// Example: "spec.ingressClassName=haproxy-internal"
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
	// Template is the template content.
	//
	// Can be included in other templates using {{ render "snippet_name" }}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`
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
	// Template is the template for generating the map file content.
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
	// Template is the template for generating the file content.
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
	// Template is the template for generating the certificate content.
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
	// Template is the template for generating haproxy.cfg.
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

	// CurrentConfig contains the raw HAProxy configuration from a previous deployment.
	//
	// This is used for testing slot-aware server assignment during rolling deployments.
	// When provided, templates can access currentConfig to preserve server slot ordering.
	// The content is parsed using the HAProxy config parser before being passed to templates.
	//
	// Example:
	//   currentConfig: |
	//     backend my-backend
	//         server srv1 10.0.0.1:8080
	//         server srv2 10.0.0.2:8080
	// +optional
	CurrentConfig string `json:"currentConfig,omitempty"`

	// ExtraContext provides custom variables that override the global extraContext for this test.
	//
	// This allows testing template behavior with different extraContext values without
	// modifying the global configuration.
	//
	// Example:
	//   extraContext:
	//     sanitize_auth_realm: true
	//     debug: true
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	ExtraContext runtime.RawExtension `json:"extraContext,omitempty"`

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
	//   - deterministic: Verifies that rendering twice produces identical output
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=haproxy_valid;contains;not_contains;equals;jsonpath;match_count;match_order;deterministic
	Type string `json:"type"`

	// Description explains what this assertion validates.
	// +optional
	Description string `json:"description,omitempty"`

	// Target specifies what to validate.
	//
	// Format depends on assertion type:
	//   - haproxy_valid: not used
	//   - contains/not_contains/equals: "haproxy.cfg", "map:<name>", "file:<name>", "cert:<name>"
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

	// ValidationErrors contains detailed validation error messages.
	// Each entry includes the template name, error location, and context.
	// +optional
	ValidationErrors []string `json:"validationErrors,omitempty"`

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
