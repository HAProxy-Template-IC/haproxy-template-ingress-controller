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
// request's hypothetical state and delegates to ProposalValidator.ValidateSync
// for rendering and validation.
package dryrunvalidator

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// ComponentName identifies this validator in log records.
const ComponentName = "dryrun-validator"

// Component implements the dry-run validator.
//
// It creates store overlays from admission requests and delegates validation
// to ProposalValidator and returns the shared pipeline's diagnostics.
type Component struct {
	proposalValidator *proposalvalidator.Component
	restMapper        meta.RESTMapper
	aliasesByGVR      map[schema.GroupVersionResource][]resourceAlias
	logger            *slog.Logger
}

// ComponentConfig contains configuration for creating a DryRunValidator.
type ComponentConfig struct {
	// ProposalValidator is the component that performs render-validate pipeline.
	ProposalValidator *proposalvalidator.Component

	// RESTMapper resolves an admission request's GVK to its GVR.
	RESTMapper meta.RESTMapper

	// WatchedResources defines the configured store aliases for each GVR.
	WatchedResources map[string]config.WatchedResource

	// Logger is the structured logger.
	Logger *slog.Logger
}

// New creates a new DryRunValidator component.
func New(cfg *ComponentConfig) (*Component, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	aliasesByGVR, err := buildResourceAliases(cfg.WatchedResources)
	if err != nil {
		return nil, err
	}

	return &Component{
		proposalValidator: cfg.ProposalValidator,
		restMapper:        cfg.RESTMapper,
		aliasesByGVR:      aliasesByGVR,
		logger:            logger.With("component", ComponentName),
	}, nil
}

// validateWithOverlay builds selector-aware overlays for the request GVR and
// runs the shared proposal pipeline.
func (c *Component) validateWithOverlay(ctx context.Context, gvk, namespace, name string, object, oldObject any, operation, requestID string) (allowed bool, reason string, warnings []string) {
	aliases, err := c.mapGVKToResourceAliases(gvk)
	if err != nil {
		return false, fmt.Sprintf("unsupported resource type: %v", err), nil
	}

	overlays, subjectAliases, err := c.createOverlays(aliases, namespace, name, object, oldObject, operation)
	if err != nil {
		return false, fmt.Sprintf("building admission overlay: %v", err), nil
	}

	c.logger.Debug("Created store overlays for dry-run",
		"request_id", requestID,
		"resource_types", subjectAliases,
		"operation", operation)

	pipelineResult, result := c.proposalValidator.ValidateSyncWithAdmissionSubject(
		ctx, overlays, subjectAliases, namespace, name,
	)
	warnings = append(warnings, result.Warnings...)
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
		return false, simplified, warnings
	}

	c.logger.Debug("Dry-run validation passed",
		"request_id", requestID,
		"resource_types", subjectAliases,
		"duration_ms", result.DurationMs)

	// Surface template-recorded Warning events as admission warnings so the
	// operator sees consequences at apply time (kubectl prints them) instead
	// of only in Events afterwards — e.g. a route that will fail closed with
	// 503 under the proposed state. PipelineResult is nil on the admitted-
	// via-baseline path, where no proposed-state render (and no event set)
	// exists.
	if pipelineResult != nil {
		renderedEvents, err := pipelineResult.MaterializeEvents()
		if err != nil {
			return false, fmt.Sprintf("reading rendered warnings: %v", err), warnings
		}
		warnings = append(warnings, formatRenderedEventWarnings(renderedEvents)...)
	}

	return true, "", warnings
}

// maxEventWarnings bounds how many template-recorded Warning events are
// surfaced per admission response; the rest collapse into a count so a
// cluster-wide breakage can't bloat every AdmissionReview.
const maxEventWarnings = 10

// formatRenderedEventWarnings renders template-recorded Warning events as
// human-readable admission warning strings, capped at maxEventWarnings.
func formatRenderedEventWarnings(events []templating.RenderedEvent) []string {
	var out []string
	suppressed := 0
	for _, e := range events {
		if e.Type != templating.EventTypeWarning {
			continue
		}
		if len(out) >= maxEventWarnings {
			suppressed++
			continue
		}
		subject := e.Name
		if e.Namespace != "" {
			subject = e.Namespace + "/" + e.Name
		}
		out = append(out, fmt.Sprintf("%s on %s %s: %s", e.Reason, e.Kind, subject, e.Message))
	}
	if suppressed > 0 {
		out = append(out, fmt.Sprintf("... and %d more warnings (see the Kubernetes Events on the affected resources)", suppressed))
	}
	return out
}

// ValidateDirect performs synchronous dry-run validation.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - gvk: GroupVersionKind string (e.g., "networking.k8s.io/v1.Ingress")
//   - namespace: Resource namespace
//   - name: Resource name
//   - object: The proposed Kubernetes resource object
//   - oldObject: The existing object for UPDATE and DELETE
//   - operation: Admission operation (CREATE, UPDATE, DELETE)
//
// Returns:
//   - allowed: Whether the resource passed validation
//   - reason: Denial reason if not allowed, empty otherwise
//   - warnings: Pipeline warnings surfaced through AdmissionResponse.Warnings
func (c *Component) ValidateDirect(ctx context.Context, gvk, namespace, name string, object, oldObject any, operation string) (allowed bool, reason string, warnings []string) {
	c.logger.Debug("Direct validation request",
		"gvk", gvk,
		"namespace", namespace,
		"name", name,
		"operation", operation)

	return c.validateWithOverlay(ctx, gvk, namespace, name, object, oldObject, operation, "direct")
}
