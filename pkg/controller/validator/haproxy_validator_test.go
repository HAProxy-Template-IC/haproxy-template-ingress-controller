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
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/events"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/renderer"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/core/config"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane"
	busevents "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/events"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/types"
)

// mockStore implements types.Store for testing.
type mockStore struct {
	items []interface{}
}

func (m *mockStore) Add(resource interface{}, keys []string) error {
	m.items = append(m.items, resource)
	return nil
}

func (m *mockStore) Update(resource interface{}, keys []string) error {
	return nil
}

func (m *mockStore) Delete(keys ...string) error {
	return nil
}

func (m *mockStore) List() ([]interface{}, error) {
	return m.items, nil
}

func (m *mockStore) Get(keys ...string) ([]interface{}, error) {
	return nil, nil
}

func (m *mockStore) Clear() error {
	m.items = nil
	return nil
}

// TestRendererToValidator_SuccessFlow tests the successful flow from Renderer to Validator.
func TestRendererToValidator_SuccessFlow(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create a minimal valid HAProxy config
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	// Create mock haproxy-pods store
	haproxyPodStore := &mockStore{}

	// Create renderer
	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	rendererComponent, err := renderer.New(bus, cfg, stores, haproxyPodStore, capabilities, logger)
	require.NoError(t, err)

	// Create validator
	validatorComponent := NewHAProxyValidator(bus, logger)

	// Subscribe to events
	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start components
	go rendererComponent.Start(ctx)
	go validatorComponent.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	// Trigger reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	// Wait for validation completed event
	// Use longer timeout for race detector (which makes execution 2-10x slower)
	timeout := time.After(30 * time.Second)
	var validationCompleted *events.ValidationCompletedEvent
	sawRendered := false

	for {
		select {
		case event := <-eventChan:
			switch e := event.(type) {
			case *events.TemplateRenderedEvent:
				sawRendered = true
				assert.Contains(t, e.HAProxyConfig, "global")
				assert.Contains(t, e.HAProxyConfig, "frontend http-in")
			case *events.ValidationCompletedEvent:
				validationCompleted = e
				goto Done
			case *events.ValidationFailedEvent:
				t.Fatalf("Validation failed unexpectedly: %v", e.Errors)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationCompletedEvent")
		}
	}

Done:
	assert.True(t, sawRendered, "Should have received TemplateRenderedEvent")
	require.NotNil(t, validationCompleted)
	assert.GreaterOrEqual(t, validationCompleted.DurationMs, int64(0))
}

// TestRendererToValidator_ValidationFailure tests validation failure handling.
func TestRendererToValidator_ValidationFailure(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create an invalid HAProxy config (semantic error)
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers
    use_backend nonexistent if TRUE

backend servers
    server s1 127.0.0.1:8080
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	// Create mock haproxy-pods store
	haproxyPodStore := &mockStore{}

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	rendererComponent, err := renderer.New(bus, cfg, stores, haproxyPodStore, capabilities, logger)
	require.NoError(t, err)

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go rendererComponent.Start(ctx)
	go validatorComponent.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	// Wait for validation failed event
	// Use longer timeout for race detector (which makes execution 2-10x slower)
	timeout := time.After(30 * time.Second)
	var validationFailed *events.ValidationFailedEvent

	for {
		select {
		case event := <-eventChan:
			switch e := event.(type) {
			case *events.ValidationFailedEvent:
				validationFailed = e
				goto Done
			case *events.ValidationCompletedEvent:
				t.Fatal("Validation succeeded unexpectedly - config should be invalid")
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationFailedEvent")
		}
	}

Done:
	require.NotNil(t, validationFailed)
	assert.Greater(t, len(validationFailed.Errors), 0, "Should have validation errors")
	assert.GreaterOrEqual(t, validationFailed.DurationMs, int64(0))
}

// TestRendererToValidator_WithMapFiles tests validation with map files.
func TestRendererToValidator_WithMapFiles(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    http-request set-header X-Backend %[base,map(maps/hosts.map,default)]
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`,
		},
		Maps: map[string]config.MapFile{
			"maps/hosts.map": {
				Template: "example.com backend1\ntest.com backend2\n",
			},
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	// Create mock haproxy-pods store
	haproxyPodStore := &mockStore{}

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	rendererComponent, err := renderer.New(bus, cfg, stores, haproxyPodStore, capabilities, logger)
	require.NoError(t, err)

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go rendererComponent.Start(ctx)
	go validatorComponent.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewReconciliationTriggeredEvent("test"))

	// Wait for validation completed event
	// Use longer timeout for race detector (which makes execution 2-10x slower)
	timeout := time.After(10 * time.Second)
	var validationCompleted *events.ValidationCompletedEvent

	for {
		select {
		case event := <-eventChan:
			switch e := event.(type) {
			case *events.ValidationCompletedEvent:
				validationCompleted = e
				goto Done
			case *events.ValidationFailedEvent:
				t.Fatalf("Validation failed unexpectedly: %v", e.Errors)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationCompletedEvent")
		}
	}

Done:
	require.NotNil(t, validationCompleted)
	assert.GreaterOrEqual(t, validationCompleted.DurationMs, int64(0))
}

// TestRendererToValidator_MultipleReconciliations tests multiple reconciliation cycles.
func TestRendererToValidator_MultipleReconciliations(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	// Create mock haproxy-pods store
	haproxyPodStore := &mockStore{}

	// Use HAProxy 3.2+ version to enable CRT-list support in tests
	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	rendererComponent, err := renderer.New(bus, cfg, stores, haproxyPodStore, capabilities, logger)
	require.NoError(t, err)

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go rendererComponent.Start(ctx)
	go validatorComponent.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	// Trigger first reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("first"))

	// Wait for first validation
	// Use longer timeout for race detector (which makes execution 2-10x slower)
	timeout1 := time.After(10 * time.Second)
	receivedFirst := false

Loop1:
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.ValidationCompletedEvent); ok {
				receivedFirst = true
				break Loop1
			}
		case <-timeout1:
			t.Fatal("Timeout waiting for first validation")
		}
	}

	assert.True(t, receivedFirst)

	// Trigger second reconciliation
	bus.Publish(events.NewReconciliationTriggeredEvent("second"))

	// Wait for second validation
	// Use longer timeout for race detector (which makes execution 2-10x slower)
	timeout2 := time.After(10 * time.Second)
	var secondValidation *events.ValidationCompletedEvent

Loop2:
	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.ValidationCompletedEvent); ok {
				secondValidation = e
				break Loop2
			}
		case <-timeout2:
			t.Fatal("Timeout waiting for second validation")
		}
	}

	require.NotNil(t, secondValidation)
}

// TestValidator_ContextCancellation tests graceful shutdown on context cancellation.
func TestValidator_ContextCancellation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	validatorComponent := NewHAProxyValidator(bus, logger)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- validatorComponent.Start(ctx)
	}()

	// Cancel context
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Should return quickly
	timeout := time.After(1 * time.Second)
	select {
	case err := <-done:
		assert.NoError(t, err, "Start should return nil on context cancellation")
	case <-timeout:
		t.Fatal("Validator did not shut down within timeout")
	}
}

// TestHAProxyValidator_Name tests the Name method.
func TestHAProxyValidator_Name(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	validator := NewHAProxyValidator(bus, logger)

	assert.Equal(t, HAProxyValidatorComponentName, validator.Name())
	assert.Equal(t, "haproxy-validator", validator.Name())
}

// TestHAProxyValidator_HandleBecameLeader_NoState tests BecameLeaderEvent when no state is available.
func TestHAProxyValidator_HandleBecameLeader_NoState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Send BecameLeaderEvent without any prior validation (no state to replay)
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Wait a bit to ensure event is processed
	time.Sleep(100 * time.Millisecond)

	// No ValidationCompletedEvent should be published since there's no state to replay
	select {
	case event := <-eventChan:
		// Skip the BecameLeaderEvent itself if received
		if _, ok := event.(*events.BecameLeaderEvent); ok {
			// Try to get another event briefly
			select {
			case event := <-eventChan:
				_, isValidation := event.(*events.ValidationCompletedEvent)
				assert.False(t, isValidation, "Should not publish ValidationCompletedEvent when no state available")
			case <-time.After(100 * time.Millisecond):
				// Expected - no event
			}
		}
	case <-time.After(200 * time.Millisecond):
		// Expected - no events beyond the one we sent
	}
}

// TestHAProxyValidator_HandleBecameLeader_WithState tests BecameLeaderEvent when validation state is available.
func TestHAProxyValidator_HandleBecameLeader_WithState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	rendererComponent, err := renderer.New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go rendererComponent.Start(ctx)
	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Trigger a reconciliation to populate state
	bus.Publish(events.NewReconciliationTriggeredEvent("initial"))

	// Wait for first validation
	var firstValidation *events.ValidationCompletedEvent
	timeout := time.After(10 * time.Second)
	for firstValidation == nil {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.ValidationCompletedEvent); ok {
				firstValidation = e
			}
		case <-timeout:
			t.Fatal("Timeout waiting for first ValidationCompletedEvent")
		}
	}
	require.NotNil(t, firstValidation)

	// Now send BecameLeaderEvent - should replay state
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Wait for replayed validation event
	var replayedEvent *events.ValidationCompletedEvent
	timeout = time.After(1 * time.Second)
	for replayedEvent == nil {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.ValidationCompletedEvent); ok {
				replayedEvent = e
			}
		case <-timeout:
			t.Fatal("Timeout waiting for replayed ValidationCompletedEvent")
		}
	}

	require.NotNil(t, replayedEvent)
}

// TestHAProxyValidator_HandleBecameLeader_AfterFailure tests BecameLeaderEvent after a failed validation.
func TestHAProxyValidator_HandleBecameLeader_AfterFailure(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create an invalid HAProxy config
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: `global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers
    use_backend nonexistent if TRUE

backend servers
    server s1 127.0.0.1:8080
`,
		},
	}

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}

	capabilities := dataplane.CapabilitiesFromVersion(&dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"})
	rendererComponent, err := renderer.New(bus, cfg, stores, &mockStore{}, capabilities, logger)
	require.NoError(t, err)

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go rendererComponent.Start(ctx)
	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Trigger a reconciliation that will fail validation
	bus.Publish(events.NewReconciliationTriggeredEvent("initial"))

	// Wait for validation failure
	timeout := time.After(10 * time.Second)
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.ValidationFailedEvent); ok {
				goto ValidationFailed
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationFailedEvent")
		}
	}

ValidationFailed:
	// Now send BecameLeaderEvent - should NOT replay failed state
	bus.Publish(events.NewBecameLeaderEvent("test-pod"))

	// Wait a bit and verify no ValidationCompletedEvent is published
	// (we don't replay failures, only successes)
	time.Sleep(200 * time.Millisecond)

	// Drain events
	eventsReceived := 0
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(*events.ValidationCompletedEvent); ok {
				t.Fatal("Should not replay ValidationCompletedEvent after failure")
			}
			eventsReceived++
			// Continue draining
		default:
			goto Done
		}
	}

Done:
	// Should have received the BecameLeaderEvent at minimum
	t.Logf("Received %d events after BecameLeaderEvent", eventsReceived)
}

// TestHAProxyValidator_InvalidValidationPaths tests handling of invalid validation paths type.
func TestHAProxyValidator_InvalidValidationPaths(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Create a TemplateRenderedEvent with invalid ValidationPaths type
	event := events.NewTemplateRenderedEvent(
		"haproxy config",
		"validation config",
		"invalid-type",              // Invalid ValidationPaths type (should be *dataplane.ValidationPaths)
		&dataplane.AuxiliaryFiles{}, // Valid auxiliary files
		&dataplane.AuxiliaryFiles{}, // Valid validation aux files
		0,
		0,
		"", // triggerReason
	)
	bus.Publish(event)

	// Wait for validation failure due to invalid type
	timeout := time.After(1 * time.Second)
	var failedEvent *events.ValidationFailedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.ValidationFailedEvent); ok {
				failedEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationFailedEvent")
		}
	}

Done:
	require.NotNil(t, failedEvent)
	assert.Contains(t, failedEvent.Errors[0], "failed to extract validation paths")
}

// TestHAProxyValidator_InvalidAuxiliaryFiles tests handling of invalid auxiliary files type.
func TestHAProxyValidator_InvalidAuxiliaryFiles(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe(100)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Create a TemplateRenderedEvent with invalid ValidationAuxiliaryFiles type
	event := events.NewTemplateRenderedEvent(
		"haproxy config",
		"validation config",
		&dataplane.ValidationPaths{}, // Valid paths
		&dataplane.AuxiliaryFiles{},  // Valid auxiliary files
		"invalid-aux-files-type",     // Invalid ValidationAuxiliaryFiles type
		0,
		0,
		"", // triggerReason
	)
	bus.Publish(event)

	// Wait for validation failure due to invalid type
	timeout := time.After(1 * time.Second)
	var failedEvent *events.ValidationFailedEvent

	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(*events.ValidationFailedEvent); ok {
				failedEvent = e
				goto Done
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationFailedEvent")
		}
	}

Done:
	require.NotNil(t, failedEvent)
	assert.Contains(t, failedEvent.Errors[0], "failed to extract validation auxiliary files")
}
