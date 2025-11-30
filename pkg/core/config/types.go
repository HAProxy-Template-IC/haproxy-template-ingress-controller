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

// Package config provides data models for the controller configuration.
//
// These models represent the structure of the configuration YAML loaded
// from the Kubernetes ConfigMap and credentials from the Secret.
package config

// Config is the root configuration structure loaded from the ConfigMap.
type Config struct {
	// PodSelector identifies HAProxy pods to configure.
	PodSelector PodSelector `yaml:"pod_selector"`

	// Controller contains controller-level settings (ports, etc.).
	Controller ControllerConfig `yaml:"controller"`

	// Logging configures logging behavior.
	Logging LoggingConfig `yaml:"logging"`

	// Dataplane configures the Dataplane API for production HAProxy instances.
	Dataplane DataplaneConfig `yaml:"dataplane"`

	// TemplatingSettings configures template rendering behavior and custom variables.
	TemplatingSettings TemplatingSettings `yaml:"templating_settings" json:"templatingSettings"`

	// WatchedResourcesIgnoreFields specifies JSONPath expressions for fields
	// to remove from all watched resources to reduce memory usage.
	//
	// Example: ["metadata.managedFields"]
	WatchedResourcesIgnoreFields []string `yaml:"watched_resources_ignore_fields"`

	// WatchedResources maps resource type names to their watch configuration.
	//
	// Example:
	//   ingresses:
	//     api_version: networking.k8s.io/v1
	//     kind: Ingress
	//     index_by: ["metadata.namespace", "metadata.name"]
	WatchedResources map[string]WatchedResource `yaml:"watched_resources"`

	// TemplateSnippets maps snippet names to reusable template fragments.
	//
	// Snippets can be included in other templates using {% include "name" %}.
	TemplateSnippets map[string]TemplateSnippet `yaml:"template_snippets"`

	// Maps maps map file names to their template definitions.
	//
	// These generate HAProxy map files for backend routing and other features.
	Maps map[string]MapFile `yaml:"maps"`

	// Files maps file names to their template definitions.
	//
	// These generate auxiliary files like custom error pages.
	Files map[string]GeneralFile `yaml:"files"`

	// SSLCertificates maps certificate names to their template definitions.
	//
	// These generate SSL certificate files for HAProxy.
	SSLCertificates map[string]SSLCertificate `yaml:"ssl_certificates"`

	// CRTLists maps crt-list file names to their template definitions.
	//
	// These generate crt-list files for SSL certificate lists with per-certificate options.
	CRTLists map[string]CRTListFile `yaml:"crt_lists"`

	// HAProxyConfig contains the main HAProxy configuration template.
	HAProxyConfig HAProxyConfig `yaml:"haproxy_config"`

	// ValidationTests contains embedded tests for validating template rendering.
	// These tests are used both in CLI validation and webhook admission validation.
	// The map key is the test name, which must be unique.
	ValidationTests map[string]ValidationTest `yaml:"validation_tests"`
}

// ValidationTest defines a single validation test with fixtures and assertions.
//
// The test name is provided by the map key in ValidationTests.
type ValidationTest struct {
	// Description explains what this test validates.
	Description string `yaml:"description"`

	// Fixtures contains mock Kubernetes resources for this test.
	// The map key is the resource type name (e.g., "services", "ingresses").
	// The map value is a list of resources in unstructured format.
	Fixtures map[string][]interface{} `yaml:"fixtures"`

	// HTTPFixtures contains mock HTTP responses for this test.
	// When templates call http.Fetch() for a URL that matches a fixture,
	// the fixture content is returned instead of making an actual HTTP request.
	HTTPFixtures []HTTPResourceFixture `yaml:"httpResources,omitempty"`

	// Assertions contains validation checks to run against the rendered config.
	Assertions []ValidationAssertion `yaml:"assertions"`
}

// HTTPResourceFixture defines mock HTTP content for validation tests.
//
// This allows templates that use http.Fetch() to receive pre-defined content
// during validation tests without making actual HTTP requests.
type HTTPResourceFixture struct {
	// URL is the HTTP URL that will be matched.
	// When a template calls http.Fetch() with this URL, the fixture content is returned.
	URL string `yaml:"url" json:"url"`

	// Content is the response body to return when this URL is fetched.
	Content string `yaml:"content" json:"content"`
}

// ValidationAssertion defines a single validation check.
type ValidationAssertion struct {
	// Type is the assertion type: "haproxy_valid", "contains", "not_contains", "match_count", "equals", "jsonpath", "match_order".
	Type string `yaml:"type"`

	// Description explains what this assertion validates.
	Description string `yaml:"description"`

	// Target specifies what to validate: "haproxy.cfg", "map:<name>", "file:<name>", "cert:<name>".
	// Only used for non-haproxy_valid assertions.
	Target string `yaml:"target"`

	// Pattern is the regex pattern to match (for "contains" and "not_contains").
	Pattern string `yaml:"pattern"`

	// Expected is the expected value (for "equals" and "jsonpath").
	Expected string `yaml:"expected"`

	// JSONPath is the JSONPath expression to query (for "jsonpath").
	JSONPath string `yaml:"jsonpath"`

	// Patterns is a list of regex patterns that must appear in the specified order (for "match_order").
	Patterns []string `yaml:"patterns"`
}

