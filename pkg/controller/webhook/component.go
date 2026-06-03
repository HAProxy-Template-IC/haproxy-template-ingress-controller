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

// Package webhook provides the webhook adapter component that bridges
// the pure webhook library to the event-driven controller architecture.
//
// The webhook component manages the lifecycle of admission webhooks including:
//   - HTTPS webhook server
//   - Integration with controller validators
//
// Note: TLS certificates are fetched from Kubernetes Secret via API.
// ValidatingWebhookConfiguration is created by Helm at installation time.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/webhook"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "webhook"

	// DefaultWebhookPort is the default HTTPS port for the webhook server.
	DefaultWebhookPort = 9443

	// DefaultWebhookPath is the default URL path for validation requests.
	DefaultWebhookPath = "/validate"
)

// Component is the webhook adapter component that manages webhook lifecycle.
//
// It coordinates the pure webhook library server with the event-driven controller architecture.
type Component struct {
	// Dependencies
	logger          *slog.Logger
	metrics         MetricsRecorder
	restMapper      meta.RESTMapper
	dryRunValidator DryRunValidator
	configValidator ConfigValidatorFunc

	// Webhook library components
	server *webhook.Server

	// Configuration
	config Config

	// Runtime state
	serverCtx    context.Context
	serverCancel context.CancelFunc

	// listening is closed once the underlying *webhook.Server has bound
	// its listener. The iteration sequencer reads Listening() before
	// flipping the controller's readiness probe so the chart's
	// ValidatingWebhookConfiguration doesn't get routed admission requests
	// while the listener is still pending.
	listening chan struct{}
}

// MetricsRecorder defines the interface for recording webhook metrics.
// This allows the component to work with or without metrics.
type MetricsRecorder interface {
	RecordWebhookRequest(gvk, result string, durationSeconds float64)
	RecordWebhookValidation(gvk, result string)
}

// DryRunValidator defines the synchronous interface the webhook uses to
// validate resources. The implementation in pkg/controller/dryrunvalidator
// is a library, not a lifecycle component.
//
// Warnings are surfaced via AdmissionResponse.Warnings on both allow and
// deny paths so kubectl prints them as "Warning:" lines without blocking
// admission. Pluggable validators (e.g. the SPOA hub running in
// --validate-socket mode) populate this slice from their non-fatal
// diagnostics; the standard render+validate pipeline does not.
type DryRunValidator interface {
	ValidateDirect(ctx context.Context, gvk, namespace, name string, object any, operation string) (allowed bool, reason string, warnings []string)
}

// ConfigValidatorFunc validates a HAProxyTemplateConfig admission request.
// Same signature shape as DryRunValidator.ValidateDirect — kept as a
// function type rather than an interface so test wiring can pass a plain
// closure without declaring a satellite type. Nil means "no validator
// configured" — handler falls back to allow (failurePolicy=Ignore on the
// chart-side ValidatingWebhookConfiguration covers the remaining gap).
type ConfigValidatorFunc func(ctx context.Context, gvk, namespace, name string, object any, operation string) (allowed bool, reason string, warnings []string)

// Config configures the webhook component.
type Config struct {
	// Port is the HTTPS port for the webhook server.
	// Default: 9443
	Port int

	// Path is the URL path for validation requests.
	// Default: "/validate"
	Path string

	// CertDir, when set, is a directory containing tls.crt and tls.key
	// (typically the mounted webhook-cert Secret). The server reads them
	// per-handshake and hot-reloads a rotated certificate without a restart.
	// Takes precedence over CertPEM/KeyPEM; production uses this path.
	CertDir string

	// CertPEM is the PEM-encoded TLS certificate.
	// Used when CertDir is unset (e.g. tests pass certs directly).
	CertPEM []byte

	// KeyPEM is the PEM-encoded TLS private key.
	// Used when CertDir is unset.
	KeyPEM []byte

	// Rules defines which resources the webhook validates.
	// Used for registering validators by GVK.
	Rules []webhook.WebhookRule

	// DryRunValidator performs dry-run validation of resources.
	// If nil, validation is skipped (fail-open).
	DryRunValidator DryRunValidator

	// ConfigValidator validates HAProxyTemplateConfig admission requests.
	// If nil, HAProxyTemplateConfig admission is admitted unconditionally
	// (no handler registered → pure server's fail-open path). The chart
	// pairs this with failurePolicy=Ignore so missing controller doesn't
	// break the chicken-and-egg of first install / recovery.
	ConfigValidator ConfigValidatorFunc
}

