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

package validator

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// panicHandler is a mock validation handler that panics.
type panicHandler struct {
	panicMessage string
}

func (h *panicHandler) HandleRequest(_ *events.ConfigValidationRequest) {
	panic(h.panicMessage)
}

// successHandler is a mock validation handler that succeeds.
type successHandler struct {
	eventBus   *busevents.EventBus
	name       string
	handleChan chan struct{}
}

func (h *successHandler) HandleRequest(req *events.ConfigValidationRequest) {
	response := events.NewConfigValidationResponse(
		req.RequestID(),
		h.name,
		true,
		nil,
	)
	h.eventBus.Publish(response)
	if h.handleChan != nil {
		close(h.handleChan)
	}
}

// TestBaseValidator_Stop tests the Stop() method.
func TestBaseValidator_Stop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := &successHandler{
		eventBus: bus,
		name:     "test",
	}
	validator := NewBaseValidator(bus, logger, "test", "Test validator", handler)

	bus.Start()

	// Start the validator in a goroutine
	done := make(chan struct{})
	go func() {
		validator.Start(context.Background())
		close(done)
	}()

	// Give validator time to start
	time.Sleep(50 * time.Millisecond)

	// Stop the validator
	validator.Stop()

	// Wait for validator to stop
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Validator did not stop")
	}

	// Verify Stop() is idempotent
	validator.Stop()
}

// TestBaseValidator_StopIdempotent tests that Stop() can be called multiple times.
func TestBaseValidator_StopIdempotent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := &successHandler{
		eventBus: bus,
		name:     "test",
	}
	validator := NewBaseValidator(bus, logger, "test", "Test validator", handler)

	// Call Stop() multiple times - should not panic
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			validator.Stop()
		}()
	}
	wg.Wait()
}

// TestBaseValidator_PanicRecovery tests panic recovery in handleEvent.
func TestBaseValidator_PanicRecovery(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := &panicHandler{panicMessage: "test panic"}
	validator := NewBaseValidator(bus, logger, "test-validator", "Test validator", handler)

	// Subscribe to events to receive the error response
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Create a valid test config
	cfg := createValidTestConfig()

	// Send a validation request that will trigger the panic
	req := events.NewConfigValidationRequest(cfg, "test-version")
	bus.Publish(req)

	// Wait for the error response
	response := testutil.WaitForEvent[*events.ConfigValidationResponse](t, eventChan, 2*time.Second)

	assert.Equal(t, "test-validator", response.ValidatorName)
	assert.False(t, response.Valid)
	require.Len(t, response.Errors, 1)
	assert.Contains(t, response.Errors[0], "validator panicked: test panic")
}

// TestBaseValidator_ContextCancellation tests context cancellation handling.
func TestBaseValidator_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := &successHandler{
		eventBus: bus,
		name:     "test",
	}
	validator := NewBaseValidator(bus, logger, "test", "Test validator", handler)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	// Start the validator in a goroutine
	done := make(chan struct{})
	go func() {
		validator.Start(ctx)
		close(done)
	}()

	// Give validator time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the context
	cancel()

	// Wait for validator to stop
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Validator did not stop on context cancellation")
	}
}

// TestBasicValidator_InvalidConfigType tests handling of invalid config type.
func TestBasicValidator_InvalidConfigType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewBasicValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Send a request with invalid config type (string instead of *coreconfig.Config)
	req := events.NewConfigValidationRequest("invalid-config-type", "test-version")
	bus.Publish(req)

	// Wait for error response
	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameBasic
		})

	assert.False(t, response.Valid)
	require.Len(t, response.Errors, 1)
	assert.Contains(t, response.Errors[0], "invalid config type")
}

// TestTemplateValidator_InvalidConfigType tests handling of invalid config type.
func TestTemplateValidator_InvalidConfigType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Send a request with invalid config type
	req := events.NewConfigValidationRequest("invalid-config-type", "test-version")
	bus.Publish(req)

	// Wait for error response
	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameTemplate
		})

	assert.False(t, response.Valid)
	require.Len(t, response.Errors, 1)
	assert.Contains(t, response.Errors[0], "invalid config type")
}

// TestJSONPathValidator_InvalidConfigType tests handling of invalid config type.
func TestJSONPathValidator_InvalidConfigType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewJSONPathValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Send a request with invalid config type
	req := events.NewConfigValidationRequest("invalid-config-type", "test-version")
	bus.Publish(req)

	// Wait for error response
	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameJSONPath
		})

	assert.False(t, response.Valid)
	require.Len(t, response.Errors, 1)
	assert.Contains(t, response.Errors[0], "invalid config type")
}

