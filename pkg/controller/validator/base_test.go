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
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// stubTypeBootstrapper returns an empty Result — useful for the
// validator tests in this file that exercise event-bus / handler
// behaviour and don't care about typed-resource declarations.
// Tests that DO care declare their own bootstrapper inline.
func stubTypeBootstrapper(_ context.Context, _ *coreconfig.Config) (*typebootstrap.Result, error) {
	return &typebootstrap.Result{
		Types:  map[string]reflect.Type{},
		Errors: map[string]error{},
	}, nil
}

type validationHandlerFunc func(context.Context, *coreconfig.Config, string) (bool, []string)

func (f validationHandlerFunc) Validate(ctx context.Context, cfg *coreconfig.Config, version string) (valid bool, errors []string) {
	return f(ctx, cfg, version)
}

// successHandler is a mock validation handler that succeeds.
type successHandler struct {
	handleChan chan struct{}
}

func (h *successHandler) Validate(context.Context, *coreconfig.Config, string) (valid bool, errors []string) {
	if h.handleChan != nil {
		close(h.handleChan)
	}
	return true, nil
}

func TestBaseValidator_Stop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := &successHandler{}
	validator := NewBaseValidator(bus, logger, "test", handler)

	bus.Start()

	// Start the validator in a goroutine
	done := make(chan struct{})
	go func() {
		validator.Start(context.Background())
		close(done)
	}()

	// Give validator time to start
	time.Sleep(50 * time.Millisecond)

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

func TestBaseValidator_StopIdempotent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := &successHandler{}
	validator := NewBaseValidator(bus, logger, "test", handler)

	// Call Stop() multiple times - should not panic
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			validator.Stop()
		})
	}
	wg.Wait()
}

func TestBaseValidator_PanicRecovery(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := validationHandlerFunc(func(_ context.Context, _ *coreconfig.Config, version string) (bool, []string) {
		if version == "panic" {
			panic("test panic")
		}
		return true, nil
	})
	validator := NewBaseValidator(bus, logger, "test-validator", handler)

	// Subscribe to events to receive the error response
	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go validator.Start(ctx)

	cfg := createValidTestConfig()

	// Send a validation request that will trigger the panic
	req := events.NewConfigValidationRequest(cfg, "panic")
	bus.Publish(req)

	// Wait for the error response
	response := testutil.WaitForEvent[*events.ConfigValidationResponse](t, eventChan, 2*time.Second)

	assert.Equal(t, req.RequestID(), response.RequestID())
	assert.Equal(t, "test-validator", response.ValidatorName)
	assert.False(t, response.Valid)
	require.Len(t, response.Errors, 1)
	assert.Contains(t, response.Errors[0], "validator panicked: test panic")

	recovery := events.NewConfigValidationRequest(cfg, "recovery")
	bus.Publish(recovery)
	response = testutil.WaitForEvent[*events.ConfigValidationResponse](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, recovery.RequestID(), response.RequestID())
	assert.True(t, response.Valid)
	assert.Empty(t, response.Errors)
}

func TestBaseValidator_PublishesHandlerVerdict(t *testing.T) {
	for _, failures := range [][]string{nil, {"first failure", "second failure"}} {
		t.Run(fmt.Sprintf("%d errors", len(failures)), func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			cfg := createValidTestConfig()
			handler := validationHandlerFunc(func(_ context.Context, actual *coreconfig.Config, version string) (bool, []string) {
				assert.Same(t, cfg, actual)
				assert.Equal(t, "test-version", version)
				return false, failures
			})
			validator := NewBaseValidator(bus, logger, "test", handler)
			responses := bus.SubscribeTypes("test-collector", 4, events.EventTypeConfigValidationResponse)
			bus.Start()

			req := events.NewConfigValidationRequest(cfg, "test-version")
			validator.HandleRequest(req)
			response := testutil.WaitForEvent[*events.ConfigValidationResponse](t, responses, testutil.LongTimeout)
			assert.Equal(t, req.RequestID(), response.RequestID())
			assert.Equal(t, "test", response.ValidatorName)
			assert.False(t, response.Valid)
			assert.Equal(t, failures, response.Errors)
		})
	}
}

