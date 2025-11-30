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

// Package dryrunvalidator implements the DryRunValidator component that
// performs dry-run reconciliation for webhook validation.
//
// This component:
// - Subscribes to WebhookValidationRequest events (scatter-gather)
// - Creates overlay stores simulating resource changes
// - Performs dry-run reconciliation (rendering + validation)
// - Publishes WebhookValidationResponse events
//
// The validator ensures resources are valid before they're saved to etcd,
// preventing invalid configurations from being admitted.
package dryrunvalidator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/renderer"
	"haproxy-template-ic/pkg/controller/resourcestore"
	"haproxy-template-ic/pkg/controller/testrunner"
	"haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/dataplane"
	"haproxy-template-ic/pkg/dataplane/auxiliaryfiles"
	busevents "haproxy-template-ic/pkg/events"
	"haproxy-template-ic/pkg/k8s/types"
	"haproxy-template-ic/pkg/templating"
)

const (
	// ValidatorID identifies this validator in scatter-gather responses.
	ValidatorID = "dryrun"

	// EventBufferSize is the size of the event subscription buffer.
	EventBufferSize = 50
)

// Component implements the dry-run validator.
//
// It subscribes to WebhookValidationRequest events, simulates resource changes
// using overlay stores, performs dry-run reconciliation (rendering + validation),
// and responds with validation results.
type Component struct {
	eventBus        *busevents.EventBus
	storeManager    *resourcestore.Manager
	config          *config.Config
	engine          *templating.TemplateEngine
	validationPaths *dataplane.ValidationPaths
	testRunner      *testrunner.Runner
	logger          *slog.Logger
	capabilities    dataplane.Capabilities // HAProxy/DataPlane API capabilities
}

// New creates a new DryRunValidator component.
//
// Parameters:
//   - eventBus: The EventBus for subscribing to requests and publishing responses
//   - storeManager: ResourceStoreManager for accessing stores and creating overlays
//   - cfg: Controller configuration containing templates
//   - engine: Pre-compiled template engine for rendering
//   - validationPaths: Filesystem paths for HAProxy validation
//   - capabilities: HAProxy capabilities determined from local version
//   - logger: Structured logger
//
// Returns:
//   - A new Component instance ready to be started
func New(
	eventBus *busevents.EventBus,
	storeManager *resourcestore.Manager,
	cfg *config.Config,
	engine *templating.TemplateEngine,
	validationPaths *dataplane.ValidationPaths,
	capabilities dataplane.Capabilities,
	logger *slog.Logger,
) *Component {
	// Create test runner for validation tests
	// Use Workers: 1 for webhook context (sequential execution, faster for small test counts)
	testRunnerInstance := testrunner.New(
		cfg,
		engine,
		validationPaths,
		testrunner.Options{
			Logger:       logger.With("component", "test-runner"),
			Workers:      1, // Sequential execution in webhook context
			Capabilities: capabilities,
		},
	)

	return &Component{
		eventBus:        eventBus,
		storeManager:    storeManager,
		config:          cfg,
		engine:          engine,
		validationPaths: validationPaths,
		testRunner:      testRunnerInstance,
		logger:          logger.With("component", "dryrun-validator"),
		capabilities:    capabilities,
	}
}

