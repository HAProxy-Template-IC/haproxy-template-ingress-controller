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
	"fmt"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"haproxy-template-ic/pkg/controller/events"
	busevents "haproxy-template-ic/pkg/events"
	"haproxy-template-ic/pkg/webhook"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "webhook"

	// DefaultWebhookPort is the default HTTPS port for the webhook server.
	DefaultWebhookPort = 9443

	// DefaultWebhookPath is the default URL path for validation requests.
	DefaultWebhookPath = "/validate"

	// EventBufferSize is the size of the event subscription buffer.
	EventBufferSize = 50
)

// Component is the webhook adapter component that manages webhook lifecycle.
//
// It coordinates the pure webhook library server with the event-driven controller architecture.
type Component struct {
	// Dependencies
	eventBus   *busevents.EventBus
	logger     *slog.Logger
	metrics    MetricsRecorder
	restMapper meta.RESTMapper

	// Webhook library components
	server *webhook.Server

	// Configuration
	config Config

	// Runtime state
	serverCtx    context.Context
	serverCancel context.CancelFunc
}

// MetricsRecorder defines the interface for recording webhook metrics.
// This allows the component to work with or without metrics.
type MetricsRecorder interface {
	RecordWebhookRequest(gvk, result string, durationSeconds float64)
	RecordWebhookValidation(gvk, result string)
}

// Config configures the webhook component.
type Config struct {
	// Port is the HTTPS port for the webhook server.
	// Default: 9443
	Port int

	// Path is the URL path for validation requests.
	// Default: "/validate"
	Path string

	// CertPEM is the PEM-encoded TLS certificate.
	// Fetched from Kubernetes Secret via API.
	CertPEM []byte

	// KeyPEM is the PEM-encoded TLS private key.
	// Fetched from Kubernetes Secret via API.
	KeyPEM []byte

	// Rules defines which resources the webhook validates.
	// Used for registering validators by GVK.
	Rules []webhook.WebhookRule
}

// New creates a new webhook component.
//
// Parameters:
//   - eventBus: EventBus for publishing webhook events
//   - logger: Structured logger
//   - config: Component configuration (must include CertPEM and KeyPEM)
//   - restMapper: RESTMapper for resolving resource kinds from GVR
//   - metrics: Optional metrics recorder (can be nil)
//
// Returns:
//   - A new Component instance ready to be started
func New(eventBus *busevents.EventBus, logger *slog.Logger, config *Config, restMapper meta.RESTMapper, metrics MetricsRecorder) *Component {
	// Apply defaults
	if config.Port == 0 {
		config.Port = DefaultWebhookPort
	}
	if config.Path == "" {
		config.Path = DefaultWebhookPath
	}

	return &Component{
		eventBus:   eventBus,
		logger:     logger.With("component", ComponentName),
		config:     *config,
		restMapper: restMapper,
		metrics:    metrics,
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
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
		"path", c.config.Path,
		"cert_size", len(c.config.CertPEM),
		"key_size", len(c.config.KeyPEM))

	// Validate certificates are present
	if len(c.config.CertPEM) == 0 {
		return fmt.Errorf("tls certificate is empty")
	}
	if len(c.config.KeyPEM) == 0 {
		return fmt.Errorf("tls private key is empty")
	}

	c.logger.Info("TLS certificates validated successfully",
		"cert_size", len(c.config.CertPEM),
		"key_size", len(c.config.KeyPEM))

	// Create webhook server with certificates from configuration
	c.server = webhook.NewServer(&webhook.ServerConfig{
		Port:         c.config.Port,
		Path:         c.config.Path,
		CertPEM:      c.config.CertPEM,
		KeyPEM:       c.config.KeyPEM,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	})

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

	c.logger.Info("Webhook server started",
		"port", c.config.Port,
		"path", c.config.Path)

	// Wait for shutdown or error
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
		return "", fmt.Errorf("failed to resolve kind for %v: %w", gvr, err)
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
// This validator uses the scatter-gather pattern to coordinate multiple
// validators (BasicValidator, DryRunValidator) via the EventBus.
//
// All validators must allow for the resource to be admitted (AND logic).
func (c *Component) createResourceValidator(gvk string) webhook.ValidationFunc {
	return func(valCtx *webhook.ValidationContext) (bool, string, error) {
		start := time.Now()

		c.logger.Debug("Validating resource",
			"gvk", gvk,
			"operation", valCtx.Operation,
			"namespace", valCtx.Namespace,
			"name", valCtx.Name)

		// Create validation request with actual operation from context
		req := events.NewWebhookValidationRequest(
			gvk,
			valCtx.Namespace,
			valCtx.Name,
			valCtx.Object,
			valCtx.Operation,
		)

		// Use scatter-gather to collect validation results from all validators
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		result, err := c.eventBus.Request(ctx, req, busevents.RequestOptions{
			Timeout:            5 * time.Second,
			ExpectedResponders: []string{"basic", "dryrun"},
		})

		// Handle timeout or error
		if err != nil {
			c.logger.Error("Validation request failed",
				"gvk", gvk,
				"operation", valCtx.Operation,
				"namespace", valCtx.Namespace,
				"name", valCtx.Name,
				"error", err)

			duration := time.Since(start).Seconds()
			if c.metrics != nil {
				c.metrics.RecordWebhookRequest(gvk, "error", duration)
				c.metrics.RecordWebhookValidation(gvk, "error")
			}

			return false, "validation timeout or internal error", nil
		}

		// Aggregate responses: ALL must allow for overall allow
		allowed, reason := c.aggregateResponses(result.Responses)

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
			"duration_ms", time.Since(start).Milliseconds())

		return allowed, reason, nil
	}
}

// aggregateResponses combines validation responses using AND logic.
//
// ANY deny = overall deny, ALL allow = overall allow.
//
// Returns:
//   - allowed: true if all validators allowed
//   - reason: combined denial reasons from all denying validators
func (c *Component) aggregateResponses(responses []busevents.Response) (allowed bool, reason string) {
	var denialReasons []string

	for _, resp := range responses {
		if valResp, ok := resp.(*events.WebhookValidationResponse); ok {
			if !valResp.Allowed {
				// Validator denied - collect reason
				denialReasons = append(denialReasons, fmt.Sprintf("%s: %s", valResp.ValidatorID, valResp.Reason))
			}
		}
	}

	// If any validator denied, return denied with combined reasons
	if len(denialReasons) > 0 {
		return false, fmt.Sprintf("validation failed: %v", denialReasons)
	}

	// All validators allowed
	return true, ""
}
