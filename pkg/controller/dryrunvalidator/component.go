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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/proposalvalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "dryrun-validator"

	// ValidatorID identifies this validator in scatter-gather responses.
	ValidatorID = "dryrun"

	// EventBufferSize is the size of the event subscription buffer.
	EventBufferSize = 50

	// TestExecutionTimeout is the maximum time allowed for running validation tests.
	// Tests run sequentially with Workers=1, so this should accommodate multiple tests.
	TestExecutionTimeout = 60 * time.Second
)

// Component implements the dry-run validator.
//
// It subscribes to WebhookValidationRequest events, creates store overlays
// from admission requests, and delegates validation to ProposalValidator.
//
// The component also runs validation tests if configured, which is not
// handled by ProposalValidator.
type Component struct {
	eventBus          *busevents.EventBus
	eventChan         <-chan busevents.Event // Subscribed in constructor per CLAUDE.md guidelines
	proposalValidator *proposalvalidator.Component
	config            *config.Config
	testRunner        *testrunner.Runner
	logger            *slog.Logger
}

// ComponentConfig contains configuration for creating a DryRunValidator.
type ComponentConfig struct {
	// EventBus is the event bus for subscribing to requests and publishing responses.
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
}

// New creates a new DryRunValidator component.
//
// Parameters:
//   - cfg: Configuration for the component
//
// Returns:
//   - A new Component instance ready to be started
func New(cfg *ComponentConfig) *Component {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Create test runner for validation tests
	// Use Workers: 1 for webhook context (sequential execution, faster for small test counts)
	var testRunnerInstance *testrunner.Runner
	if len(cfg.Config.ValidationTests) > 0 {
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

	// Subscribe to only WebhookValidationRequest events per CLAUDE.md guidelines
	// This ensures subscription happens before EventBus.Start() and reduces buffer pressure
	eventChan := cfg.EventBus.SubscribeTypes(ComponentName, EventBufferSize, events.EventTypeWebhookValidationRequestSG)

	return &Component{
		eventBus:          cfg.EventBus,
		eventChan:         eventChan,
		proposalValidator: cfg.ProposalValidator,
		config:            cfg.Config,
		testRunner:        testRunnerInstance,
		logger:            logger.With("component", ComponentName),
	}
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// Start begins the validator's event loop.
//
// This method blocks until the context is cancelled. It processes
// WebhookValidationRequest events from the pre-subscribed channel.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Debug("dryrun validator starting")

	for {
		select {
		case event := <-c.eventChan:
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
//  2. Creating a StoreOverlay from the admission request
//  3. Delegating to ProposalValidator.ValidateSync() for rendering and validation
//  4. Running validation tests if configured
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

	// Create StoreOverlay from admission request
	overlay := c.createOverlay(req.Namespace, req.Name, req.Object, req.Operation, req.ID)

	// Create overlays map with single entry for the affected resource type
	overlays := map[string]*stores.StoreOverlay{
		resourceType: overlay,
	}

	c.logger.Debug("Created store overlay for dry-run",
		"request_id", req.ID,
		"resource_type", resourceType,
		"operation", req.Operation)

	// Delegate to ProposalValidator for render-validate pipeline
	// Use timeout context to prevent validation from hanging indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), validation.DefaultValidationTimeout)
	defer cancel()
	result := c.proposalValidator.ValidateSync(ctx, overlays)

	if !result.Valid {
		c.logger.Info("Dry-run validation failed",
			"request_id", req.ID,
			"phase", result.Phase,
			"error", result.Error)

		// Simplify error message for user-facing response
		simplified := c.simplifyError(result.Phase, result.Error)
		c.logger.Debug("Simplified error",
			"request_id", req.ID,
			"phase", result.Phase,
			"simplified", simplified)
		c.publishResponse(req.ID, false, simplified)
		return
	}

	// Run validation tests if configured
	if c.testRunner != nil && len(c.config.ValidationTests) > 0 {
		if err := c.runValidationTests(req.ID); err != nil {
			c.publishResponse(req.ID, false, err.Error())
			return
		}
	}

	c.logger.Debug("Dry-run validation passed",
		"request_id", req.ID,
		"resource_type", resourceType,
		"duration_ms", result.DurationMs)

	c.publishResponse(req.ID, true, "")
}

// createOverlay creates a StoreOverlay from validation parameters.
//
// Parameters:
//   - namespace: Resource namespace
//   - name: Resource name
//   - object: The Kubernetes resource object (must be runtime.Object for CREATE/UPDATE)
//   - operation: Admission operation (CREATE, UPDATE, DELETE)
//   - requestID: Request ID for logging (can be empty for direct validation)
func (c *Component) createOverlay(namespace, name string, object interface{}, operation, requestID string) *stores.StoreOverlay {
	// Handle DELETE first - it doesn't need an object
	if operation == "DELETE" {
		return stores.NewStoreOverlayForDelete(namespace, name)
	}

	// Convert the object to runtime.Object if possible
	obj, ok := object.(runtime.Object)
	if !ok {
		// If not a runtime.Object, return empty overlay
		// This shouldn't happen for K8s resources but handles edge cases
		c.logger.Warn("object is not runtime.Object",
			"request_id", requestID,
			"type", fmt.Sprintf("%T", object))
		return stores.NewStoreOverlay()
	}

	switch operation {
	case "CREATE":
		return stores.NewStoreOverlayForCreate(obj)
	case "UPDATE":
		return stores.NewStoreOverlayForUpdate(obj)
	default:
		c.logger.Warn("unknown operation type",
			"request_id", requestID,
			"operation", operation)
		return stores.NewStoreOverlay()
	}
}

// simplifyError simplifies an error message based on the validation phase.
func (c *Component) simplifyError(phase string, err error) string {
	if err == nil {
		return ""
	}
	switch phase {
	case "render":
		return dataplane.SimplifyRenderingError(err)
	case "syntax", "schema", "semantic":
		return dataplane.SimplifyValidationError(err)
	default:
		return err.Error()
	}
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

	// Map of irregular plurals and special cases for Kubernetes resources.
	// The default rule (append 's') doesn't work for:
	// - Words ending in -ss need -es suffix (ingress -> ingresses, not ingresss)
	// - Words ending in consonant + y need -ies suffix (policy -> policies)
	// - Words that are already plural (endpoints)
	irregularPlurals := map[string]string{
		// -ss ending needs -es suffix
		"ingress":       "ingresses",
		"ingressclass":  "ingressclasses",
		"storageclass":  "storageclasses",
		"priorityclass": "priorityclasses",
		"runtimeclass":  "runtimeclasses",
		// -y ending (after consonant) needs -ies suffix
		"networkpolicy":     "networkpolicies",
		"podsecuritypolicy": "podsecuritypolicies",
		// Already plural (no change needed)
		"endpoints": "endpoints",
	}

	if plural, ok := irregularPlurals[kindLower]; ok {
		return plural, nil
	}

	// Default: add 's' for regular plurals
	return kindLower + "s", nil
}

// runValidationTests executes validation tests and returns an error if tests fail.
func (c *Component) runValidationTests(requestID string) error {
	c.logger.Debug("Running validation tests",
		"request_id", requestID,
		"test_count", len(c.config.ValidationTests))

	testStartTime := time.Now()

	// Publish ValidationTestsStartedEvent
	c.eventBus.Publish(events.NewValidationTestsStartedEvent(len(c.config.ValidationTests)))

	// Run all validation tests with timeout
	ctx, cancel := context.WithTimeout(context.Background(), TestExecutionTimeout)
	defer cancel()
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

// ValidateDirect performs synchronous dry-run validation without scatter-gather.
//
// This method is intended for direct webhook integration, eliminating the
// event-based scatter-gather pattern for improved performance and simplicity.
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
func (c *Component) ValidateDirect(ctx context.Context, gvk, namespace, name string, object interface{}, operation string) (allowed bool, reason string) {
	c.logger.Debug("Direct validation request",
		"gvk", gvk,
		"namespace", namespace,
		"name", name,
		"operation", operation)

	// Map GVK to resource type
	resourceType, err := c.mapGVKToResourceType(gvk)
	if err != nil {
		return false, fmt.Sprintf("unsupported resource type: %v", err)
	}

	// Create StoreOverlay from parameters
	overlay := c.createOverlay(namespace, name, object, operation, "direct")

	// Create overlays map with single entry for the affected resource type
	overlays := map[string]*stores.StoreOverlay{
		resourceType: overlay,
	}

	c.logger.Debug("Created store overlay for direct validation",
		"resource_type", resourceType,
		"operation", operation)

	// Delegate to ProposalValidator for render-validate pipeline
	result := c.proposalValidator.ValidateSync(ctx, overlays)

	if !result.Valid {
		c.logger.Info("Direct validation failed",
			"gvk", gvk,
			"phase", result.Phase,
			"error", result.Error)

		// Simplify error message for user-facing response
		simplified := c.simplifyError(result.Phase, result.Error)
		return false, simplified
	}

	// Run validation tests if configured
	if c.testRunner != nil && len(c.config.ValidationTests) > 0 {
		if err := c.runValidationTests("direct"); err != nil {
			return false, err.Error()
		}
	}

	c.logger.Debug("Direct validation passed",
		"gvk", gvk,
		"resource_type", resourceType,
		"duration_ms", result.DurationMs)

	return true, ""
}
