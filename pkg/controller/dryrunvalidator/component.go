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

// Package dryrunvalidator implements the DryRunValidator that performs
// dry-run reconciliation for webhook validation.
//
// The validator is called synchronously by pkg/controller/webhook via
// ValidateDirect. It creates an overlay store simulating the admission
// request's hypothetical state, delegates to ProposalValidator.ValidateSync
// for rendering and validation, and runs validation tests if configured.
package dryrunvalidator

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	// ComponentName identifies this validator in log records.
	ComponentName = "dryrun-validator"

	// TestExecutionTimeout is the maximum time allowed for running validation tests.
	// Tests run sequentially with Workers=1, so this should accommodate multiple tests.
	TestExecutionTimeout = 60 * time.Second
)

// Component implements the dry-run validator.
//
// It creates store overlays from admission requests and delegates validation
// to ProposalValidator. The component also runs validation tests if
// configured, which is not handled by ProposalValidator.
type Component struct {
	eventBus          *busevents.EventBus
	proposalValidator *proposalvalidator.Component
	config            *config.Config
	testRunner        *testrunner.Runner
	logger            *slog.Logger
}

// ComponentConfig contains configuration for creating a DryRunValidator.
type ComponentConfig struct {
	// EventBus is used to publish validation-tests observability events
	// (ValidationTestsStarted/Completed/Failed). The component does not
	// subscribe to anything; ValidateDirect is called synchronously.
	EventBus *busevents.EventBus

	// ProposalValidator is the component that performs render-validate pipeline.
	ProposalValidator *proposalvalidator.Component

	// Config is the controller configuration containing templates.
	Config *config.Config

	// Engine is the pre-compiled template engine for rendering validation tests.
	Engine templating.Engine

	// ValidationPaths is the filesystem paths for HAProxy validation.
	ValidationPaths *dataplane.ValidationPaths

	// Capabilities is the HAProxy capabilities determined from local version.
	Capabilities dataplane.Capabilities

	// Logger is the structured logger.
	Logger *slog.Logger

	// SkipValidationTests disables the embedded `validationTests` runner
	// for this validator. Set true for the admission-webhook caller —
	// `validationTests` are CHART-AUTHOR tests with their own fixtures
	// (e.g. expecting a `default-ssl-cert` Secret in their fixture set);
	// running them on every admission request both wastes work (the same
	// 130+ tests run for every Ingress, regardless of what the
	// submission contains) and surfaces fixture-vs-cluster mismatches as
	// admission denials. The chart's own CI / `haptic-controller validate`
	// path runs those tests; the webhook should only validate the
	// proposal itself.
	SkipValidationTests bool
}

// New creates a new DryRunValidator component.
func New(cfg *ComponentConfig) *Component {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create test runner for validation tests.
	// SkipValidationTests is honoured first: the admission webhook caller
	// passes true so chart-author tests (which run against their own
	// fixtures, not the live cluster) don't get re-executed for every
	// admission request — see the field doc on ComponentConfig.
	var testRunnerInstance *testrunner.Runner
	if !cfg.SkipValidationTests && len(cfg.Config.ValidationTests) > 0 {
		testRunnerInstance = testrunner.New(
			cfg.Config,
			cfg.Engine,
			cfg.ValidationPaths,
			testrunner.Options{
				Logger:       logger.With("component", "test-runner"),
				Workers:      1, // Sequential execution in webhook context
				Capabilities: cfg.Capabilities,
			},
		)
	}

	return &Component{
		eventBus:          cfg.EventBus,
		proposalValidator: cfg.ProposalValidator,
		config:            cfg.Config,
		testRunner:        testRunnerInstance,
		logger:            logger.With("component", ComponentName),
	}
}

// validateWithOverlay maps the GVK, builds an overlay store for the affected
// resource, runs the proposal validator, and runs validation tests if
// configured. It returns whether the resource is allowed and, if not, a
// user-facing reason.
func (c *Component) validateWithOverlay(ctx context.Context, gvk, namespace, name string, object any, operation, requestID string) (allowed bool, reason string) {
	resourceType, err := c.mapGVKToResourceType(gvk)
	if err != nil {
		return false, fmt.Sprintf("unsupported resource type: %v", err)
	}

	overlay := c.createOverlay(namespace, name, object, operation, requestID)
	overlays := map[string]*stores.StoreOverlay{
		resourceType: overlay,
	}

	c.logger.Debug("Created store overlay for dry-run",
		"request_id", requestID,
		"resource_type", resourceType,
		"operation", operation)

	result := c.proposalValidator.ValidateSync(ctx, overlays)
	if !result.Valid {
		c.logger.Info("Dry-run validation failed",
			"request_id", requestID,
			"phase", result.Phase,
			"error", result.Error)

		simplified := c.simplifyError(result.Phase, result.Error)
		c.logger.Debug("Simplified error",
			"request_id", requestID,
			"phase", result.Phase,
			"simplified", simplified)
		return false, simplified
	}

	if c.testRunner != nil && len(c.config.ValidationTests) > 0 {
		if err := c.runValidationTests(requestID); err != nil {
			return false, err.Error()
		}
	}

	c.logger.Debug("Dry-run validation passed",
		"request_id", requestID,
		"resource_type", resourceType,
		"duration_ms", result.DurationMs)

	return true, ""
}

// ValidateDirect performs synchronous dry-run validation.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - gvk: GroupVersionKind string (e.g., "networking.k8s.io/v1.Ingress")
//   - namespace: Resource namespace
//   - name: Resource name
//   - object: The Kubernetes resource object
//   - operation: Admission operation (CREATE, UPDATE, DELETE)
//
// Returns:
//   - allowed: Whether the resource passed validation
//   - reason: Denial reason if not allowed, empty otherwise
func (c *Component) ValidateDirect(ctx context.Context, gvk, namespace, name string, object any, operation string) (allowed bool, reason string) {
	c.logger.Debug("Direct validation request",
		"gvk", gvk,
		"namespace", namespace,
		"name", name,
		"operation", operation)

	return c.validateWithOverlay(ctx, gvk, namespace, name, object, operation, "direct")
}
