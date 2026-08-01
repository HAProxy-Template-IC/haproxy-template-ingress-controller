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

	// DefaultConfigAdmissionTimeout is the controller-side deadline for
	// HAProxyTemplateConfig admission. The chart pairs it with a 30-second
	// Kubernetes webhook timeout, leaving one second for the response to reach
	// the API server. Config admission is deliberately separate from watched-
	// resource admission: it compiles the prospective template set, performs a
	// strict render, and runs the bounded validationTests admission gate.
	DefaultConfigAdmissionTimeout = 29 * time.Second

	// DefaultResourceAdmissionTimeout bounds watched-resource (for example,
	// Ingress) dry-run admission. The chart pairs it with a 10-second Kubernetes
	// webhook timeout. Keeping this path at nine seconds limits per-request work
	// and preserves fail-closed admission under untrusted routing-resource load.
	DefaultResourceAdmissionTimeout = 9 * time.Second

	// MaximumAdmissionTimeout is the largest safe controller-side deadline.
	// Kubernetes caps ValidatingWebhookConfiguration.timeoutSeconds at 30;
	// keeping one second of response margin makes 29 seconds the hard maximum.
	MaximumAdmissionTimeout = 29 * time.Second

	// unregisteredGVKLabel stands in for the real GVK on the metric emitted
	// when an AdmissionReview arrives for a kind no validator backs. Deliberately
	// not a valid GVK string, so it cannot collide with a real one.
	unregisteredGVKLabel = "<unregistered>"

	// webhookResponseGrace keeps the HTTP server's write deadline beyond the
	// longest validator deadline. The API server's own timeout remains the
	// authoritative outer bound; this only prevents net/http from cutting off a
	// valid response before that bound.
	webhookResponseGrace = 2 * time.Second
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
	Rules []WebhookRule

	// DryRunValidator performs dry-run validation of resources.
	// If nil, validation is skipped (fail-open).
	DryRunValidator DryRunValidator

	// ConfigValidator validates HAProxyTemplateConfig admission requests.
	// If nil, HAProxyTemplateConfig admission is admitted unconditionally
	// (no handler registered → pure server's fail-open path). The chart
	// pairs this with failurePolicy=Ignore so missing controller doesn't
	// break the chicken-and-egg of first install / recovery.
	ConfigValidator ConfigValidatorFunc

	// ResourceAdmissionTimeout bounds watched-resource dry-run validation.
	// Default: 9s. Keep it below the corresponding Kubernetes webhook
	// timeoutSeconds value so the controller can return a structured decision.
	ResourceAdmissionTimeout time.Duration

	// ConfigAdmissionTimeout bounds HAProxyTemplateConfig validation.
	// Default: 29s. A timed-out validation is admitted with a warning because
	// the controller's load gate still enforces the prospective config.
	ConfigAdmissionTimeout time.Duration
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
	if config.ResourceAdmissionTimeout <= 0 {
		config.ResourceAdmissionTimeout = DefaultResourceAdmissionTimeout
	}
	if config.ConfigAdmissionTimeout <= 0 {
		config.ConfigAdmissionTimeout = DefaultConfigAdmissionTimeout
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
		WriteTimeout: c.serverWriteTimeout(),

		OnUnregisteredGVK: c.reportUnregisteredGVK,
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

func (c *Component) serverWriteTimeout() time.Duration {
	return max(
		c.config.ResourceAdmissionTimeout,
		c.config.ConfigAdmissionTimeout,
		timeouts.HTTPServerTimeout,
	) + webhookResponseGrace
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

// registerValidators installs the validator table for all configured webhook
// rules.
//
// This is called automatically during Start() after the server is created.
// It uses RESTMapper to resolve resource names to kinds.
//
// The table is built in full and installed in ONE SetValidators call rather
// than registered kind by kind. Incremental registration can only ever add, so
// it cannot express "this kind is no longer validated" — and it would leave the
// server serving a half-built table for the duration of the loop, during which
// a request for a not-yet-registered kind is admitted unchecked.
func (c *Component) registerValidators() {
	c.logger.Info("Registering validators")
	validators := make(map[string]webhook.ValidationFunc)

	// HAProxyTemplateConfig admission validator. Registered separately
	// from the Rules-driven loop because HAProxyTemplateConfig is the
	// controller's own config, NOT a watched resource — its admission
	// validation runs a different code path (parse CRD + ephemeral
	// render+validate pipeline) than the overlay-based DryRunValidator
	// used for watched resources.
	if c.configValidator != nil {
		c.logger.Debug("Registering HAProxyTemplateConfig validator",
			"gvk", HAProxyTemplateConfigGVK)
		validators[HAProxyTemplateConfigGVK] = c.createConfigValidator()
	}

	// For each webhook rule, register a validator
	for _, rule := range c.config.Rules {
		// Resolve Kind from Resource using RESTMapper
		// The webhook server receives AdmissionRequests with Kind (e.g., "Ingress")
		// but we only have the resources field (e.g., "ingresses")
		// RESTMapper queries the Kubernetes API to get the authoritative mapping
		kind, err := c.resolveKind(
			rule.APIGroup,
			rule.APIVersion,
			rule.Resource,
		)
		if err != nil {
			c.logger.Error("Failed to resolve kind, skipping validator registration",
				"error", err,
				"api_group", rule.APIGroup,
				"api_version", rule.APIVersion,
				"resource", rule.Resource)
			continue
		}

		gvk := c.buildGVK(rule.APIGroup, rule.APIVersion, kind)

		c.logger.Debug("Registering validator",
			"gvk", gvk,
			"kind", kind,
			"resource", rule.Resource)

		validators[gvk] = c.createResourceValidator(gvk)
	}

	c.server.SetValidators(validators)
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
		// would orphan validation work past server cancellation, delaying
		// graceful shutdown. ResourceAdmissionTimeout still bounds each admission
		// individually; watched-resource admission remains fail closed.
		// Fall back to context.Background() if serverCtx hasn't been set
		// yet — happens in unit tests that call this validator without
		// going through Start(); in production Start() always sets
		// serverCtx before RegisterValidator wires this closure up.
		parent := c.serverCtx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, c.config.ResourceAdmissionTimeout)
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

		c.logger.Log(context.Background(), admissionLogLevel(allowed, len(warnings)), "Validation completed",
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

		// Config admission has its own deadline because this path compiles and
		// strictly renders the complete prospective template set before running
		// the bounded validationTests gate. The chart keeps this internal timeout
		// one second below its HAProxyTemplateConfig timeoutSeconds value. Parent
		// is c.serverCtx so iteration shutdown cancels in-flight validations. See
		// createResourceValidator for the serverCtx-nil fallback rationale.
		parent := c.serverCtx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, c.config.ConfigAdmissionTimeout)
		defer cancel()

		allowed, reason, warnings := c.configValidator(
			ctx,
			HAProxyTemplateConfigGVK,
			valCtx.Namespace,
			valCtx.Name,
			valCtx.Object,
			valCtx.Operation,
		)
		if !allowed && ctx.Err() != nil {
			// The chart deliberately gives HAProxyTemplateConfig admission an
			// Ignore failure policy: an overloaded or restarting controller must
			// never make the config object impossible to repair. Convert an
			// internal deadline/cancellation into the same explicit fail-open
			// behaviour, with a warning for the operator. The daemon load gate
			// remains authoritative and will reject an invalid prospective config.
			c.logger.Warn("HAProxyTemplateConfig validation did not complete; admitting (load gate still enforces)",
				"operation", valCtx.Operation,
				"namespace", valCtx.Namespace,
				"name", valCtx.Name,
				"error", ctx.Err())
			allowed = true
			reason = ""
			warnings = append(warnings, fmt.Sprintf(
				"HAProxyTemplateConfig admission validation did not complete: %v — the controller's load gate will still enforce this config",
				ctx.Err(),
			))
		}

		duration := time.Since(start).Seconds()
		if c.metrics != nil {
			resultStr := "allowed"
			if !allowed {
				resultStr = "denied"
			}
			c.metrics.RecordWebhookRequest(HAProxyTemplateConfigGVK, resultStr, duration)
			c.metrics.RecordWebhookValidation(HAProxyTemplateConfigGVK, resultStr)
		}

		c.logger.Log(context.Background(), configAdmissionLogLevel(), "HAProxyTemplateConfig validation completed",
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

// reportUnregisteredGVK records an AdmissionReview the API server routed here
// for a kind no validator backs. It is admitted unchecked — nothing registered
// can judge it — so the only defence is that it is loud: a rule in the
// ValidatingWebhookConfiguration whose validator failed to register (a
// RESTMapper miss, a kind dropped from watchedResources) leaves the gate open
// for that kind, and silence would make it indistinguishable from a clean pass.
func (c *Component) reportUnregisteredGVK(gvk string) {
	c.logger.Warn("Admission request for a kind with no registered validator; admitted unchecked",
		"gvk", gvk)
	if c.metrics != nil {
		// The gvk is read off the AdmissionReview, and the listener does not
		// require client certificates — anything that can reach the Service can
		// invent one. A Prometheus label value keeps its series forever, so the
		// counter carries a fixed sentinel and the real gvk stays in the log
		// line above, where volume is bounded by retention rather than memory.
		c.metrics.RecordWebhookValidation(unregisteredGVKLabel, "unregistered")
	}
}

// admissionLogLevel returns slog.LevelDebug for a clean allow (admitted with
// no warnings) so steady-state successful admissions don't spam the log at
// INFO, and slog.LevelInfo for denials or warning-bearing admissions — the
// cases an operator actually wants to see. Mirrors the commentator's
// "demote to DEBUG when nothing notable happened" rule (see
// pkg/controller/commentator/log_levels.go).
//
// Only for watched resources, which arrive at cluster traffic rates. The
// HAProxyTemplateConfig gate logs unconditionally at INFO — see
// configAdmissionLogLevel.
func admissionLogLevel(allowed bool, warnings int) slog.Level {
	if allowed && warnings == 0 {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// configAdmissionLogLevel is slog.LevelInfo for every HAProxyTemplateConfig
// admission decision, including a clean allow.
//
// Demoting clean allows to DEBUG here made the gate's verdicts indistinguishable
// from it never being consulted: at the default INFO level, "admitted cleanly"
// and "the API server could not reach the webhook, so failurePolicy:Ignore
// admitted it" both appear as no log line at all. That ambiguity is what left
// the intermittent test-chart-upgrade phase-3 failure unattributable (#110).
//
// Costs nothing: this gate fires on the operator's own config objects, not on
// cluster traffic — a handful of decisions per `helm upgrade`.
func configAdmissionLogLevel() slog.Level {
	return slog.LevelInfo
}