// TestTemplateValidator_SnippetErrors tests template snippet validation errors.
func TestTemplateValidator_SnippetErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Create config with invalid template snippets
	// The main template must reference the snippet for it to be compiled
	// (Snippets are only compiled when referenced by an entry point)
	cfg := &coreconfig.Config{
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: `{{ render "bad-snippet" }}`,
		},
		TemplateSnippets: map[string]coreconfig.TemplateSnippet{
			"bad-snippet": {
				Template: "{{ unclosed",
			},
		},
	}

	req := events.NewConfigValidationRequest(cfg, "test-version")
	bus.Publish(req)

	// Wait for response
	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameTemplate
		})

	// Templates are validated together, so syntax errors are detected
	// Error may not contain specific template path since validation is done as complete set
	assert.False(t, response.Valid)
	require.GreaterOrEqual(t, len(response.Errors), 1)
	assert.Contains(t, response.Errors[0], "syntax error")
}

// TestTemplateValidator_MapErrors tests map file template validation errors.
func TestTemplateValidator_MapErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Create config with invalid map template
	cfg := &coreconfig.Config{
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: "valid template",
		},
		Maps: map[string]coreconfig.MapFile{
			"bad-map.map": {
				Template: "{{ unclosed",
			},
		},
	}

	req := events.NewConfigValidationRequest(cfg, "test-version")
	bus.Publish(req)

	// Wait for response
	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameTemplate
		})

	// Templates are validated together, so syntax errors are detected
	// Error may not contain specific template path since validation is done as complete set
	assert.False(t, response.Valid)
	require.GreaterOrEqual(t, len(response.Errors), 1)
	assert.Contains(t, response.Errors[0], "syntax error")
}

// TestTemplateValidator_FileErrors tests file template validation errors.
func TestTemplateValidator_FileErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Create config with invalid file template
	cfg := &coreconfig.Config{
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: "valid template",
		},
		Files: map[string]coreconfig.GeneralFile{
			"bad-file.txt": {
				Template: "{{ unclosed",
			},
		},
	}

	req := events.NewConfigValidationRequest(cfg, "test-version")
	bus.Publish(req)

	// Wait for response
	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameTemplate
		})

	// Templates are validated together, so syntax errors are detected
	// Error may not contain specific template path since validation is done as complete set
	assert.False(t, response.Valid)
	require.GreaterOrEqual(t, len(response.Errors), 1)
	assert.Contains(t, response.Errors[0], "syntax error")
}

// TestTemplateValidator_CurrentConfigDeclaration tests that templates using
// currentConfig compile successfully. This ensures the TemplateValidator
// provides the currentConfig type declaration like other code paths do.
func TestTemplateValidator_CurrentConfigDeclaration(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Template that uses currentConfig - this is the pattern used in base.yaml
	// for slot preservation in BackendServers macro
	cfg := &coreconfig.Config{
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: `{%- if !isNil(currentConfig) %}
{%- for _, backend := range currentConfig.Backends %}
# Backend: {{ backend.Name }}
{%- end %}
{%- end %}
valid config`,
		},
	}

	req := events.NewConfigValidationRequest(cfg, "test-version")
	bus.Publish(req)

	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameTemplate
		})

	// Should pass - currentConfig should be available as a type declaration
	assert.True(t, response.Valid, "Template using currentConfig should compile successfully. Errors: %v", response.Errors)
	assert.Empty(t, response.Errors)
}

// TestJSONPathValidator_IndexByErrors tests IndexBy JSONPath validation errors.
func TestJSONPathValidator_IndexByErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewJSONPathValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Create config with invalid IndexBy JSONPath
	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy: []string{
					"invalid[[jsonpath",
				},
			},
		},
	}

	req := events.NewConfigValidationRequest(cfg, "test-version")
	bus.Publish(req)

	// Wait for response
	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameJSONPath
		})

	assert.False(t, response.Valid)
	require.GreaterOrEqual(t, len(response.Errors), 1)
	assert.Contains(t, response.Errors[0], "watched_resources.ingresses.index_by")
}

// TestBaseValidator_IgnoresOtherEvents tests that the validator ignores non-ConfigValidationRequest events.
func TestBaseValidator_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handleChan := make(chan struct{})
	handler := &successHandler{
		eventBus:   bus,
		name:       "test",
		handleChan: handleChan,
	}
	validator := NewBaseValidator(bus, logger, "test", "Test validator", handler)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Publish some non-validation events
	bus.Publish(events.NewReconciliationTriggeredEvent("test", true))
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// These should be ignored, so the handler should not be called
	select {
	case <-handleChan:
		t.Fatal("Handler should not be called for non-ConfigValidationRequest events")
	case <-time.After(200 * time.Millisecond):
		// Expected - handler was not called
	}
}
