# HAProxyTemplateConfig CRD Implementation Design

## Overview

### Problem Statement

The HAProxy Template Ingress Controller uses a template-driven approach where users define HAProxy configurations using Gonja templates. While this provides flexibility, it introduces a confidence gap: unlike traditional ingress controllers where annotations are part of the code (and therefore tested), here the templates are part of the configuration and remain untested until deployment.

This creates risk:
- Invalid templates cause runtime failures
- Configuration errors only discovered after applying changes
- No validation feedback before deployment

### Solution

Migrate from ConfigMap-based configuration to a Custom Resource Definition (CRD) that:

1. **Provides declarative configuration** - Kubernetes-native resource with proper validation
2. **Embeds validation tests** - Users define test fixtures and assertions inline
3. **Validates on admission** - Webhook runs embedded tests before accepting changes
4. **Enables CLI validation** - Pre-apply validation via `controller validate` subcommand
5. **Multi-layer defense** - OpenAPI schema, webhook validation, runtime validation

### Design Principles

- **Security first**: Credentials stay in Secrets, never in CRDs
- **Fail-open webhook**: `failurePolicy: Ignore` prevents deadlock when controller is down
- **Simple failure mode**: Invalid config causes crash loop (visible in monitoring)
- **No complex fallbacks**: Keep it simple - let monitoring systems detect failures
- **Namespace isolation**: Webhook only validates resources with specific labels

## Phase 1: CRD API Design

### API Group and Version

- **Group**: `haproxy-template-ic.github.io`
- **Version**: `v1alpha1` (pre-release, API may change)
- **Kind**: `HAProxyTemplateConfig`
- **Plural**: `haproxytemplateconfigs`
- **Short names**: `htplcfg`, `haptpl`

### Resource Structure

```yaml
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: my-haproxy-config
  namespace: default
  labels:
    app.kubernetes.io/name: haproxy-template-ic
    app.kubernetes.io/instance: my-instance
spec:
  # Reference to Secret containing credentials
  credentialsSecretRef:
    name: haproxy-credentials
    namespace: default  # optional, defaults to same namespace as config

  # Pod selector for HAProxy instances
  podSelector:
    matchLabels:
      app: haproxy
      component: loadbalancer

  # Controller settings
  controller:
    healthzPort: 8080
    metricsPort: 9090
    leaderElection:
      enabled: true
      leaseName: haproxy-template-ic-leader
      leaseDuration: 60s
      renewDeadline: 15s
      retryPeriod: 5s

  # Logging configuration
  logging:
    verbose: 1  # 0=WARNING, 1=INFO, 2=DEBUG

  # Dataplane API configuration
  dataplane:
    port: 5555
    minDeploymentInterval: 2s
    driftPreventionInterval: 60s
    mapsDir: /etc/haproxy/maps
    sslCertsDir: /etc/haproxy/ssl
    generalStorageDir: /etc/haproxy/general
    configFile: /etc/haproxy/haproxy.cfg

  # JSONPath fields to ignore across all resources
  watchedResourcesIgnoreFields:
    - metadata.managedFields
    - metadata.resourceVersion

  # Watched Kubernetes resources
  watchedResources:
    ingresses:
      apiVersion: networking.k8s.io/v1
      resources: ingresses
      enableValidationWebhook: true
      indexBy:
        - metadata.namespace
        - metadata.name
      labelSelector: ""
      fieldSelector: ""
      namespaceSelector: ""

    services:
      apiVersion: v1
      resources: services
      indexBy:
        - metadata.namespace
        - metadata.name

  # Template snippets
  templateSnippets:
    ssl_bind_options: |
      ssl-min-ver TLSv1.2
      ssl-default-bind-ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256

  # HAProxy map files
  maps:
    domain_to_backend:
      template: |
        {% for ingress in ingresses %}
        {% for rule in ingress.spec.rules %}
        {{ rule.host }} {{ rule.host }}_backend
        {% endfor %}
        {% endfor %}

  # General files
  files:
    error_503:
      template: |
        HTTP/1.1 503 Service Unavailable
        Content-Type: text/html

        <html><body><h1>503 Service Unavailable</h1></body></html>
      path: /etc/haproxy/errors/503.http

  # SSL certificates
  sslCertificates:
    wildcard_example_com:
      template: |
        {{ secrets["tls-wildcard"].data["tls.crt"] | b64decode }}
        {{ secrets["tls-wildcard"].data["tls.key"] | b64decode }}

  # Main HAProxy configuration
  haproxyConfig:
    template: |
      global
          maxconn 4096
          log stdout format raw local0 info

      defaults
          mode http
          timeout connect 5s
          timeout client 50s
          timeout server 50s

      frontend http-in
          bind *:80
          use_backend %[req.hdr(host),lower,map(/etc/haproxy/maps/domain_to_backend.map)]

  # Embedded validation tests
  validationTests:
    - name: test_basic_ingress
      description: Validate that a basic ingress generates valid HAProxy config
      fixtures:
        ingresses:
          - apiVersion: networking.k8s.io/v1
            kind: Ingress
            metadata:
              name: test-ingress
              namespace: default
            spec:
              rules:
                - host: example.com
                  http:
                    paths:
                      - path: /
                        pathType: Prefix
                        backend:
                          service:
                            name: test-service
                            port:
                              number: 80
        services:
          - apiVersion: v1
            kind: Service
            metadata:
              name: test-service
              namespace: default
            spec:
              selector:
                app: test
              ports:
                - port: 80
                  targetPort: 8080
      assertions:
        - type: haproxy_valid
          description: Generated config must pass HAProxy validation

        - type: contains
          description: Config must include frontend for example.com
          target: haproxy_config
          pattern: "example.com"

        - type: contains
          description: Map file must include domain mapping
          target: maps.domain_to_backend
          pattern: "example.com example.com_backend"

status:
  # Controller updates these fields
  observedGeneration: 1
  lastValidated: "2025-01-27T10:00:00Z"
  validationStatus: Valid
  validationMessage: "All validation tests passed"
```