// PodSelector identifies which HAProxy pods to configure.
type PodSelector struct {
	// MatchLabels are the labels to match HAProxy pods.
	//
	// Example:
	//   app: haproxy
	//   component: loadbalancer
	MatchLabels map[string]string `yaml:"match_labels"`
}

// ControllerConfig contains controller-level configuration.
type ControllerConfig struct {
	// HealthzPort is the port for health check endpoints.
	// A value of 0 means "uninitialized" and will be replaced with the default.
	// This is a production port and MUST NOT remain 0 after defaults are applied.
	// Default: 8080
	HealthzPort int `yaml:"healthz_port"`

	// MetricsPort is the port for Prometheus metrics.
	// A value of 0 means "uninitialized" and will be replaced with the default.
	// This is a production port and MUST NOT remain 0 after defaults are applied.
	// Default: 9090
	MetricsPort int `yaml:"metrics_port"`

	// LeaderElection configures leader election for high availability.
	LeaderElection LeaderElectionConfig `yaml:"leader_election"`
}

// LeaderElectionConfig configures leader election for running multiple replicas.
type LeaderElectionConfig struct {
	// Enabled determines whether leader election is active.
	// If false, the controller assumes it is the sole instance (single-replica mode).
	// Default: true
	Enabled bool `yaml:"enabled"`

	// LeaseName is the name of the Lease resource used for coordination.
	// Default: haproxy-template-ic-leader
	LeaseName string `yaml:"lease_name"`

	// LeaseDuration is the duration that non-leader candidates will wait
	// to force acquire leadership (measured against time of last observed ack).
	// Format: Go duration string (e.g., "60s", "1m")
	// Default: 60s
	// Minimum: 15s
	LeaseDuration string `yaml:"lease_duration"`

	// RenewDeadline is the duration that the acting leader will retry
	// refreshing leadership before giving up.
	// Format: Go duration string (e.g., "15s")
	// Default: 15s
	// Must be less than LeaseDuration
	RenewDeadline string `yaml:"renew_deadline"`

	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions.
	// Format: Go duration string (e.g., "5s")
	// Default: 5s
	// Must be less than RenewDeadline
	RetryPeriod string `yaml:"retry_period"`
}

// LoggingConfig configures logging behavior.
type LoggingConfig struct {
	// Verbose controls log level: 0=WARNING, 1=INFO, 2=DEBUG
	// Default: 1
	Verbose int `yaml:"verbose"`
}

// DataplaneConfig configures the Dataplane API for production HAProxy instances.
type DataplaneConfig struct {
	// Port is the Dataplane API port for production HAProxy pods.
	// A value of 0 means "uninitialized" and will be replaced with the default.
	// This is a production port and MUST NOT remain 0 after defaults are applied.
	// Default: 5555
	Port int `yaml:"port"`

	// MinDeploymentInterval enforces minimum time between consecutive deployments.
	// This prevents rapid-fire deployments from hammering HAProxy instances.
	// Format: Go duration string (e.g., "2s", "500ms")
	// Default: 2s
	MinDeploymentInterval string `yaml:"min_deployment_interval"`

	// DriftPreventionInterval triggers periodic deployments to prevent configuration drift.
	// A deployment is automatically triggered if no deployment has occurred within this interval.
	// This detects and corrects drift caused by external Dataplane API clients.
	// Format: Go duration string (e.g., "60s", "5m")
	// Default: 60s
	DriftPreventionInterval string `yaml:"drift_prevention_interval"`

	// MapsDir is the directory for HAProxy map files.
	// Used for both validation and deployment.
	// Default: /etc/haproxy/maps
	MapsDir string `yaml:"maps_dir"`

	// SSLCertsDir is the directory for SSL certificates.
	// Used for both validation and deployment.
	// Default: /etc/haproxy/ssl
	SSLCertsDir string `yaml:"ssl_certs_dir"`

	// GeneralStorageDir is the directory for general files (error pages, etc.).
	// Used for both validation and deployment.
	// Default: /etc/haproxy/general
	GeneralStorageDir string `yaml:"general_storage_dir"`

	// ConfigFile is the path to the main HAProxy configuration file.
	// Used for validation.
	// Default: /etc/haproxy/haproxy.cfg
	ConfigFile string `yaml:"config_file"`
}

