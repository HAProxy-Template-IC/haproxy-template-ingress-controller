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
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
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
// configured, which is not handled by ProposalValidator. After a successful
// dry-run, it dispatches the rendered file set to any configured pluggable
// validators (e.g. the SPOA hub running in --validate-socket mode); their
// errors deny admission with line/col-precise diagnostics, their warnings
// flow up to the webhook handler unchanged.
type Component struct {
	eventBus           *busevents.EventBus
	proposalValidator  *proposalvalidator.Component
	pluggableValidator *pluggablevalidator.Manager
	config             *config.Config
	testRunner         *testrunner.Runner
	logger             *slog.Logger
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

	// PluggableValidator is the optional pluggable-validator manager that
	// dispatches the rendered file set to external validator sidecars
	// (e.g. the SPOA hub running in --validate-socket mode). Nil disables
	// the dispatch entirely. When non-nil but Configured() returns false
	// (no validators in the CRD), the manager's ValidateAll is a no-op,
	// so callers don't need to pre-check before wiring this in.
	PluggableValidator *pluggablevalidator.Manager
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
		eventBus:           cfg.EventBus,
		proposalValidator:  cfg.ProposalValidator,
		pluggableValidator: cfg.PluggableValidator,
		config:             cfg.Config,
		testRunner:         testRunnerInstance,
		logger:             logger.With("component", ComponentName),
	}
}

// validateWithOverlay maps the GVK, builds an overlay store for the affected
// resource, runs the proposal validator, runs validation tests if
// configured, and finally dispatches the rendered file set to any
// pluggable validators. Returns whether the resource is allowed, a
// user-facing denial reason (empty when allowed), and any soft warnings
// the pluggable validators surfaced — the latter flow through to the
// admission response on both allow and deny paths.
func (c *Component) validateWithOverlay(ctx context.Context, gvk, namespace, name string, object any, operation, requestID string) (allowed bool, reason string, warnings []string) {
	resourceType, err := c.mapGVKToResourceType(gvk)
	if err != nil {
		return false, fmt.Sprintf("unsupported resource type: %v", err), nil
	}

	overlay := c.createOverlay(namespace, name, object, operation, requestID)
	overlays := map[string]*stores.StoreOverlay{
		resourceType: overlay,
	}

	c.logger.Debug("Created store overlay for dry-run",
		"request_id", requestID,
		"resource_type", resourceType,
		"operation", operation)

	pipelineResult, result := c.proposalValidator.ValidateSync(ctx, overlays)
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
		return false, simplified, nil
	}

	if c.testRunner != nil && len(c.config.ValidationTests) > 0 {
		if err := c.runValidationTests(requestID); err != nil {
			return false, err.Error(), nil
		}
	}

	// Pluggable validator dispatch runs after the standard pipeline
	// succeeds — we only want to pay the socket round-trip if the
	// rendered config is otherwise valid. PipelineResult is nil if the
	// validator admitted via the baseline-still-fails path; in that case
	// there are no proposed-state files to validate (baseline rendered
	// against the live store, not the overlay) so the dispatch is
	// skipped.
	if c.pluggableValidator != nil && c.pluggableValidator.Configured() && pipelineResult != nil {
		files := buildPluggableFiles(pipelineResult)
		outcome := c.pluggableValidator.ValidateAll(ctx, files)
		warnings = formatDiagnostics(outcome.Warnings)
		if len(outcome.Errors) > 0 {
			c.logger.Info("Pluggable validator denied admission",
				"request_id", requestID,
				"errors", len(outcome.Errors),
				"warnings", len(outcome.Warnings))
			return false, formatErrorReason(outcome.Errors), warnings
		}
		if len(outcome.Warnings) > 0 {
			c.logger.Debug("Pluggable validator surfaced warnings",
				"request_id", requestID,
				"warnings", len(outcome.Warnings))
		}
	}

	c.logger.Debug("Dry-run validation passed",
		"request_id", requestID,
		"resource_type", resourceType,
		"duration_ms", result.DurationMs)

	return true, "", warnings
}