### Go Type Definitions

Location: `pkg/apis/haproxytemplate/v1alpha1/types.go`

```go
package v1alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=htplcfg;haptpl,scope=Namespaced
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.validationStatus`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyTemplateConfig defines the configuration for the HAProxy Template Ingress Controller.
type HAProxyTemplateConfig struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   HAProxyTemplateConfigSpec   `json:"spec,omitempty"`
    Status HAProxyTemplateConfigStatus `json:"status,omitempty"`
}

// HAProxyTemplateConfigSpec defines the desired state of HAProxyTemplateConfig.
type HAProxyTemplateConfigSpec struct {
    // CredentialsSecretRef references the Secret containing HAProxy Dataplane API credentials.
    // The Secret must contain the following keys:
    //   - dataplane_username: Username for production HAProxy Dataplane API
    //   - dataplane_password: Password for production HAProxy Dataplane API
    //   - validation_username: Username for validation HAProxy instance
    //   - validation_password: Password for validation HAProxy instance
    // +kubebuilder:validation:Required
    CredentialsSecretRef SecretReference `json:"credentialsSecretRef"`

    // PodSelector identifies which HAProxy pods to configure.
    // +kubebuilder:validation:Required
    PodSelector PodSelector `json:"podSelector"`

    // Controller contains controller-level settings.
    // +optional
    Controller ControllerConfig `json:"controller,omitempty"`

    // Logging configures logging behavior.
    // +optional
    Logging LoggingConfig `json:"logging,omitempty"`

    // Dataplane configures the Dataplane API for production HAProxy instances.
    // +optional
    Dataplane DataplaneConfig `json:"dataplane,omitempty"`

    // WatchedResourcesIgnoreFields specifies JSONPath expressions for fields
    // to remove from all watched resources to reduce memory usage.
    // +optional
    WatchedResourcesIgnoreFields []string `json:"watchedResourcesIgnoreFields,omitempty"`

    // WatchedResources maps resource type names to their watch configuration.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinProperties=1
    WatchedResources map[string]WatchedResource `json:"watchedResources"`

    // TemplateSnippets maps snippet names to reusable template fragments.
    // +optional
    TemplateSnippets map[string]TemplateSnippet `json:"templateSnippets,omitempty"`

    // Maps maps map file names to their template definitions.
    // +optional
    Maps map[string]MapFile `json:"maps,omitempty"`

    // Files maps file names to their template definitions.
    // +optional
    Files map[string]GeneralFile `json:"files,omitempty"`

    // SSLCertificates maps certificate names to their template definitions.
    // +optional
    SSLCertificates map[string]SSLCertificate `json:"sslCertificates,omitempty"`

    // HAProxyConfig contains the main HAProxy configuration template.
    // +kubebuilder:validation:Required
    HAProxyConfig HAProxyConfig `json:"haproxyConfig"`

    // ValidationTests contains embedded validation test definitions.
    // These tests are executed during admission webhook validation and
    // via the "controller validate" CLI command.
    // +optional
    ValidationTests []ValidationTest `json:"validationTests,omitempty"`
}

// SecretReference references a Secret by name and optional namespace.
type SecretReference struct {
    // Name is the name of the Secret.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    Name string `json:"name"`

    // Namespace is the namespace of the Secret.
    // If empty, defaults to the same namespace as the HAProxyTemplateConfig.
    // +optional
    Namespace string `json:"namespace,omitempty"`
}

// ValidationTest defines a validation test with fixtures and assertions.
type ValidationTest struct {
    // Name is a unique identifier for this test.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinLength=1
    Name string `json:"name"`

    // Description explains what this test validates.
    // +optional
    Description string `json:"description,omitempty"`

    // Fixtures defines the Kubernetes resources to use for this test.
    // Keys are resource type names (matching WatchedResources keys).
    // Values are arrays of resources in unstructured format.
    // +kubebuilder:validation:Required
    Fixtures map[string][]unstructured.Unstructured `json:"fixtures"`

    // Assertions defines the validation checks to perform.
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:MinItems=1
    Assertions []ValidationAssertion `json:"assertions"`
}