// New creates a new webhook component.
//
// Parameters:
//   - logger: Structured logger
//   - config: Component configuration (must include CertPEM and KeyPEM)
//   - restMapper: RESTMapper for resolving resource kinds from GVR
//   - metrics: Optional metrics recorder (can be nil)
//
// Returns:
//   - A new Component instance ready to be started
func New(logger *slog.Logger, config *Config, restMapper meta.RESTMapper, metrics MetricsRecorder) *Component {
	// Apply defaults
	if config.Port == 0 {
		config.Port = DefaultWebhookPort
	}
	if config.Path == "" {
		config.Path = DefaultWebhookPath
	}

	return &Component{
		logger:          logger.With("component", ComponentName),
		config:          *config,
		restMapper:      restMapper,
		metrics:         metrics,
		dryRunValidator: config.DryRunValidator,
		configValidator: config.ConfigValidator,
		listening:       make(chan struct{}),
	}
}

// Listening returns a channel that is closed once the underlying webhook
// server has bound its TLS listener. Until this channel is closed, an
// admission request routed at the controller fails with "connection
// refused", because the listening socket simply doesn't exist yet.
func (c *Component) Listening() <-chan struct{} {
	return c.listening
}

// Start starts the webhook component.
//
// This method:
// 1. Validates TLS certificates from configuration
// 2. Creates and starts the webhook HTTPS server
// 3. Publishes lifecycle events
//
// The server continues running until the context is cancelled.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Info("Starting webhook component",
		"port", c.config.Port,
		"path", c.config.Path)

	// Validate a certificate source is configured.
	if c.config.CertDir == "" {
		if len(c.config.CertPEM) == 0 {
			return errors.New("no webhook TLS certificate configured (set CertDir or CertPEM)")
		}
		if len(c.config.KeyPEM) == 0 {
			return errors.New("tls private key is empty")
		}
		c.logger.Info("Loading webhook TLS certificate from configuration",
			"cert_size", len(c.config.CertPEM), "key_size", len(c.config.KeyPEM))
	} else {
		// The mounted Secret directory is read per-handshake, so a
		// cert-manager rotation is picked up without an iteration restart.
		c.logger.Info("Loading webhook TLS certificate from directory (hot-reload on rotation)",
			"cert_dir", c.config.CertDir)
	}

	server, err := webhook.NewServer(&webhook.ServerConfig{
		Port:         c.config.Port,
		Path:         c.config.Path,
		CertDir:      c.config.CertDir,
		CertPEM:      c.config.CertPEM,
		KeyPEM:       c.config.KeyPEM,
		ReadTimeout:  timeouts.HTTPServerTimeout,
		WriteTimeout: timeouts.HTTPServerTimeout,
	})
	if err != nil {
		return fmt.Errorf("creating webhook server: %w", err)
	}
	c.server = server

	// Register validators
	c.registerValidators()

	// Create server context
	c.serverCtx, c.serverCancel = context.WithCancel(ctx)

	// Start server in goroutine
	serverErrCh := make(chan error, 1)
	go func() {
		if err := c.server.Start(c.serverCtx); err != nil {
			c.logger.Error("Webhook server error", "error", err)
			serverErrCh <- err
		}
	}()

	// Wait for the underlying listener to actually bind before logging
	// "started" or signalling readiness to the iteration sequencer. The
	// pure server signals via Listening() once net.Listen has returned;
	// this prevents the controller from advertising readiness while the
	// API server's first AdmissionReview would still bounce with
	// "connection refused".
	select {
	case <-c.server.Listening():
		close(c.listening)
	case err := <-serverErrCh:
		return fmt.Errorf("webhook server failed before bind: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}

	c.logger.Info("Webhook server started",
		"port", c.config.Port,
		"path", c.config.Path)

	// Wait for shutdown or error. When CertDir is set (production) the
	// certificate is hot-reloaded in-process by the server's GetCertificate
	// callback, so a rotation is served without an iteration restart. The
	// fixed CertPEM/KeyPEM fallback (tests) is only refreshed on restart.
	select {
	case err := <-serverErrCh:
		return fmt.Errorf("webhook server failed: %w", err)
	case <-ctx.Done():
		c.logger.Info("Webhook component shutting down")
		c.serverCancel()
		return nil
	}
}

// RegisterValidator registers a validation function for a specific resource type.
//
// This should be called before Start() to register all validators.
//
// Parameters:
//   - gvk: Group/Version.Kind identifier (e.g., "networking.k8s.io/v1.Ingress", "v1.ConfigMap")
//   - validatorFunc: The validation function to call for this resource type
func (c *Component) RegisterValidator(gvk string, validatorFunc webhook.ValidationFunc) {
	if c.server == nil {
		c.logger.Warn("RegisterValidator called before server created, validator will be registered when server starts")
		return
	}
	c.server.RegisterValidator(gvk, validatorFunc)
	c.logger.Debug("Validator registered", "gvk", gvk)
}

// resolveKind uses RESTMapper to convert GVR (Group/Version/Resource) to Kind.
//
// This queries the Kubernetes API server's discovery information to get the
// authoritative mapping from resource names to kinds.
//
// Parameters:
//   - apiGroup: API group (empty string for core resources)
//   - apiVersion: API version (e.g., "v1", "v1beta1")
//   - resource: Plural resource name (e.g., "ingresses", "services")
//
// Returns:
//   - kind: Singular kind name (e.g., "Ingress", "Service")
//   - error: If resolution fails
func (c *Component) resolveKind(apiGroup, apiVersion, resource string) (string, error) {
	gvr := schema.GroupVersionResource{
		Group:    apiGroup,
		Version:  apiVersion,
		Resource: resource,
	}

	c.logger.Debug("Resolving kind from GVR",
		"group", apiGroup,
		"version", apiVersion,
		"resource", resource)

	gvk, err := c.restMapper.KindFor(gvr)
	if err != nil {
		return "", fmt.Errorf("resolving kind for %v: %w", gvr, err)
	}

	c.logger.Debug("Resolved kind",
		"resource", resource,
		"kind", gvk.Kind)

	return gvk.Kind, nil
}

// registerValidators registers validators for all configured webhook rules.
//
// This is called automatically during Start() after the server is created.
// It uses RESTMapper to resolve resource names to kinds.
func (c *Component) registerValidators() {
	c.logger.Info("Registering validators")

	// HAProxyTemplateConfig admission validator. Registered separately
	// from the Rules-driven loop because HAProxyTemplateConfig is the
	// controller's own config, NOT a watched resource — its admission
	// validation runs a different code path (parse CRD + ephemeral
	// render+validate pipeline) than the overlay-based DryRunValidator
	// used for watched resources.
	if c.configValidator != nil {
		c.logger.Debug("Registering HAProxyTemplateConfig validator",
			"gvk", HAProxyTemplateConfigGVK)
		c.server.RegisterValidator(HAProxyTemplateConfigGVK, c.createConfigValidator())
	}

	// For each webhook rule, register a validator
	for _, rule := range c.config.Rules {
		// Resolve Kind from Resource using RESTMapper
		// The webhook server receives AdmissionRequests with Kind (e.g., "Ingress")
		// but we only have the resources field (e.g., "ingresses")
		// RESTMapper queries the Kubernetes API to get the authoritative mapping
		kind, err := c.resolveKind(
			rule.APIGroups[0],
			rule.APIVersions[0],
			rule.Resources[0],
		)
		if err != nil {
			c.logger.Error("Failed to resolve kind, skipping validator registration",
				"error", err,
				"api_group", rule.APIGroups[0],
				"api_version", rule.APIVersions[0],
				"resource", rule.Resources[0])
			continue
		}

		gvk := c.buildGVK(rule.APIGroups[0], rule.APIVersions[0], kind)

		c.logger.Debug("Registering validator",
			"gvk", gvk,
			"kind", kind,
			"resources", rule.Resources)

		// Create resource validator
		validator := c.createResourceValidator(gvk)
		c.server.RegisterValidator(gvk, validator)
	}
}

// buildGVK constructs a GVK string from API group, version, and kind.
func (c *Component) buildGVK(apiGroup, version, kind string) string {
	if apiGroup == "" {
		// Core API group
		return fmt.Sprintf("%s.%s", version, kind)
	}
	return fmt.Sprintf("%s/%s.%s", apiGroup, version, kind)
}

// createResourceValidator creates a ValidationFunc for a specific GVK.
//
// This validator performs:
// 1. Basic structural validation (metadata checks)
// 2. Dry-run validation via DryRunValidator (render + HAProxy validation).
func (c *Component) createResourceValidator(gvk string) webhook.ValidationFunc {
	return func(valCtx *webhook.ValidationContext) (bool, string, []string, error) {
		start := time.Now()

		c.logger.Debug("Validating resource",
			"gvk", gvk,
			"operation", valCtx.Operation,
			"namespace", valCtx.Namespace,
			"name", valCtx.Name)

		// Basic structural validation runs inline before delegating to ValidateDirect.
		if err := c.validateBasicStructure(valCtx.Object); err != nil {
			c.logger.Info("Basic validation failed",
				"gvk", gvk,
				"namespace", valCtx.Namespace,
				"name", valCtx.Name,
				"error", err)

			duration := time.Since(start).Seconds()
			if c.metrics != nil {
				c.metrics.RecordWebhookRequest(gvk, "denied", duration)
				c.metrics.RecordWebhookValidation(gvk, "denied")
			}

			return false, err.Error(), nil, nil
		}

		// Dry-run validation (synchronous call into dryrunvalidator.ValidateDirect).
		if c.dryRunValidator == nil {
			// Fail-open if no validator configured
			c.logger.Warn("No dry-run validator configured, allowing resource",
				"gvk", gvk,
				"namespace", valCtx.Namespace,
				"name", valCtx.Name)

			duration := time.Since(start).Seconds()
			if c.metrics != nil {
				c.metrics.RecordWebhookRequest(gvk, "allowed", duration)
				c.metrics.RecordWebhookValidation(gvk, "allowed")
			}

			return true, "", nil, nil
		}

		// Derive from c.serverCtx (set in Start()) so iteration shutdown
		// cancels in-flight validations promptly. Using context.Background()
		// would orphan up to 5s of validation work past server cancellation,
		// delaying graceful shutdown. The 5s deadline still bounds each
		// admission individually; failurePolicy=Ignore admits on timeout.
		// Fall back to context.Background() if serverCtx hasn't been set
		// yet — happens in unit tests that call this validator without
		// going through Start(); in production Start() always sets
		// serverCtx before RegisterValidator wires this closure up.
		parent := c.serverCtx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()

		allowed, reason, warnings := c.dryRunValidator.ValidateDirect(
			ctx,
			gvk,
			valCtx.Namespace,
			valCtx.Name,
			valCtx.Object,
			valCtx.Operation,
		)

		// Record metrics
		duration := time.Since(start).Seconds()
		if c.metrics != nil {
			resultStr := "allowed"
			if !allowed {
				resultStr = "denied"
			}
			c.metrics.RecordWebhookRequest(gvk, resultStr, duration)
			c.metrics.RecordWebhookValidation(gvk, resultStr)
		}

		c.logger.Info("Validation completed",
			"gvk", gvk,
			"operation", valCtx.Operation,
			"namespace", valCtx.Namespace,
			"name", valCtx.Name,
			"allowed", allowed,
			"reason", reason,
			"warnings", len(warnings),
			"duration_ms", time.Since(start).Milliseconds())

		return allowed, reason, warnings, nil
	}
}

// createConfigValidator returns the ValidationFunc that handles
// HAProxyTemplateConfig admission. Mirrors createResourceValidator's shape
// (basic structure check, metric recording, deadline guard) but dispatches
// to the dedicated configValidator instead of the overlay-based DryRunValidator.
func (c *Component) createConfigValidator() webhook.ValidationFunc {
	return func(valCtx *webhook.ValidationContext) (bool, string, []string, error) {
		start := time.Now()

		c.logger.Debug("Validating HAProxyTemplateConfig admission",
			"gvk", HAProxyTemplateConfigGVK,
			"operation", valCtx.Operation,
			"namespace", valCtx.Namespace,
			"name", valCtx.Name)

		if err := c.validateBasicStructure(valCtx.Object); err != nil {
			duration := time.Since(start).Seconds()
			if c.metrics != nil {
				c.metrics.RecordWebhookRequest(HAProxyTemplateConfigGVK, "denied", duration)
				c.metrics.RecordWebhookValidation(HAProxyTemplateConfigGVK, "denied")
			}
			return false, err.Error(), nil, nil
		}

		// Hard 5 s internal deadline mirrors createResourceValidator. Kept
		// shorter than the chart-side timeoutSeconds (also 5 s, but
		// failurePolicy=Ignore there so a timeout admits anyway). Parent
		// is c.serverCtx so iteration shutdown cancels in-flight validations.
		// See createResourceValidator for the serverCtx-nil fallback rationale.
		parent := c.serverCtx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()

		allowed, reason, warnings := c.configValidator(
			ctx,
			HAProxyTemplateConfigGVK,
			valCtx.Namespace,
			valCtx.Name,
			valCtx.Object,
			valCtx.Operation,
		)

		duration := time.Since(start).Seconds()
		if c.metrics != nil {
			resultStr := "allowed"
			if !allowed {
				resultStr = "denied"
			}
			c.metrics.RecordWebhookRequest(HAProxyTemplateConfigGVK, resultStr, duration)
			c.metrics.RecordWebhookValidation(HAProxyTemplateConfigGVK, resultStr)
		}

		c.logger.Info("HAProxyTemplateConfig validation completed",
			"operation", valCtx.Operation,
			"namespace", valCtx.Namespace,
			"name", valCtx.Name,
			"allowed", allowed,
			"reason", reason,
			"duration_ms", time.Since(start).Milliseconds())

		return allowed, reason, warnings, nil
	}
}

// validateBasicStructure performs basic structural validation on a Kubernetes resource.
//
// The check is intentionally inlined here rather than living in a separate
// component — it's trivial enough that a dedicated subscriber + event hop
// would add latency on the admission path for no benefit.
//
// Checks:
//   - Object is a valid unstructured resource
//   - Metadata.name or metadata.generateName exists
func (c *Component) validateBasicStructure(object any) error {
	obj, ok := object.(*unstructured.Unstructured)
	if !ok {
		return fmt.Errorf("invalid object type: %T", object)
	}

	name := obj.GetName()
	generateName := obj.GetGenerateName()

	if name == "" && generateName == "" {
		return errors.New("metadata.name or metadata.generateName is required")
	}

	return nil
}
