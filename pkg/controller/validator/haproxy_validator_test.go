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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func TestRendererToValidator_SuccessFlow(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

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

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()
	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewTemplateRenderedEvent(
		cfg.HAProxyConfig.Template,
		&dataplane.AuxiliaryFiles{},
		nil, 0, 0, "test", "", true,
	))

	timeout := time.After(30 * time.Second)
	var validationCompleted *events.ValidationCompletedEvent

	for validationCompleted == nil {
		select {
		case event := <-eventChan:
			switch e := event.(type) {
			case *events.ValidationCompletedEvent:
				validationCompleted = e
			case *events.ValidationFailedEvent:
				t.Fatalf("Validation failed unexpectedly: %v", e.Errors)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationCompletedEvent")
		}
	}

	require.NotNil(t, validationCompleted)
	assert.GreaterOrEqual(t, validationCompleted.DurationMs, int64(0))
}

func TestRendererToValidator_ValidationFailure(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

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

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()
	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	bus.Publish(events.NewTemplateRenderedEvent(
		cfg.HAProxyConfig.Template,
		&dataplane.AuxiliaryFiles{},
		nil, 0, 0, "test", "", true,
	))

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

func TestRendererToValidator_WithMapFiles(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

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
	}

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()
	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	auxFiles := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{{
			Path:    "maps/hosts.map",
			Content: "example.com backend1\ntest.com backend2\n",
		}},
	}
	bus.Publish(events.NewTemplateRenderedEvent(
		cfg.HAProxyConfig.Template,
		auxFiles,
		nil, 1, 0, "test", "", true,
	))

	timeout := time.After(10 * time.Second)
	var validationCompleted *events.ValidationCompletedEvent

	for validationCompleted == nil {
		select {
		case event := <-eventChan:
			switch e := event.(type) {
			case *events.ValidationCompletedEvent:
				validationCompleted = e
			case *events.ValidationFailedEvent:
				t.Fatalf("Validation failed unexpectedly: %v", e.Errors)
			}
		case <-timeout:
			t.Fatal("Timeout waiting for ValidationCompletedEvent")
		}
	}

	require.NotNil(t, validationCompleted)
	assert.GreaterOrEqual(t, validationCompleted.DurationMs, int64(0))
}

func TestValidator_ContextCancellation(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

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

func TestHAProxyValidator_Name(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

	validator := NewHAProxyValidator(bus, logger)

	assert.Equal(t, HAProxyValidatorComponentName, validator.Name())
	assert.Equal(t, "haproxy-validator", validator.Name())
}

func TestHAProxyValidator_HandleBecameLeader_NoState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 50)
	bus.Start()

	ctx := t.Context()

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

func TestHAProxyValidator_HandleBecameLeader_WithState(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

	// Valid HAProxy config for testing
	validConfig := `global
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
`

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 100)
	bus.Start()

	ctx := t.Context()

	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Directly publish TemplateRenderedEvent to populate validator state
	// (The renderer is now leader-only and we're testing the validator in isolation)
	bus.Publish(events.NewTemplateRenderedEvent(
		validConfig,                 // haproxyConfig
		&dataplane.AuxiliaryFiles{}, // auxiliaryFiles
		nil,                         // statusPatches
		0,                           // auxFileCount
		100,                         // durationMs
		"initial",                   // triggerReason
		"",                          // contentChecksum
		true,                        // coalescible
	))

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

func TestHAProxyValidator_HandleBecameLeader_AfterFailure(t *testing.T) {
	bus := busevents.NewEventBus(100)
	logger := testutil.NewTestLogger()

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

	validatorComponent := NewHAProxyValidator(bus, logger)

	eventChan := bus.Subscribe("test-sub", 100)
	bus.Start()

	ctx := t.Context()
	go validatorComponent.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Publish an invalid rendered config so validator records a failure.
	bus.Publish(events.NewTemplateRenderedEvent(
		cfg.HAProxyConfig.Template,
		&dataplane.AuxiliaryFiles{},
		nil, 0, 0, "initial", "", true,
	))

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