// buildPluggableFiles flattens a pipeline result into the file set the
// pluggable validator manager expects. The rendered haproxy.cfg uses the
// canonical "/etc/haproxy/haproxy.cfg" path so validator globs that
// target it (e.g. "/etc/haproxy/haproxy.cfg") match. Auxiliary files
// keep their pre-computed absolute Path verbatim — the dataplane sync
// path treats those as the on-disk locations HAProxy will reference, so
// the validator sees the exact same paths the running HAProxy will see.
func buildPluggableFiles(result *pipeline.PipelineResult) []pluggablevalidator.File {
	const haproxyConfigPath = "/etc/haproxy/haproxy.cfg"

	capacity := 1
	if result.AuxiliaryFiles != nil {
		capacity += result.AuxFileCount
	}
	files := make([]pluggablevalidator.File, 0, capacity)
	files = append(files, pluggablevalidator.File{
		Path:    haproxyConfigPath,
		Content: result.HAProxyConfig,
	})
	if result.AuxiliaryFiles == nil {
		return files
	}
	for _, f := range result.AuxiliaryFiles.GeneralFiles {
		files = append(files, pluggablevalidator.File{Path: f.Path, Content: f.Content})
	}
	for _, c := range result.AuxiliaryFiles.SSLCertificates {
		files = append(files, pluggablevalidator.File{Path: c.Path, Content: c.Content})
	}
	for _, c := range result.AuxiliaryFiles.SSLCaFiles {
		files = append(files, pluggablevalidator.File{Path: c.Path, Content: c.Content})
	}
	for _, m := range result.AuxiliaryFiles.MapFiles {
		files = append(files, pluggablevalidator.File{Path: m.Path, Content: m.Content})
	}
	for _, l := range result.AuxiliaryFiles.CRTListFiles {
		files = append(files, pluggablevalidator.File{Path: l.Path, Content: l.Content})
	}
	return files
}

// formatDiagnostics renders pluggable-validator diagnostics as the short
// strings the API server attaches to AdmissionResponse.Warnings. Format:
// "<path>:<line>:<column>: <message>" (path/line/col omitted when 0/empty
// for protocol-level diagnostics).
func formatDiagnostics(diags []pluggablevalidator.Diagnostic) []string {
	if len(diags) == 0 {
		return nil
	}
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, formatDiagnostic(d))
	}
	return out
}

// formatDiagnostic produces a single human-readable line for one
// diagnostic. Protocol-level diagnostics (empty Path) drop the location
// prefix entirely so warnings about the validator itself read naturally.
func formatDiagnostic(d pluggablevalidator.Diagnostic) string {
	if d.Path == "" {
		return d.Message
	}
	if d.Line == 0 {
		return fmt.Sprintf("%s: %s", d.Path, d.Message)
	}
	if d.Column == 0 {
		return fmt.Sprintf("%s:%d: %s", d.Path, d.Line, d.Message)
	}
	return fmt.Sprintf("%s:%d:%d: %s", d.Path, d.Line, d.Column, d.Message)
}

// formatErrorReason joins one or more error diagnostics into the single
// admission-denial string the API server prints back to the user. Each
// diagnostic gets its own line so kubectl displays them one per line.
func formatErrorReason(diags []pluggablevalidator.Diagnostic) string {
	lines := make([]string, 0, len(diags))
	for _, d := range diags {
		lines = append(lines, formatDiagnostic(d))
	}
	return strings.Join(lines, "\n")
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
//   - warnings: Soft warnings from pluggable validators, surfaced via
//     AdmissionResponse.Warnings on both allow and deny paths
func (c *Component) ValidateDirect(ctx context.Context, gvk, namespace, name string, object any, operation string) (allowed bool, reason string, warnings []string) {
	c.logger.Debug("Direct validation request",
		"gvk", gvk,
		"namespace", namespace,
		"name", name,
		"operation", operation)

	return c.validateWithOverlay(ctx, gvk, namespace, name, object, operation, "direct")
}
