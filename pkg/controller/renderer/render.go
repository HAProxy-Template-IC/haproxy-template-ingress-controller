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

package renderer

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// singleRenderResult holds the output of a single render (production or validation).
type singleRenderResult struct {
	haproxyConfig  string
	auxiliaryFiles *dataplane.AuxiliaryFiles
	statusPatches  []templating.StatusPatch
	durationMs     int64
}

// performRender renders all templates for a reconciliation event.
// Uses single render with relative paths that work with HAProxy's `default-path origin`.
// Propagates correlation ID from the triggering event to the rendered event.
// This method is called by handleReconciliationTriggered after coalescing logic.
func (c *Component) performRender(event *events.ReconciliationTriggeredEvent) {
	// Track processing for health check stall detection
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	startTime := time.Now()
	correlationID := event.CorrelationID()
	c.logger.Debug("Template rendering triggered",
		"reason", event.Reason,
		"correlation_id", correlationID)

	// Create path resolver with fixed relative paths.
	// These paths work with HAProxy's `default-path origin` directive.
	// CRT-list files always use the general files directory to avoid triggering
	// HAProxy reloads when creating CRT-list files through the native API.
	pathResolver := &templating.PathResolver{
		MapsDir:    "maps",
		SSLDir:     "ssl",
		CRTListDir: "files",
		GeneralDir: "files",
	}

	// Render templates
	result, err := c.renderSingle(pathResolver)
	if err != nil {
		// Error already published by renderSingle
		return
	}

	// Calculate metrics
	durationMs := time.Since(startTime).Milliseconds()
	auxFileCount := len(result.auxiliaryFiles.MapFiles) +
		len(result.auxiliaryFiles.GeneralFiles) +
		len(result.auxiliaryFiles.SSLCertificates)

	c.logger.Debug("Template rendering completed",
		"total_ms", durationMs,
		"render_ms", result.durationMs,
		"config_bytes", len(result.haproxyConfig),
		"auxiliary_files", auxFileCount,
	)

	// Compute checksum to detect unchanged content
	checksumHex := dataplane.ComputeContentChecksum(result.haproxyConfig, result.auxiliaryFiles)

	// Skip publishing if rendered content is unchanged.
	// Note: HTTP content validation is handled separately via ProposalValidation.
	// When HTTP content is validated and promoted, HTTPStore triggers its own
	// ReconciliationTriggeredEvent, which will cause a fresh render with the
	// promoted content. The Renderer no longer needs to know about HTTP pending state.
	if checksumHex == c.lastRenderedChecksum {
		c.logger.Debug("skipping template rendered event, content unchanged",
			"checksum", checksumHex,
			"trigger_reason", event.Reason,
		)
		return
	}

	// Update checksum before publishing
	c.lastRenderedChecksum = checksumHex

	// Publish success event with rendered config, propagating correlation and coalescibility
	c.eventBus.Publish(events.NewTemplateRenderedEvent(
		result.haproxyConfig,
		result.auxiliaryFiles,
		result.statusPatches,
		auxFileCount,
		durationMs,
		event.Reason,
		checksumHex,
		event.Coalescible(),
		events.PropagateCorrelation(event),
	))
}