// Start begins the validator's event loop.
//
// This method blocks until the context is cancelled. It subscribes to
// WebhookValidationRequest events and processes them.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Info("DryRun validator starting")

	eventChan := c.eventBus.Subscribe(EventBufferSize)

	for {
		select {
		case event := <-eventChan:
			c.handleEvent(event)

		case <-ctx.Done():
			c.logger.Info("DryRun validator shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent processes events from the EventBus.
func (c *Component) handleEvent(event busevents.Event) {
	if req, ok := event.(*events.WebhookValidationRequest); ok {
		c.handleValidationRequest(req)
	}
}

// handleValidationRequest processes a webhook validation request.
//
// This performs dry-run validation by:
//  1. Mapping GVK to resource type
//  2. Creating overlay stores for ALL resources (with the proposed change)
//  3. Rendering HAProxy configuration using overlay stores
//  4. Validating the rendered configuration
//  5. Publishing a validation response
func (c *Component) handleValidationRequest(req *events.WebhookValidationRequest) {
	c.logger.Debug("Processing validation request",
		"request_id", req.ID,
		"gvk", req.GVK,
		"namespace", req.Namespace,
		"name", req.Name,
		"operation", req.Operation)

	// Map GVK to resource type
	resourceType, err := c.mapGVKToResourceType(req.GVK)
	if err != nil {
		c.publishResponse(req.ID, false, fmt.Sprintf("unsupported resource type: %v", err))
		return
	}

	// Verify resource store exists
	if _, exists := c.storeManager.GetStore(resourceType); !exists {
		c.publishResponse(req.ID, false, fmt.Sprintf("no store registered for %s", resourceType))
		return
	}

	// Create overlay stores for ALL resource types (including the modified one)
	operation := resourcestore.Operation(req.Operation)
	overlayStores, err := c.storeManager.CreateOverlayMap(resourceType, req.Namespace, req.Name, req.Object, operation)
	if err != nil {
		c.publishResponse(req.ID, false, fmt.Sprintf("failed to create overlay stores: %v", err))
		return
	}

	c.logger.Debug("Created overlay stores for dry-run",
		"request_id", req.ID,
		"store_count", len(overlayStores))

	// Phase 3: Full dry-run reconciliation
	// Render HAProxy configuration using overlay stores
	haproxyConfig, auxiliaryFiles, err := c.renderWithOverlayStores(overlayStores)
	if err != nil {
		c.logger.Info("Dry-run rendering failed",
			"request_id", req.ID,
			"error", err)

		// Simplify rendering error message for user-facing response
		// Keep full error in logs for debugging
		simplified := dataplane.SimplifyRenderingError(err)
		c.logger.Debug("Simplified rendering error",
			"request_id", req.ID,
			"original_length", len(err.Error()),
			"simplified_length", len(simplified),
			"simplified", simplified)
		c.publishResponse(req.ID, false, simplified)
		return
	}

	// Validate the rendered configuration
	// Pass nil version to use default v3.0 schema (safest for validation)
	err = dataplane.ValidateConfiguration(haproxyConfig, auxiliaryFiles, c.validationPaths, nil)
	if err != nil {
		c.logger.Info("Dry-run validation failed",
			"request_id", req.ID,
			"error", err)

		// Simplify error message for user-facing response
		// Keep full error in logs for debugging
		simplified := dataplane.SimplifyValidationError(err)
		c.logger.Debug("Simplified validation error",
			"request_id", req.ID,
			"original_length", len(err.Error()),
			"simplified_length", len(simplified),
			"simplified", simplified)
		c.publishResponse(req.ID, false, simplified)
		return
	}

	// Run validation tests if configured
	if len(c.config.ValidationTests) > 0 {
		if err := c.runValidationTests(req.ID); err != nil {
			c.publishResponse(req.ID, false, err.Error())
			return
		}
	}

	c.logger.Debug("Dry-run validation passed",
		"request_id", req.ID,
		"resource_type", resourceType,
		"config_bytes", len(haproxyConfig))

	c.publishResponse(req.ID, true, "")
}

// mapGVKToResourceType maps a GVK string to a resource type name.
//
// Examples:
//   - "networking.k8s.io/v1.Ingress" -> "ingresses"
//   - "v1.Service" -> "services"
//   - "v1.ConfigMap" -> "configmaps"
//
// Returns the plural, lowercase resource type name used as store keys.
func (c *Component) mapGVKToResourceType(gvk string) (string, error) {
	// Extract Kind from GVK
	// Format: "group/version.Kind" or "version.Kind"
	parts := strings.Split(gvk, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid GVK format: %s", gvk)
	}

	kind := parts[len(parts)-1]

	// Convert Kind to plural resource type
	// Handle common irregular plurals and special cases
	kindLower := strings.ToLower(kind)

	// Map of irregular plurals and special cases
	irregularPlurals := map[string]string{
		"ingress":   "ingresses",
		"endpoints": "endpoints", // Already plural
	}

	if plural, ok := irregularPlurals[kindLower]; ok {
		return plural, nil
	}

	// Default: add 's' for regular plurals
	return kindLower + "s", nil
}

// renderWithOverlayStores renders HAProxy configuration using overlay stores.
//
// This replicates the Renderer component's logic but uses overlay stores
// to simulate the proposed resource changes.
func (c *Component) renderWithOverlayStores(overlayStores map[string]types.Store) (string, *dataplane.AuxiliaryFiles, error) {
	// Build rendering context with overlay stores (similar to renderer.Component.buildRenderingContext)
	renderCtx := c.buildRenderingContext(overlayStores)

	// Render main HAProxy configuration
	haproxyConfig, err := c.engine.Render("haproxy.cfg", renderCtx)
	if err != nil {
		return "", nil, fmt.Errorf("failed to render haproxy.cfg: %w", err)
	}

	// Render auxiliary files
	auxiliaryFiles, err := c.renderAuxiliaryFiles(renderCtx)
	if err != nil {
		return "", nil, fmt.Errorf("failed to render auxiliary files: %w", err)
	}

	return haproxyConfig, auxiliaryFiles, nil
}

// buildRenderingContext builds the template rendering context using overlay stores.
//
// This mirrors renderer.Component.buildRenderingContext and TestRunner.buildRenderingContext.
// The context includes resources (overlay stores), template snippets, pathResolver, and controller configuration.
func (c *Component) buildRenderingContext(stores map[string]types.Store) map[string]interface{} {
	// Create resources map with wrapped stores
	resources := make(map[string]interface{})

	for resourceTypeName, store := range stores {
		resources[resourceTypeName] = &renderer.StoreWrapper{
			Store:        store,
			ResourceType: resourceTypeName,
			Logger:       c.logger,
		}
	}

	// Build template snippets list
	snippetNames := c.sortSnippetsByPriority()

	// Create PathResolver from ValidationPaths
	// ValidationPaths already has CRTListDir set correctly based on capabilities
	pathResolver := &templating.PathResolver{
		MapsDir:    c.validationPaths.MapsDir,
		SSLDir:     c.validationPaths.SSLCertsDir,
		CRTListDir: c.validationPaths.CRTListDir,
		GeneralDir: c.validationPaths.GeneralStorageDir,
	}

	// Build final context
	return map[string]interface{}{
		"resources":         resources,
		"template_snippets": snippetNames,
		"pathResolver":      pathResolver,
		"config":            c.config,
	}
}

// sortSnippetsByPriority sorts template snippet names by priority, then alphabetically.
func (c *Component) sortSnippetsByPriority() []string {
	// Extract snippet names in sorted order (by priority, then name)
	type snippetWithPriority struct {
		name     string
		priority int
	}

	list := make([]snippetWithPriority, 0, len(c.config.TemplateSnippets))
	for name, snippet := range c.config.TemplateSnippets {
		priority := snippet.Priority
		if priority == 0 {
			priority = 500 // Default priority
		}
		list = append(list, snippetWithPriority{name, priority})
	}

	// Sort by priority (ascending), then by name (alphabetically)
	// Using simple bubble sort to avoid importing sort package again
	for i := 0; i < len(list)-1; i++ {
		for j := 0; j < len(list)-i-1; j++ {
			if list[j].priority > list[j+1].priority ||
				(list[j].priority == list[j+1].priority && list[j].name > list[j+1].name) {
				list[j], list[j+1] = list[j+1], list[j]
			}
		}
	}

	names := make([]string, len(list))
	for i, item := range list {
		names[i] = item.name
	}

	return names
}

// renderAuxiliaryFiles renders all auxiliary files (maps, general files, SSL certificates).
func (c *Component) renderAuxiliaryFiles(renderCtx map[string]interface{}) (*dataplane.AuxiliaryFiles, error) {
	auxFiles := &dataplane.AuxiliaryFiles{}

	// Render map files
	for name := range c.config.Maps {
		rendered, err := c.engine.Render(name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to render map file %s: %w", name, err)
		}

		auxFiles.MapFiles = append(auxFiles.MapFiles, auxiliaryfiles.MapFile{
			Path:    name,
			Content: rendered,
		})
	}

	// Render general files
	for name := range c.config.Files {
		rendered, err := c.engine.Render(name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to render general file %s: %w", name, err)
		}

		auxFiles.GeneralFiles = append(auxFiles.GeneralFiles, auxiliaryfiles.GeneralFile{
			Filename: name,
			Content:  rendered,
		})
	}

	// Render SSL certificates
	for name := range c.config.SSLCertificates {
		rendered, err := c.engine.Render(name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to render SSL certificate %s: %w", name, err)
		}

		auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
			Path:    name,
			Content: rendered,
		})
	}

	return auxFiles, nil
}

// runValidationTests executes validation tests and returns an error if tests fail.
func (c *Component) runValidationTests(requestID string) error {
	c.logger.Debug("Running validation tests",
		"request_id", requestID,
		"test_count", len(c.config.ValidationTests))

	testStartTime := time.Now()

	// Publish ValidationTestsStartedEvent
	c.eventBus.Publish(events.NewValidationTestsStartedEvent(len(c.config.ValidationTests)))

	// Run all validation tests
	ctx := context.Background() // Use background context for test execution
	testResults, err := c.testRunner.RunTests(ctx, "")
	testDuration := time.Since(testStartTime)

	if err != nil {
		c.logger.Info("Validation tests execution failed",
			"request_id", requestID,
			"error", err)
		return fmt.Errorf("validation test execution failed: %w", err)
	}

	// Publish ValidationTestsCompletedEvent
	c.eventBus.Publish(events.NewValidationTestsCompletedEvent(
		testResults.TotalTests,
		testResults.PassedTests,
		testResults.FailedTests,
		testDuration.Milliseconds(),
	))

	// If any tests failed, reject the admission
	if !testResults.AllPassed() {
		c.logger.Info("Validation tests failed",
			"request_id", requestID,
			"total_tests", testResults.TotalTests,
			"passed_tests", testResults.PassedTests,
			"failed_tests", testResults.FailedTests)

		// Collect failed test names
		failedTestNames := make([]string, 0, testResults.FailedTests)
		for i := range testResults.TestResults {
			result := &testResults.TestResults[i]
			if !result.Passed {
				failedTestNames = append(failedTestNames, result.TestName)
			}
		}

		// Publish ValidationTestsFailedEvent
		c.eventBus.Publish(events.NewValidationTestsFailedEvent(failedTestNames))

		// Build detailed error message
		return c.buildTestFailureError(testResults)
	}

	c.logger.Debug("Validation tests passed",
		"request_id", requestID,
		"total_tests", testResults.TotalTests,
		"duration_ms", testDuration.Milliseconds())

	return nil
}

// buildTestFailureError builds a detailed error message from test results.
func (c *Component) buildTestFailureError(testResults *testrunner.TestResults) error {
	errorMsg := fmt.Sprintf("Validation tests failed: %d/%d tests failed\n\nFailed tests:\n",
		testResults.FailedTests, testResults.TotalTests)

	for i := range testResults.TestResults {
		result := &testResults.TestResults[i]
		if !result.Passed {
			errorMsg += fmt.Sprintf("\n- Test: %s\n", result.TestName)
			if result.RenderError != "" {
				errorMsg += fmt.Sprintf("  Rendering failed: %s\n", result.RenderError)
			}
			for _, assertion := range result.Assertions {
				if !assertion.Passed {
					errorMsg += fmt.Sprintf("  Assertion failed: %s - %s\n",
						assertion.Description, assertion.Error)
				}
			}
		}
	}

	return fmt.Errorf("%s", errorMsg)
}

// publishResponse publishes a WebhookValidationResponse event.
func (c *Component) publishResponse(requestID string, allowed bool, reason string) {
	response := events.NewWebhookValidationResponse(requestID, ValidatorID, allowed, reason)
	c.eventBus.Publish(response)

	if allowed {
		c.logger.Debug("Published allowed response", "request_id", requestID)
	} else {
		c.logger.Info("Published denied response",
			"request_id", requestID,
			"reason", reason)
	}
}