// ValidationAssertion defines a single validation check.
type ValidationAssertion struct {
    // Type is the assertion type.
    // Supported types:
    //   - haproxy_valid: Validates that generated HAProxy config is syntactically valid
    //   - contains: Checks if target contains pattern (regex)
    //   - not_contains: Checks if target does not contain pattern (regex)
    //   - equals: Checks if target equals expected value
    //   - jsonpath: Evaluates JSONPath expression against target
    // +kubebuilder:validation:Required
    // +kubebuilder:validation:Enum=haproxy_valid;contains;not_contains;equals;jsonpath
    Type string `json:"type"`

    // Description explains what this assertion validates.
    // +optional
    Description string `json:"description,omitempty"`

    // Target specifies what to validate.
    // For haproxy_valid: not used
    // For contains/not_contains/equals: "haproxy_config", "maps.<name>", "files.<name>", "sslCertificates.<name>"
    // For jsonpath: the resource to query (e.g., "haproxy_config")
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

// +kubebuilder:object:root=true

// HAProxyTemplateConfigList contains a list of HAProxyTemplateConfig.
type HAProxyTemplateConfigList struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ListMeta `json:"metadata,omitempty"`
    Items           []HAProxyTemplateConfig `json:"items"`
}

// Remaining types (PodSelector, ControllerConfig, etc.) are copied from pkg/core/config/types.go
// with appropriate kubebuilder validation markers added.
```

### CRD Generation

Use `controller-gen` to generate CRD YAML:

```bash
# Install controller-gen
go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

# Generate CRD manifests
controller-gen crd:crdVersions=v1 \
    paths=./pkg/apis/haproxytemplate/v1alpha1/... \
    output:crd:dir=./charts/haproxy-template-ic/crds/
```

### Helm Chart Integration

Place generated CRD in `charts/haproxy-template-ic/crds/`:

```
charts/haproxy-template-ic/
├── crds/
│   └── haproxy-template-ic.github.io_haproxytemplateconfigs.yaml
├── templates/
│   ├── deployment.yaml
│   ├── serviceaccount.yaml
│   ├── rbac.yaml
│   └── ...
└── values.yaml
```

**Helm CRD Handling:**
- CRDs in `crds/` directory are installed before other chart resources
- CRDs are **not templated** (no Helm variable substitution)
- CRDs **cannot be upgraded** via `helm upgrade` (Helm limitation)
- CRDs **cannot be deleted** via `helm uninstall` (prevents data loss)
- To update CRD schema: `kubectl apply -f charts/haproxy-template-ic/crds/`

### RBAC Requirements

The controller needs additional permissions to watch CRDs:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: haproxy-template-ic
rules:
  # CRD permissions
  - apiGroups: ["haproxy-template-ic.github.io"]
    resources: ["haproxytemplateconfigs"]
    verbs: ["get", "list", "watch"]

  # CRD status updates
  - apiGroups: ["haproxy-template-ic.github.io"]
    resources: ["haproxytemplateconfigs/status"]
    verbs: ["update", "patch"]

  # Secret permissions (for credentials)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "watch"]

  # Existing watched resource permissions...
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses"]
    verbs: ["get", "list", "watch"]
  # ...
```

## Phase 2: Credentials Management

### Design Decision

**Keep credentials in Kubernetes Secret** (not in CRD) for security:
- Secrets are encrypted at rest (in clusters with encryption enabled)
- CRDs are typically not encrypted
- Credentials should never be in version control
- Follows Kubernetes security best practices

### Secret Structure

The Secret referenced by `credentialsSecretRef` must contain:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
  namespace: default
type: Opaque
data:
  # Base64-encoded credentials
  dataplane_username: YWRtaW4=           # admin
  dataplane_password: cGFzc3dvcmQ=       # password
  validation_username: dmFsaWRhdG9y=     # validator
  validation_password: dmFscGFzcw==      # valpass
```

### Credentials Watcher

The controller must watch the referenced Secret for changes:

1. **On startup**: Load credentials from Secret referenced in config
2. **On config change**: If `credentialsSecretRef` changes, reload credentials
3. **On Secret change**: If Secret content changes, reload credentials

**Implementation in `pkg/controller/credentialsloader/`:**

```go
package credentialsloader

type Component struct {
    eventBus   *events.EventBus
    client     kubernetes.Interface
    secretRef  *v1alpha1.SecretReference
    namespace  string
    logger     *slog.Logger
}