// renderSingle performs template rendering and returns the result.
func (c *Component) renderSingle(pathResolver *templating.PathResolver) (*singleRenderResult, error) {
	renderStart := time.Now()

	// Build rendering context
	contextStart := time.Now()
	renderContext, fileRegistry, statusPatchCollector := c.buildRenderingContext(c.ctx, pathResolver, false)
	contextMs := time.Since(contextStart).Milliseconds()

	// Render main HAProxy config
	mainStart := time.Now()
	haproxyConfig, err := c.engine.Render(c.ctx, names.MainTemplateName, renderContext)
	mainMs := time.Since(mainStart).Milliseconds()
	if err != nil {
		c.publishRenderFailure(names.MainTemplateName, err)
		return nil, err
	}

	// Render auxiliary files
	auxStart := time.Now()
	staticFiles, err := c.renderAuxiliaryFiles(c.ctx, renderContext)
	auxMs := time.Since(auxStart).Milliseconds()
	if err != nil {
		// Error already published by renderAuxiliaryFiles
		return nil, err
	}

	dynamicFiles := fileRegistry.GetFiles()
	auxiliaryFiles := rendercontext.MergeAuxiliaryFiles(staticFiles, dynamicFiles)

	totalMs := time.Since(renderStart).Milliseconds()

	c.logger.Debug("Render breakdown",
		"context_ms", contextMs,
		"main_template_ms", mainMs,
		"aux_files_ms", auxMs,
		"total_ms", totalMs,
	)

	return &singleRenderResult{
		haproxyConfig:  haproxyConfig,
		auxiliaryFiles: auxiliaryFiles,
		statusPatches:  statusPatchCollector.Patches(),
		durationMs:     totalMs,
	}, nil
}

// renderAuxiliaryFiles renders all auxiliary files (maps, general files, SSL certificates) in parallel.
// It respects the caller's context for cancellation.
func (c *Component) renderAuxiliaryFiles(ctx context.Context, renderCtx map[string]any) (*dataplane.AuxiliaryFiles, error) {
	totalFiles := len(c.config.Maps) + len(c.config.Files) + len(c.config.SSLCertificates)
	if totalFiles == 0 {
		return &dataplane.AuxiliaryFiles{}, nil
	}

	// Use mutex-protected slices for concurrent appends
	var mu sync.Mutex
	auxFiles := &dataplane.AuxiliaryFiles{}

	// Create errgroup for parallel rendering. We discard the derived context because:
	// 1. Template rendering is CPU-bound and doesn't benefit from early cancellation
	// 2. errgroup still coordinates completion and returns the first error via Wait()
	// 3. The caller's ctx is available for overall timeout/cancellation if needed
	g, _ := errgroup.WithContext(ctx)

	// Render map files in parallel
	for name := range c.config.Maps {
		g.Go(func() error {
			rendered, err := c.engine.Render(ctx, name, renderCtx)
			if err != nil {
				c.publishRenderFailure(name, err)
				return err
			}
			mu.Lock()
			auxFiles.MapFiles = append(auxFiles.MapFiles, auxiliaryfiles.MapFile{
				Path:    name,
				Content: rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	// Render general files in parallel
	for name := range c.config.Files {
		g.Go(func() error {
			rendered, err := c.engine.Render(ctx, name, renderCtx)
			if err != nil {
				c.publishRenderFailure(name, err)
				return err
			}
			mu.Lock()
			auxFiles.GeneralFiles = append(auxFiles.GeneralFiles, auxiliaryfiles.GeneralFile{
				Filename: name,
				Path:     filepath.Join(c.config.Dataplane.GeneralStorageDir, name),
				Content:  rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	// Render SSL certificates in parallel
	for name := range c.config.SSLCertificates {
		g.Go(func() error {
			rendered, err := c.engine.Render(ctx, name, renderCtx)
			if err != nil {
				c.publishRenderFailure(name, err)
				return err
			}
			mu.Lock()
			auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
				Path:    name,
				Content: rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return auxFiles, nil
}

// publishRenderFailure publishes a template render failure event.
func (c *Component) publishRenderFailure(templateName string, err error) {
	// Get template content for context in error message
	templateContent, _ := c.engine.GetRawTemplate(templateName)

	// Format error for human readability
	formattedError := templating.FormatRenderError(err, templateName, templateContent)

	// Log formatted error (multi-line for readability)
	c.logger.Error("Template rendering failed\n"+formattedError,
		"template", templateName,
		"error_raw", err.Error()) // Keep raw error for programmatic access

	// Publish event with formatted error
	c.eventBus.Publish(events.NewTemplateRenderFailedEvent(
		templateName,
		formattedError,
		"", // Stack trace could be added here if needed
	))
}
