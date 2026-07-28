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
// +kubebuilder:printcolumn:name="Observed",type=integer,JSONPath=`.status.observedGeneration`,description="Spec generation the controller last processed"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyTemplateConfig defines the configuration for the HAProxy Template Ingress Controller.
//
// As a custom resource it provides OpenAPI validation, type safety, and support for
// embedded validation tests.
type HAProxyTemplateConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyTemplateConfigSpec   `json:"spec,omitempty"`
	Status HAProxyTemplateConfigStatus `json:"status,omitempty"`
}

// HAProxyTemplateConfigSpec defines the desired state of HAProxyTemplateConfig.
//
// A controller can be pointed at several of these at once (`--crd-name` takes an
// ordered list) and merges them, later wins. **The merged set, not any single
// object, is the unit of completeness** — the chart emits one config per template
// library, and a library config legitimately carries nothing but
// `templateSnippets` and `validationTests`. That is why fields the controller
// genuinely requires are marked optional here and enforced after the merge by
// config.ValidateStructure instead of by the apiserver.
type HAProxyTemplateConfigSpec struct {
	// CredentialsSecretRef references the Secret containing HAProxy Dataplane API credentials.
	//
	// The Secret must contain the following keys:
	//   - dataplane_username: Username for HAProxy Dataplane API
	//   - dataplane_password: Password for HAProxy Dataplane API
	//
	// If the namespace is omitted, it defaults to the same namespace as this config resource.
	// +optional
	CredentialsSecretRef SecretReference `json:"credentialsSecretRef,omitempty"`

	// PodSelector identifies which HAProxy pods to configure.
	//
	// Required in the merged config (config.ValidateStructure rejects an empty
	// matchLabels); optional per object so a library config need not repeat it.
	// +optional
	PodSelector PodSelector `json:"podSelector,omitempty"`

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
	//
	// The merged config must declare at least one (config.ValidateStructure);
	// a single object may declare none.
	// +optional
	WatchedResources map[string]WatchedResource `json:"watchedResources,omitempty"`

	// Validators declares pluggable validator sidecars consulted by the
	// admission webhook before admitting changes that affect plugin
	// configuration. See `docs/site/docs/operations/pluggable-validators.md`
	// for setup; the wire protocol is at
	// `docs/development/validator-protocol.md`.
	//
	// An empty list (the default) disables pluggable validation — the
	// webhook keeps performing template + HAProxy syntax dry-run only.
	// +optional
	// +listType=map
	// +listMapKey=name
	Validators []ValidatorConfig `json:"validators,omitempty"`

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

	// K8sResources maps resource template names to their template
	// definitions. Each entry's rendered output is parsed as one or
	// more Kubernetes resources (multi-doc YAML, separated by `---`)
	// and applied via Server-Side Apply with field manager `haptic`.
	//
	// The controller injects an OwnerReference to the
	// HAProxyTemplateConfig CR (controller=true,
	// blockOwnerDeletion=true) on every applied resource so cascade
	// garbage collection removes them when the CR is deleted (e.g.
	// `helm uninstall`). Resources that disappear from the rendered
	// set across reconciliations are pruned via the
	// `haproxy-haptic.org/managed-by` label the applier injects.
	//
	// Templates have full access to the same engine context as
	// haproxyConfig — `resources`, filters, `templateSnippets`,
	// `fileRegistry`, `extraContext`, etc. — so a k8sResources
	// template can render extension points (`render_glob` patterns)
	// and consume cached state (`shared.ComputeIfAbsent`).
	// +optional
	K8sResources map[string]K8sResource `json:"k8sResources,omitempty"`

	// HAProxyConfig contains the main HAProxy configuration template.
	//
	// Exactly one object in a merged set supplies it (the base library);
	// config.ValidateStructure rejects a merged config whose template is empty.
	// +optional
	HAProxyConfig HAProxyConfig `json:"haproxyConfig,omitempty"`

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

	// ValidationTestsSelector selects HAProxyValidationTests objects in this
	// namespace whose tests join the ones above. The controller runs the union;
	// a test name may appear in only one source.
	//
	// Tests are kept out of this object because they dominate its size while
	// being needed only when the configuration is validated, never when it is
	// rendered. Nothing forces that split: inline tests remain fully supported,
	// and an operator using neither the chart nor this selector loses nothing.
	//
	// A nil selector matches nothing. An empty selector (`{}`) matches every
	// HAProxyValidationTests in the namespace, which is how two HAPTIC releases
	// in one namespace would steal each other's tests — the chart sets a
	// release-scoped selector rather than leaving it empty.
	// +optional
	ValidationTestsSelector *metav1.LabelSelector `json:"validationTestsSelector,omitempty"`

	// RequireValidationTests refuses to load a configuration that ends up with
	// no validation tests at all.
	//
	// This exists because an empty suite is an unconditional pass: a selector
	// typo, a missing RBAC rule or an unsynced cache would otherwise leave the
	// load gate running zero tests and reporting success, which is
	// indistinguishable from a configuration that genuinely has none. Set it
	// whenever tests are expected, and the difference becomes a refusal instead
	// of silence.
	//
	// It is enforced only when the configuration is loaded, never at admission:
	// during a fresh install the configuration is admitted before the tests
	// objects exist, so enforcing it there would deadlock on apply ordering.
	// +optional
	RequireValidationTests bool `json:"requireValidationTests,omitempty"`

	// MigrationCoverage declares, per migration source (another ingress
	// controller whose annotations a template library emulates), how each
	// of the source's annotations is handled. The controller treats this
	// as opaque data: it is contributed by template libraries and consumed
	// by tooling such as `migrate-check` — no entry influences rendering or
	// reconciliation.
	//
	// This is the one spec field that ACCUMULATES across a merged set rather
	// than being overwritten: every contributing library's declaration
	// survives, in merge order. See conversion.MergeSpecs.
	// +optional
	// +listType=map
	// +listMapKey=source
	MigrationCoverage []MigrationCoverageSource `json:"migrationCoverage,omitempty"`
}