func (c *Component) Start(ctx context.Context) error {
    // Subscribe to config validated events
    eventChan := c.eventBus.Subscribe(50)

    // Create Secret informer
    informer := c.createSecretInformer()
    go informer.Run(ctx.Done())

    for {
        select {
        case event := <-eventChan:
            switch e := event.(type) {
            case *events.ConfigValidatedEvent:
                // Config changed - check if credentials ref changed
                if c.secretRefChanged(e.Config.CredentialsSecretRef) {
                    c.secretRef = &e.Config.CredentialsSecretRef
                    c.reloadCredentials()
                }
            }
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

func (c *Component) reloadCredentials() {
    // Load new credentials from Secret
    secret, err := c.client.CoreV1().Secrets(c.getSecretNamespace()).
        Get(context.TODO(), c.secretRef.Name, metav1.GetOptions{})
    if err != nil {
        c.eventBus.Publish(&events.CredentialsLoadFailedEvent{Error: err})
        return
    }

    // Parse credentials
    creds, err := parseCredentials(secret.Data)
    if err != nil {
        c.eventBus.Publish(&events.CredentialsLoadFailedEvent{Error: err})
        return
    }

    // Publish credentials loaded event
    c.eventBus.Publish(&events.CredentialsLoadedEvent{
        Credentials: creds,
    })
}
```

### Credentials Reload Flow

```
Secret Updated
    ↓
Secret Informer Event
    ↓
Credentials Reloaded
    ↓
CredentialsLoadedEvent
    ↓
Dataplane Clients Reconnect with New Credentials
```

## Phase 3: Config Watcher Updates

### Current Implementation

Location: `pkg/controller/configloader/`

Currently watches a ConfigMap and publishes `ConfigParsedEvent`.

### Required Changes

1. **Watch HAProxyTemplateConfig CRD instead of ConfigMap**
2. **Parse CRD spec into internal config types**
3. **Maintain same event flow** (`ConfigParsedEvent` → validators → `ConfigValidatedEvent`)

### Implementation

```go
package configloader

import (
    "haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
    "haproxy-template-ic/pkg/controller/events"
    haproxyversioned "haproxy-template-ic/pkg/generated/clientset/versioned"
)

type Component struct {
    eventBus      *events.EventBus
    haproxyClient haproxyversioned.Interface
    namespace     string
    configName    string
    logger        *slog.Logger
}

func (c *Component) Start(ctx context.Context) error {
    // Create informer for HAProxyTemplateConfig
    informerFactory := haproxyinformers.NewSharedInformerFactoryWithOptions(
        c.haproxyClient,
        0,
        haproxyinformers.WithNamespace(c.namespace),
    )

    informer := informerFactory.Haproxytemplate().V1alpha1().
        HAProxyTemplateConfigs().Informer()

    informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: func(obj interface{}) {
            c.handleConfigChange(obj)
        },
        UpdateFunc: func(oldObj, newObj interface{}) {
            c.handleConfigChange(newObj)
        },
        DeleteFunc: func(obj interface{}) {
            c.logger.Error("HAProxyTemplateConfig deleted - controller will crash",
                "name", c.configName)
            // Let controller crash - invalid state
        },
    })

    informerFactory.Start(ctx.Done())
    informerFactory.WaitForCacheSync(ctx.Done())

    <-ctx.Done()
    return ctx.Err()
}

func (c *Component) handleConfigChange(obj interface{}) {
    config, ok := obj.(*v1alpha1.HAProxyTemplateConfig)
    if !ok {
        c.logger.Error("unexpected object type")
        return
    }

    // Only handle our specific config
    if config.Name != c.configName {
        return
    }

    c.logger.Info("HAProxyTemplateConfig changed",
        "name", config.Name,
        "generation", config.Generation)

    // Convert CRD spec to internal config types
    internalConfig := c.convertToInternalConfig(config.Spec)

    // Publish config parsed event (same as before)
    c.eventBus.Publish(&events.ConfigParsedEvent{
        Config:  internalConfig,
        Version: fmt.Sprintf("gen-%d", config.Generation),
    })
}
```

### Config Resolution

The controller needs to know:
1. **Which namespace** to watch for HAProxyTemplateConfig
2. **Which config resource** to watch (by name)

**Options:**

**Option A: Environment variables (current pattern)**
```bash
CONTROLLER_NAMESPACE=default
CONFIG_NAME=haproxy-config
```

**Option B: CLI flags**
```bash
controller --namespace=default --config-name=haproxy-config
```

**Recommendation**: Keep environment variables for consistency with existing pattern.

### Staged Startup Integration

The existing staged startup already handles config loading:

```
Stage 1: Config Management
  - ConfigWatcher (now watches CRD)
  - ConfigValidator
  - EventBus.Start()

Stage 2: Wait for Valid Config
  - Block until ConfigValidatedEvent
  - Publish ControllerStartedEvent

Stage 3-5: Rest of startup...
```

No changes needed to staged startup flow - just swap ConfigMap watcher for CRD watcher.

## Phase 4: Validation Subcommand

### CLI Interface

Add `validate` subcommand to existing controller binary:

```bash
# Validate config file before applying
controller validate --config haproxytemplate-config.yaml

# Validate config already in cluster
controller validate --name haproxy-config --namespace default

# Run specific test
controller validate --config config.yaml --test test_basic_ingress

# Output formats
controller validate --config config.yaml --output json
controller validate --config config.yaml --output yaml
controller validate --config config.yaml --output summary  # default
```

### Implementation

Location: `cmd/controller/validate.go`

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "haproxy-template-ic/pkg/controller/testrunner"
)

func newValidateCommand() *cobra.Command {
    var (
        configFile string
        configName string
        namespace  string
        testName   string
        outputFormat string
    )

    cmd := &cobra.Command{
        Use:   "validate",
        Short: "Validate HAProxyTemplateConfig and run embedded tests",
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := context.Background()

            var config *v1alpha1.HAProxyTemplateConfig
            var err error

            if configFile != "" {
                // Load from file
                config, err = loadConfigFromFile(configFile)
            } else if configName != "" {
                // Load from cluster
                config, err = loadConfigFromCluster(configName, namespace)
            } else {
                return fmt.Errorf("either --config or --name must be specified")
            }

            if err != nil {
                return fmt.Errorf("failed to load config: %w", err)
            }

            // Run validation tests
            runner := testrunner.New(config, testrunner.Options{
                TestName: testName,
                Logger:   logger,
            })

            results, err := runner.RunTests(ctx)
            if err != nil {
                return fmt.Errorf("failed to run tests: %w", err)
            }

            // Output results
            if err := outputResults(results, outputFormat); err != nil {
                return err
            }

            // Exit code: 0 if all tests passed, 1 otherwise
            if !results.AllPassed() {
                os.Exit(1)
            }

            return nil
        },
    }

    cmd.Flags().StringVar(&configFile, "config", "", "Path to HAProxyTemplateConfig YAML file")
    cmd.Flags().StringVar(&configName, "name", "", "Name of HAProxyTemplateConfig in cluster")
    cmd.Flags().StringVar(&namespace, "namespace", "default", "Namespace of HAProxyTemplateConfig")
    cmd.Flags().StringVar(&testName, "test", "", "Run specific test (default: all)")
    cmd.Flags().StringVar(&outputFormat, "output", "summary", "Output format: summary, json, yaml")

    return cmd
}
```

### Test Runner Implementation

Location: `pkg/controller/testrunner/runner.go`

Reuses DryRunValidator pattern:

```go
package testrunner

import (
    "context"
    "fmt"

    "haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
    "haproxy-template-ic/pkg/controller/resourcestore"
    "haproxy-template-ic/pkg/dataplane"
    "haproxy-template-ic/pkg/templating"
)

type Runner struct {
    config  *v1alpha1.HAProxyTemplateConfig
    engine  *templating.TemplateEngine
    validator *dataplane.Validator
    options Options
}

type Options struct {
    TestName string
    Logger   *slog.Logger
}

type TestResults struct {
    TotalTests   int
    PassedTests  int
    FailedTests  int
    TestResults  []TestResult
}

type TestResult struct {
    TestName    string
    Description string
    Passed      bool
    Duration    time.Duration
    Assertions  []AssertionResult
}

type AssertionResult struct {
    Type        string
    Description string
    Passed      bool
    Error       string
}

func (r *Runner) RunTests(ctx context.Context) (*TestResults, error) {
    results := &TestResults{
        TotalTests: len(r.config.Spec.ValidationTests),
    }

    for _, test := range r.config.Spec.ValidationTests {
        // Skip if specific test requested and this isn't it
        if r.options.TestName != "" && test.Name != r.options.TestName {
            continue
        }

        result := r.runSingleTest(ctx, test)
        results.TestResults = append(results.TestResults, result)

        if result.Passed {
            results.PassedTests++
        } else {
            results.FailedTests++
        }
    }

    return results, nil
}

func (r *Runner) runSingleTest(ctx context.Context, test v1alpha1.ValidationTest) TestResult {
    startTime := time.Now()

    result := TestResult{
        TestName:    test.Name,
        Description: test.Description,
        Passed:      true,
    }

    // 1. Create stores from fixtures
    stores := r.createStoresFromFixtures(test.Fixtures)

    // 2. Build template context
    templateContext := r.buildTemplateContext(stores)

    // 3. Render templates
    haproxyConfig, err := r.engine.Render("haproxy.cfg", templateContext)
    if err != nil {
        result.Passed = false
        result.Assertions = append(result.Assertions, AssertionResult{
            Type:        "rendering",
            Description: "Template rendering failed",
            Passed:      false,
            Error:       dataplane.SimplifyRenderingError(err),
        })
        result.Duration = time.Since(startTime)
        return result
    }

    // 4. Run assertions
    for _, assertion := range test.Assertions {
        assertionResult := r.runAssertion(ctx, assertion, haproxyConfig, templateContext)
        result.Assertions = append(result.Assertions, assertionResult)

        if !assertionResult.Passed {
            result.Passed = false
        }
    }

    result.Duration = time.Since(startTime)
    return result
}

func (r *Runner) runAssertion(
    ctx context.Context,
    assertion v1alpha1.ValidationAssertion,
    haproxyConfig string,
    templateContext map[string]interface{},
) AssertionResult {
    result := AssertionResult{
        Type:        assertion.Type,
        Description: assertion.Description,
        Passed:      true,
    }

    switch assertion.Type {
    case "haproxy_valid":
        // Validate HAProxy config syntax
        _, err := r.validator.ValidateConfig(ctx, haproxyConfig, nil, 0)
        if err != nil {
            result.Passed = false
            result.Error = dataplane.SimplifyValidationError(err)
        }

    case "contains":
        // Check if target contains pattern
        target := r.resolveTarget(assertion.Target, haproxyConfig, templateContext)
        matched, err := regexp.MatchString(assertion.Pattern, target)
        if err != nil || !matched {
            result.Passed = false
            result.Error = fmt.Sprintf("pattern '%s' not found in %s", assertion.Pattern, assertion.Target)
        }

    case "not_contains":
        // Check if target does NOT contain pattern
        target := r.resolveTarget(assertion.Target, haproxyConfig, templateContext)
        matched, err := regexp.MatchString(assertion.Pattern, target)
        if err != nil {
            result.Passed = false
            result.Error = fmt.Sprintf("regex error: %v", err)
        } else if matched {
            result.Passed = false
            result.Error = fmt.Sprintf("pattern '%s' unexpectedly found in %s", assertion.Pattern, assertion.Target)
        }

    case "equals":
        // Check if target equals expected
        target := r.resolveTarget(assertion.Target, haproxyConfig, templateContext)
        if target != assertion.Expected {
            result.Passed = false
            result.Error = fmt.Sprintf("expected '%s', got '%s'", assertion.Expected, target)
        }

    case "jsonpath":
        // Evaluate JSONPath expression
        value, err := evaluateJSONPath(assertion.JSONPath, templateContext)
        if err != nil {
            result.Passed = false
            result.Error = fmt.Sprintf("jsonpath error: %v", err)
        }
        // Additional validation based on expected value...
    }

    return result
}
```

### Output Formats

**Summary (default):**
```
Validating HAProxyTemplateConfig: haproxy-config

✓ test_basic_ingress (1.2s)
  ✓ Generated config must pass HAProxy validation
  ✓ Config must include frontend for example.com
  ✓ Map file must include domain mapping

✗ test_ssl_config (0.8s)
  ✓ Generated config must pass HAProxy validation
  ✗ Config must include SSL bind options
    Error: pattern 'ssl-min-ver TLSv1.2' not found in haproxy_config

Tests: 1 passed, 1 failed, 2 total
Time: 2.0s
```

**JSON:**
```json
{
  "totalTests": 2,
  "passedTests": 1,
  "failedTests": 1,
  "testResults": [
    {
      "testName": "test_basic_ingress",
      "description": "Validate that a basic ingress generates valid HAProxy config",
      "passed": true,
      "duration": "1.2s",
      "assertions": [...]
    },
    ...
  ]
}
```

## Phase 5: Validating Webhook

### Webhook Configuration

Location: `charts/haproxy-template-ic/templates/validatingwebhook.yaml`

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: haproxy-template-ic-webhook
webhooks:
  - name: validate.haproxytemplateconfig.haproxy-template-ic.github.io
    clientConfig:
      service:
        name: haproxy-template-ic-webhook
        namespace: {{ .Release.Namespace }}
        path: /validate-haproxytemplateconfig
      caBundle: {{ .Values.webhook.caBundle }}

    rules:
      - apiGroups: ["haproxy-template-ic.github.io"]
        apiVersions: ["v1alpha1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["haproxytemplateconfigs"]
        scope: "Namespaced"

    # Fail-open: prevent deadlock when controller is down
    failurePolicy: Ignore

    # Only validate resources with specific labels (namespace isolation)
    objectSelector:
      matchExpressions:
        - key: app.kubernetes.io/name
          operator: In
          values:
            - haproxy-template-ic

    sideEffects: None
    admissionReviewVersions: ["v1"]
    timeoutSeconds: 10
```

### Webhook Server Implementation

Location: `pkg/controller/webhook/server.go`

```go
package webhook

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"

    admissionv1 "k8s.io/api/admission/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    "haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
    "haproxy-template-ic/pkg/controller/testrunner"
)

type Server struct {
    testRunner *testrunner.Runner
    logger     *slog.Logger
}

func (s *Server) Start(ctx context.Context, addr string) error {
    mux := http.NewServeMux()
    mux.HandleFunc("/validate-haproxytemplateconfig", s.handleValidation)
    mux.HandleFunc("/healthz", s.handleHealthz)

    server := &http.Server{
        Addr:    addr,
        Handler: mux,
    }

    go func() {
        <-ctx.Done()
        server.Shutdown(context.Background())
    }()

    s.logger.Info("webhook server starting", "addr", addr)
    return server.ListenAndServeTLS("/certs/tls.crt", "/certs/tls.key")
}

func (s *Server) handleValidation(w http.ResponseWriter, r *http.Request) {
    var admissionReview admissionv1.AdmissionReview
    if err := json.NewDecoder(r.Body).Decode(&admissionReview); err != nil {
        s.logger.Error("failed to decode admission review", "error", err)
        http.Error(w, "invalid admission review", http.StatusBadRequest)
        return
    }

    response := s.validateConfig(admissionReview.Request)

    admissionReview.Response = response
    admissionReview.Response.UID = admissionReview.Request.UID

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(admissionReview)
}

func (s *Server) validateConfig(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
    // Parse config from request
    var config v1alpha1.HAProxyTemplateConfig
    if err := json.Unmarshal(req.Object.Raw, &config); err != nil {
        return &admissionv1.AdmissionResponse{
            Allowed: false,
            Result: &metav1.Status{
                Message: fmt.Sprintf("failed to parse config: %v", err),
            },
        }
    }

    // Run embedded validation tests
    ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
    defer cancel()

    runner := testrunner.New(&config, testrunner.Options{
        Logger: s.logger,
    })

    results, err := runner.RunTests(ctx)
    if err != nil {
        // Fail-open: allow config if validation errors out
        s.logger.Error("validation test execution failed", "error", err)
        return &admissionv1.AdmissionResponse{
            Allowed: true,
            Warnings: []string{
                fmt.Sprintf("validation tests failed to execute: %v", err),
            },
        }
    }

    // Reject if any tests failed
    if !results.AllPassed() {
        return &admissionv1.AdmissionResponse{
            Allowed: false,
            Result: &metav1.Status{
                Message: s.formatTestFailures(results),
            },
        }
    }

    // All tests passed - allow
    return &admissionv1.AdmissionResponse{
        Allowed: true,
    }
}

func (s *Server) formatTestFailures(results *testrunner.TestResults) string {
    var msg strings.Builder
    msg.WriteString(fmt.Sprintf("Validation failed: %d/%d tests passed\n\n",
        results.PassedTests, results.TotalTests))

    for _, test := range results.TestResults {
        if !test.Passed {
            msg.WriteString(fmt.Sprintf("✗ %s\n", test.TestName))
            for _, assertion := range test.Assertions {
                if !assertion.Passed {
                    msg.WriteString(fmt.Sprintf("  ✗ %s\n", assertion.Description))
                    if assertion.Error != "" {
                        msg.WriteString(fmt.Sprintf("    Error: %s\n", assertion.Error))
                    }
                }
            }
            msg.WriteString("\n")
        }
    }

    return msg.String()
}
```

### Certificate Management

The webhook requires TLS certificates. Options:

1. **cert-manager** (recommended for production)
2. **Manual certificate generation** (development)
3. **Self-signed with CA injection** (simple deployments)

Example with cert-manager:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: haproxy-template-ic-webhook-cert
  namespace: default
spec:
  secretName: haproxy-webhook-certs
  dnsNames:
    - haproxy-template-ic-webhook.default.svc
    - haproxy-template-ic-webhook.default.svc.cluster.local
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer
```

### Webhook Deployment

The webhook runs as part of the controller (not separate pod):

```go
// cmd/controller/main.go
func main() {
    // ... existing startup ...

    // Stage 6: Webhook Server (after controller operational)
    if webhookEnabled {
        webhookServer := webhook.NewServer(testRunner, logger)
        g.Go(func() error {
            return webhookServer.Start(gCtx, ":9443")
        })
    }

    // ... rest of startup ...
}
```

### ObjectSelector for Multi-Controller Support

The webhook only validates configs with matching labels:

```yaml
# Controller A's config
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: controller-a-config
  namespace: shared-namespace
  labels:
    app.kubernetes.io/name: haproxy-template-ic
    app.kubernetes.io/instance: controller-a  # Matches controller A's webhook

# Controller B's config
apiVersion: haproxy-template-ic.github.io/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: controller-b-config
  namespace: shared-namespace
  labels:
    app.kubernetes.io/name: haproxy-template-ic
    app.kubernetes.io/instance: controller-b  # Matches controller B's webhook
```

Each controller's webhook filters by instance label to prevent cross-validation.

## Phase 6: Documentation Updates

### Files to Update

1. **Remove ConfigMap references:**
   - `docs/supported-configuration.md` → Update to CRD syntax
   - `README.md` → Update quick start examples
   - `charts/haproxy-template-ic/README.md` → Update installation docs
   - All example YAML files in `examples/`

2. **Update CLAUDE.md files:**
   - `pkg/core/CLAUDE.md` → Document CRD types instead of ConfigMap parsing
   - `cmd/controller/CLAUDE.md` → Update startup to reference CRD watching
   - `pkg/controller/configloader/CLAUDE.md` → Document CRD watching pattern

3. **New documentation:**
   - `docs/validation-tests.md` → Guide for writing validation tests
   - `docs/cli-reference.md` → Document `controller validate` subcommand

### Example Documentation Structure

**docs/validation-tests.md:**

```markdown
# Writing Validation Tests

Validation tests are embedded in HAProxyTemplateConfig and provide
confidence that template changes work correctly before deployment.

## Test Structure

Each test consists of:
1. **Fixtures**: Kubernetes resources to use for rendering
2. **Assertions**: Checks to validate the rendered output

## Assertion Types

### haproxy_valid

Validates that the generated HAProxy configuration is syntactically valid.

### contains

Checks if the target contains a pattern (regex).

### not_contains

Checks if the target does NOT contain a pattern (regex).

### equals

Checks if the target exactly equals an expected value.

### jsonpath

Evaluates a JSONPath expression against the target.

## Examples

[Comprehensive examples of each assertion type...]

## Best Practices

1. Test critical paths (SSL, routing, backend selection)
2. Use realistic fixtures (not toy examples)
3. Add descriptive test names and descriptions
4. Start simple, add complexity gradually
5. Run `controller validate` before applying changes

## CLI Usage

```bash
# Validate before applying
controller validate --config myconfig.yaml

# Run specific test
controller validate --config myconfig.yaml --test test_ssl_config

# Validate deployed config
controller validate --name haproxy-config --namespace default
```
```

## Implementation Roadmap

### Milestone 1: CRD Infrastructure (Week 1)
- [ ] Create `pkg/apis/haproxytemplate/v1alpha1/` package
- [ ] Define Go types with kubebuilder markers
- [ ] Generate CRD YAML with controller-gen
- [ ] Place CRD in `charts/haproxy-template-ic/crds/`
- [ ] Generate clientset, informers, listers
- [ ] Update RBAC for CRD permissions

### Milestone 2: Config Watcher (Week 1-2)
- [ ] Update `pkg/controller/configloader/` to watch CRD
- [ ] Implement CRD → internal config conversion
- [ ] Add credentials watcher for Secret
- [ ] Test config reloading with CRD changes
- [ ] Integration tests for CRD watching

### Milestone 3: Validation CLI (Week 2)
- [ ] Create `pkg/controller/testrunner/` package
- [ ] Implement test fixture → store conversion
- [ ] Implement assertion evaluation
- [ ] Add `controller validate` subcommand
- [ ] Add output formatters (summary, JSON, YAML)
- [ ] Unit tests for test runner

### Milestone 4: Webhook (Week 3)
- [ ] Create `pkg/controller/webhook/` package
- [ ] Implement webhook server
- [ ] Integrate test runner
- [ ] Add certificate management
- [ ] Deploy webhook as part of controller
- [ ] Test fail-open behavior
- [ ] Test objectSelector filtering

### Milestone 5: Documentation (Week 3-4)
- [ ] Update all ConfigMap → CRD references
- [ ] Write validation test guide
- [ ] Update CLI reference
- [ ] Update Helm chart README
- [ ] Create migration examples
- [ ] Update architecture diagrams

### Milestone 6: Testing & Polish (Week 4)
- [ ] End-to-end tests with CRD
- [ ] Acceptance tests for validation
- [ ] Performance testing (webhook timeout)
- [ ] Error message polish
- [ ] Final documentation review

## Testing Strategy

### Unit Tests

- CRD conversion logic
- Assertion evaluation
- Error simplification
- Fixture store creation

### Integration Tests

- CRD watching and config reload
- Credentials watcher
- Test runner execution
- Webhook admission

### Acceptance Tests

- Full validation flow (CLI + webhook)
- Multi-controller isolation
- Fail-open webhook behavior
- Certificate rotation

## Rollout Plan

Since there are no releases yet, this is a **breaking change** that replaces ConfigMap with CRD:

1. **Merge CRD implementation** to main branch
2. **Update all documentation** to reference CRD (remove ConfigMap)
3. **Update examples** to use CRD syntax
4. **Announce breaking change** in README and release notes
5. **First release** (v0.1.0) with CRD-based configuration

No migration guide needed - anyone using pre-release versions must update to CRD.

## Open Questions

1. **CRD versioning**: Should we plan for v1alpha1 → v1beta1 → v1 graduation path?
2. **Status subresource**: Should we surface validation results in status field?
3. **Webhook high availability**: Multiple replicas need leader election for webhook?
4. **Test isolation**: Should tests run in parallel or sequentially?

## References

- [Kubebuilder Book](https://book.kubebuilder.io/)
- [Kubernetes CRD Best Practices](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/)
- [Helm CRD Handling](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/)
- [Admission Webhook Best Practices](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
