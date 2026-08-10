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

	// ResourceAdmissionTimeout bounds watched-resource dry-run validation.
	// Default: 9s. Keep it below the corresponding Kubernetes webhook
	// timeoutSeconds value so the controller can return a structured decision.
	ResourceAdmissionTimeout time.Duration

	// Server, when set, is an already-bound webhook server this component
	// ADOPTS instead of creating and owning one.
	//
	// The controller rebuilds every component on each config change, so a
	// component-owned listener closes and re-binds on every reinitialization —
	// a multi-second window in which the API server dials a dead port and, under
	// failurePolicy=Ignore, admits the very config change that opened it (#110).
	// Hoisting the listener to process lifetime and swapping only the validator
	// table closes that window: an iteration installs its table and, on
	// teardown, leaves the previous one serving until the next install.
	//
	// The adopting component MUST NOT shut this server down — it belongs to the
	// caller, and stopping it is precisely the hole being fixed.
	Server *webhook.Server
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

	return &Component{
		logger:          logger.With("component", ComponentName),
		config:          *config,
		restMapper:      restMapper,
		metrics:         metrics,
		dryRunValidator: config.DryRunValidator,
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

	// Adoption is decided BEFORE the certificate check: the shared server was
	// built with its own TLS material, so an adopting component carries none and
	// would fail the check below — silently, because Start() returning early
	// leaves Listening() open and every caller waiting on it blocks forever.
	if c.config.Server != nil {
		return c.startAdopted(ctx)
	}

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

// startAdopted runs the component against a caller-owned, already-bound server.
//
// It installs this iteration's validator table and then does nothing until the
// iteration ends — deliberately: the listener, its port and its TLS material
// belong to the caller and outlive this component. Returning without stopping
// the server is what keeps the admission gate answerable across a config
// reinitialization; the previous table keeps serving until the next iteration
// installs its own, so a request in the gap gets a verdict from slightly older
// wiring rather than the silent admit an unreachable webhook produces.
func (c *Component) startAdopted(ctx context.Context) error {
	c.server = c.config.Server

	// Point the unregistered-GVK reporter at THIS iteration's metrics recorder.
	// The server outlives the recorder that was current when it was built.
	c.server.SetOnUnregisteredGVK(c.reportUnregisteredGVK)

	c.registerValidators()

	// The caller bound the listener before handing it over, so readiness is
	// already satisfied — the sequencer must not block waiting for a bind that
	// happened in an earlier iteration.
	close(c.listening)

	c.logger.Info("Webhook validators installed on the persistent server",
		"port", c.config.Port,
		"path", c.config.Path)

	<-ctx.Done()
	c.logger.Info("Webhook component shutting down; leaving the shared listener bound")
	return nil
}

func (c *Component) serverWriteTimeout() time.Duration {
	return max(
		c.config.ResourceAdmissionTimeout,
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