// WatchedResource configures watching for a specific Kubernetes resource type.
type WatchedResource struct {
	// APIVersion is the Kubernetes API version (e.g., "networking.k8s.io/v1").
	APIVersion string `yaml:"api_version"`

	// Resources is the plural form of the Kubernetes resource type (e.g., "ingresses", "services").
	// This is the name used in RBAC rules and API paths.
	Resources string `yaml:"resources"`

	// EnableValidationWebhook enables admission webhook validation for this resource.
	// Default: false
	EnableValidationWebhook bool `yaml:"enable_validation_webhook"`

	// IndexBy specifies JSONPath expressions for extracting index keys.
	//
	// Resources are indexed by these values for O(1) lookup.
	//
	// Examples:
	//   ["metadata.namespace", "metadata.name"]
	//   ["metadata.labels['kubernetes.io/service-name']"]
	IndexBy []string `yaml:"index_by"`

	// LabelSelector filters resources by labels (server-side filtering).
	//
	// Example:
	//   app: haproxy
	//   component: loadbalancer
	LabelSelector map[string]string `yaml:"label_selector,omitempty"`

	// Store specifies the storage backend: "full" (MemoryStore) or "on-demand" (CachedStore).
	// Default: "full"
	//
	// Use "on-demand" for large resources that are accessed infrequently (e.g., Secrets).
	// Use "full" for frequently accessed resources (e.g., Ingress, Service, EndpointSlice).
	Store string `yaml:"store"`
}

// TemplateSnippet is a reusable template fragment.
type TemplateSnippet struct {
	// Name is the snippet identifier for {% include "name" %}.
	// This is derived from the map key in the configuration.
	Name string

	// Template is the template content.
	Template string `yaml:"template"`

	// Priority determines the rendering order when multiple snippets are included.
	// Lower values are rendered first. Snippets with the same priority are sorted alphabetically by name.
	// Default: 500
	Priority int `yaml:"priority"`
}

// MapFile is an HAProxy map file template.
type MapFile struct {
	// Template is the template content that generates the map file.
	Template string `yaml:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	// Post-processors are applied in order to transform the rendered output.
	PostProcessing []PostProcessorConfig `yaml:"post_processing,omitempty"`
}

// GeneralFile is a general-purpose auxiliary file template.
type GeneralFile struct {
	// Template is the template content that generates the file.
	Template string `yaml:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	// Post-processors are applied in order to transform the rendered output.
	PostProcessing []PostProcessorConfig `yaml:"post_processing,omitempty"`
}

// SSLCertificate is an SSL certificate file template.
type SSLCertificate struct {
	// Template is the template content that generates the certificate file.
	Template string `yaml:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	// Post-processors are applied in order to transform the rendered output.
	PostProcessing []PostProcessorConfig `yaml:"post_processing,omitempty"`
}

// CRTListFile is a crt-list file template.
//
// CRT-list files contain entries that map SSL certificates to specific SNI patterns
// with per-certificate options. This enables advanced SSL certificate management with
// features like SNI routing, OCSP stapling, and per-certificate TLS settings.
type CRTListFile struct {
	// Template is the template content that generates the crt-list file.
	Template string `yaml:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	// Post-processors are applied in order to transform the rendered output.
	PostProcessing []PostProcessorConfig `yaml:"post_processing,omitempty"`
}

// HAProxyConfig is the main HAProxy configuration template.
type HAProxyConfig struct {
	// Template is the template content that generates haproxy.cfg.
	Template string `yaml:"template"`

	// PostProcessing defines optional post-processors to apply after rendering.
	// Post-processors are applied in order to transform the rendered output.
	// Common use case: indentation normalization with regex_replace.
	//
	// Example:
	//   post_processing:
	//     - type: regex_replace
	//       params:
	//         pattern: "^[ ]+"
	//         replace: "  "
	PostProcessing []PostProcessorConfig `yaml:"post_processing,omitempty"`
}

// PostProcessorConfig defines a post-processor to apply to rendered template output.
type PostProcessorConfig struct {
	// Type specifies the post-processor type.
	// Supported values: "regex_replace"
	Type string `yaml:"type"`

	// Params contains type-specific configuration parameters.
	// For regex_replace:
	//   - pattern: Regular expression pattern to match (required)
	//   - replace: Replacement string (required)
	Params map[string]string `yaml:"params"`
}

// TemplatingSettings configures template rendering behavior and custom variables.
type TemplatingSettings struct {
	// ExtraContext provides custom variables that are passed to all templates.
	//
	// This allows users to add arbitrary data to the template context without
	// modifying controller code. Values can be any valid JSON type (string, number,
	// boolean, object, array).
	//
	// Example in YAML:
	//   extra_context:
	//     debug:
	//       enabled: true
	//     environment: production
	//     custom_value: 42
	//
	// Templates can then reference these variables directly: {{ debug.enabled }}, {{ environment }}, etc.
	ExtraContext map[string]interface{} `yaml:"extra_context" json:"extraContext"`
}

// Credentials contains HAProxy Dataplane API credentials.
//
// This is loaded from the Kubernetes Secret, not the ConfigMap.
type Credentials struct {
	// DataplaneUsername is the username for production HAProxy instances.
	DataplaneUsername string

	// DataplanePassword is the password for production HAProxy instances.
	DataplanePassword string
}