func TestBaseValidator_ContextCancellation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handler := &successHandler{}
	validator := NewBaseValidator(bus, logger, "test", handler)

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

func TestValidators_InvalidConfigType(t *testing.T) {
	for _, input := range []struct {
		name   string
		config any
	}{
		{name: "wrong type", config: "invalid-config-type"},
		{name: "nil"},
		{name: "typed nil", config: (*coreconfig.Config)(nil)},
	} {
		t.Run(input.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			validators := []*BaseValidator{
				NewBasicValidator(bus, logger).BaseValidator,
				NewTemplateValidator(bus, logger, stubTypeBootstrapper).BaseValidator,
				NewJSONPathValidator(bus, logger).BaseValidator,
				NewValidationTestsValidator(bus, logger, stubTypeBootstrapper).BaseValidator,
			}
			responses := bus.SubscribeTypes("test-collector", 4, events.EventTypeConfigValidationResponse)
			bus.Start()
			for _, validator := range validators {
				req := events.NewConfigValidationRequest(input.config, "test-version")
				validator.HandleRequest(req)
				response := testutil.WaitForEvent[*events.ConfigValidationResponse](t, responses, testutil.LongTimeout)
				assert.Equal(t, req.RequestID(), response.RequestID())
				assert.Equal(t, validator.name, response.ValidatorName)
				assert.False(t, response.Valid)
				require.Len(t, response.Errors, 1)
				assert.Contains(t, response.Errors[0], "invalid config type")
			}
		})
	}
}

func TestTemplateValidator_SnippetErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger, stubTypeBootstrapper)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

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

func TestTemplateValidator_MapErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger, stubTypeBootstrapper)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

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

func TestTemplateValidator_FileErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger, stubTypeBootstrapper)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

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

// currentConfig compile successfully. This ensures the TemplateValidator
// provides the currentConfig type declaration like other code paths do.
func TestTemplateValidator_CurrentConfigDeclaration(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewTemplateValidator(bus, logger, stubTypeBootstrapper)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

	go validator.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Template that uses currentConfig - this is the pattern used in base.yaml
	// for slot preservation in BackendServers macro
	cfg := &coreconfig.Config{
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: `{%- if !isNil(currentConfig) %}
{%- for backendName, _ := range currentConfig.ServerIndex %}
# Backend: {{ backendName }}
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

func TestJSONPathValidator_IndexByErrors(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	validator := NewJSONPathValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

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

	response := testutil.WaitForEventWithPredicate(t, eventChan, 2*time.Second,
		func(resp *events.ConfigValidationResponse) bool {
			return resp.ValidatorName == ValidatorNameJSONPath
		})

	assert.False(t, response.Valid)
	require.GreaterOrEqual(t, len(response.Errors), 1)
	assert.Contains(t, response.Errors[0], "watched_resources.ingresses.index_by")
}

func TestJSONPathValidatorIncrementalActivationPaths(t *testing.T) {
	cfg := &coreconfig.Config{TemplateSnippets: map[string]coreconfig.TemplateSnippet{
		"valid": {
			Incremental: &coreconfig.IncrementalTemplate{
				WhenAnyPathExists: []string{
					`metadata.annotations['example.test/enabled']`,
					"spec.rules[*].filters",
				},
			},
		},
		"invalid": {
			Incremental: &coreconfig.IncrementalTemplate{
				WhenAnyPathExists: []string{"spec.rules[?(@.host)].host"},
			},
		},
	}}

	errors := validateJSONPaths(cfg)
	require.Len(t, errors, 1)
	assert.Contains(t, errors[0], "template_snippets.invalid.incremental.when_any_path_exists[0]")
}

func TestBaseValidator_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	handleChan := make(chan struct{})
	handler := &successHandler{
		handleChan: handleChan,
	}
	validator := NewBaseValidator(bus, logger, "test", handler)

	bus.Start()

	ctx := t.Context()

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