// MigrationCoverageSource documents one migration source: how to detect
// resources managed by that source controller and how each of its
// annotations is handled by the template libraries.
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (MigrationCoverageSource)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function)
type MigrationCoverageSource struct {
	// Source names the migration source controller (e.g. as printed by
	// migration tooling). Unique across the list.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Source string `json:"source"`

	// Detect describes how to recognise resources managed by the source.
	// +optional
	Detect MigrationDetect `json:"detect,omitempty"`

	// Annotations maps each full annotation key of the source controller
	// to its migration classification.
	// +optional
	Annotations map[string]AnnotationCoverage `json:"annotations,omitempty"`
}

// MigrationDetect describes how migration tooling recognises resources
// managed by a migration source controller.
type MigrationDetect struct {
	// IngressClasses lists spec.ingressClassName values the source
	// controller conventionally serves (e.g. "nginx").
	// +optional
	IngressClasses []string `json:"ingressClasses,omitempty"`

	// AnnotationPrefixes lists annotation key prefixes owned by the
	// source controller (e.g. "nginx.ingress.kubernetes.io/").
	// +optional
	AnnotationPrefixes []string `json:"annotationPrefixes,omitempty"`
}

// AnnotationCoverage classifies how one source-controller annotation is
// handled by the template libraries.
type AnnotationCoverage struct {
	// Status classifies the annotation:
	//   - supported: semantics carried over.
	//   - different: acted on, but with behaviour differences to check.
	//   - dropped: accepted with no effect.
	//   - fails: setting it fails the render with an error.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=supported;different;dropped;fails
	Status string `json:"status"`

	// Note explains the classification in plain language.
	// +optional
	Note string `json:"note,omitempty"`

	// Doc is an anchor into the migration guide (docs/site/docs/
	// migrating.md) with more detail, e.g. "annotation-support".
	// +optional
	Doc string `json:"doc,omitempty"`
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
	// Format: Go duration string (e.g., "15s", "30s")
	// Default: 30s (DefaultLeaderElectionLeaseDuration in pkg/core/config/defaults.go;
	// the Helm chart's values.yaml sets the same value at controller.config.controller.leaderElection.leaseDuration)
	// +optional
	LeaseDuration string `json:"leaseDuration,omitempty"`

	// RenewDeadline is the duration that the acting leader will retry
	// refreshing leadership before giving up.
	//
	// Format: Go duration string (e.g., "20s")
	// Default: 20s (DefaultLeaderElectionRenewDeadline)
	// Must be less than LeaseDuration
	// +optional
	RenewDeadline string `json:"renewDeadline,omitempty"`

	// RetryPeriod is the duration the LeaderElector clients should wait
	// between tries of actions.
	//
	// Format: Go duration string (e.g., "5s")
	// Default: 5s (DefaultLeaderElectionRetryPeriod)
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
	// A value of 0 is treated as unset and the default applies.
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
	// Default: /etc/haproxy/certs (the Helm chart sets /etc/haproxy/ssl)
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

	// ConfigPublishInterval throttles how often the rendered HAProxy config is
	// republished as the HAProxyCfg observability CRD. The CRD itself is not on
	// the deployment hot path, but during endpoint churn rewriting the (~500 KB)
	// CRD on every reconciliation creates significant etcd write pressure;
	// throttling republishes reduces that write load while leaving the
	// event-driven push to HAProxy pods untouched.
	//
	// Format: Go duration string (e.g., "10s", "1m").
	// Default: 10s
	// +optional
	ConfigPublishInterval string `json:"configPublishInterval,omitempty"`

	// ReloadVerificationTimeout is the maximum time the Dataplane sync waits for
	// HAProxy to report a graceful reload as completed before failing the sync.
	// Set this higher than the Dataplane API's own reload-delay setting.
	//
	// Format: Go duration string (e.g., "10s", "30s").
	// Default: 10s
	// +optional
	ReloadVerificationTimeout string `json:"reloadVerificationTimeout,omitempty"`

	// SyncTimeout is the overall timeout for a single Dataplane sync to one
	// HAProxy endpoint. The sync covers parse + diff + raw config push +
	// optional reload-verification. If exceeded, the sync is cancelled and the
	// scheduler retries on the next reconciliation.
	//
	// Format: Go duration string (e.g., "2m", "30s").
	// Default: 2m
	// +optional
	SyncTimeout string `json:"syncTimeout,omitempty"`
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
	//
	// Mutually exclusive with apiVersions (exactly one must be set; the
	// controller's config validation enforces this). Equivalent to a
	// one-element apiVersions list.
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// APIVersions is an ordered candidate list of API versions. The
	// controller resolves the entry to the FIRST candidate the apiserver
	// serves and watches that version. Mutually exclusive with apiVersion.
	//
	// Example: ["gateway.networking.k8s.io/v1", "gateway.networking.k8s.io/v1beta1"]
	// +optional
	// +kubebuilder:validation:MinItems=1
	APIVersions []string `json:"apiVersions,omitempty"`

	// Optional marks this resource as non-essential: when NO candidate
	// version is served by the cluster, the watch is dropped and every
	// templateSnippet / validationTest whose requires list names this
	// resource is stripped from the effective config at load time, instead
	// of failing startup.
	// +optional
	Optional bool `json:"optional,omitempty"`

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
	// Equality-only: comma-separated "key=value" pairs.
	// Example: "app=nginx,environment=production"
	// Set-based syntax (e.g. "tier in (frontend,api)", "!disabled") is NOT supported —
	// the controller's parseLabelSelector splits on ',' and '=' and silently drops the rest.
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

	// DebounceInterval overrides the watcher's refractory window.
	// "0" disables debouncing; default 2s.
	// +optional
	DebounceInterval string `json:"debounceInterval,omitempty"`
}

// TemplateSnippet defines a reusable template fragment.
type TemplateSnippet struct {
	// Template is the template content.
	//
	// Can be included in other templates using {{ render "snippet_name" }}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`

	// Requires lists watched-resource names this snippet depends on. When an
	// optional watched resource has no served candidate version, every
	// snippet requiring it is stripped from the effective config at load
	// time. Each entry must name a key of watchedResources.
	// +optional
	Requires []string `json:"requires,omitempty"`
}

// PostProcessorConfig defines a post-processor to apply to rendered template output.
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (PostProcessorConfig)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function)
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

// K8sResource defines a template that emits one or more Kubernetes
// resources via multi-doc YAML.
//
// IMPORTANT: This is a Kubernetes CRD type. When modifying this struct, you must also update:
//   - The internal config type: pkg/core/config/types.go (K8sResource)
//   - The conversion logic: pkg/controller/conversion/converter.go (ConvertSpec function - k8sResources section)
type K8sResource struct {
	// Template is the template for generating the resource YAML.
	//
	// The rendered output is parsed as one or more Kubernetes
	// resources (separate documents with `---`). Each document must
	// declare `apiVersion`, `kind`, and `metadata.name` (plus
	// `metadata.namespace` for namespaced kinds).
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
	//
	// Optional per object so several libraries can each contribute part of the
	// shared `_global` baseline; see Assertions.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Fixtures map[string][]runtime.RawExtension `json:"fixtures,omitempty"`

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

	// CurrentFiles are the currently-deployed general auxiliary files
	// (filename → content), exposed to templates under `currentFiles`. Used to
	// test templates that read their own prior output — e.g. self-rotating TLS
	// session-ticket keys inspecting the current key file's embedded date marker.
	// +optional
	CurrentFiles map[string]string `json:"currentFiles,omitempty"`

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

	// MinHAProxyVersion specifies the minimum HAProxy version required to run this test.
	//
	// When set, the test is skipped if the local HAProxy version is below this threshold.
	// This is useful for tests that use HAProxy features only available in newer versions
	// (e.g., shm-stats-file requires HAProxy 3.3+).
	//
	// Format: "major.minor" (e.g., "3.3")
	// +optional
	MinHAProxyVersion string `json:"minHAProxyVersion,omitempty"`

	// Requires lists watched-resource names this test depends on. When an
	// optional watched resource has no served candidate version, every test
	// requiring it is stripped from the effective config at load time.
	// Each entry must name a key of watchedResources.
	// +optional
	Requires []string `json:"requires,omitempty"`

	// RequiresFields lists schema field paths this test depends on, each in
	// the form "<watchedResourceKey>.<field.path>" (e.g.
	// "httproutes.spec.rules.filters.cors"). When any referenced field is
	// absent from the resolved schema generation of its watched resource,
	// the test is stripped from the effective config at load time. This
	// covers clusters that serve the resource at the same API version as
	// newer releases but with an older schema generation lacking the field.
	// The first dot-segment of each entry must name a key of
	// watchedResources.
	// +optional
	RequiresFields []string `json:"requiresFields,omitempty"`

	// Assertions defines the validation checks to perform.
	//
	// Optional in the schema, required in practice: config.ValidateStructure
	// rejects a merged config whose test declares none. The schema cannot
	// express it because the reserved `_global` entry is a shared baseline
	// rather than a test — the runner never executes its assertions — and
	// several template libraries each contribute part of it, so each of their
	// objects carries an incomplete `_global` that only becomes whole after
	// the merge.
	// +optional
	Assertions []ValidationAssertion `json:"assertions,omitempty"`
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
