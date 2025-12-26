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

package configchange

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// testDebounceInterval is a short debounce interval for tests.
// Using a short interval (50ms) keeps tests fast while still exercising debounce logic.
const testDebounceInterval = 50 * time.Millisecond

func TestNewConfigChangeHandler(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)
	validators := []string{"basic", "template"}

	handler := NewConfigChangeHandler(bus, logger, configCh, validators, testDebounceInterval)

	require.NotNil(t, handler)
	assert.Equal(t, bus, handler.eventBus)
	assert.NotNil(t, handler.eventChan) // Event channel subscribed in constructor
	assert.NotNil(t, handler.logger)    // Logger is enhanced with component name
	// Can't directly compare bidirectional channel to send-only channel, just verify it's set
	assert.NotNil(t, handler.configChangeCh)
	assert.Equal(t, validators, handler.validators)
	assert.NotNil(t, handler.stopCh)
	assert.Equal(t, testDebounceInterval, handler.debounceInterval)
}

func TestConfigChangeHandler_StartAndStop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start handler in goroutine
	done := make(chan struct{})
	go func() {
		handler.Start(ctx)
		close(done)
	}()

	// Give handler time to start
	time.Sleep(testutil.StartupDelay)

	// Stop handler
	handler.Stop()

	// Verify handler stops gracefully
	select {
	case <-done:
		// Success
	case <-time.After(testutil.LongTimeout):
		t.Fatal("handler did not stop in time")
	}
}

func TestConfigChangeHandler_StartWithContextCancel(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		handler.Start(ctx)
		close(done)
	}()

	time.Sleep(testutil.StartupDelay)
	cancel()

	select {
	case <-done:
		// Success
	case <-time.After(testutil.LongTimeout):
		t.Fatal("handler did not stop in time after context cancel")
	}
}

func TestConfigChangeHandler_HandleConfigParsed_NoValidators(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// No validators configured
	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ConfigParsedEvent
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigParsedEvent(testConfig, nil, "v1", "sv1"))

	// Should immediately publish ConfigValidatedEvent (no validation needed)
	validated := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "v1", validated.Version)
	assert.Equal(t, testConfig, validated.Config)
}

func TestConfigChangeHandler_HandleConfigValidated_SignalController(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ConfigValidatedEvent (not initial)
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(testConfig, nil, "v2", "sv2"))

	// Should signal controller reinitialization
	select {
	case cfg := <-configCh:
		assert.Equal(t, testConfig, cfg)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("timeout waiting for config signal")
	}
}

func TestConfigChangeHandler_HandleConfigValidated_InitialVersion_SkipsSignal(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ConfigValidatedEvent with version="initial"
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(testConfig, nil, "initial", ""))

	// Should NOT signal controller (initial version is skipped)
	select {
	case <-configCh:
		t.Fatal("unexpected config signal for initial version")
	case <-time.After(testutil.NoEventTimeout):
		// Expected - no signal
	}
}

func TestConfigChangeHandler_HandleConfigValidated_InvalidConfigType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ConfigValidatedEvent with invalid config type
	bus.Publish(events.NewConfigValidatedEvent("not-a-config", nil, "v2", ""))

	// Should NOT signal controller (invalid type)
	select {
	case <-configCh:
		t.Fatal("unexpected config signal for invalid config type")
	case <-time.After(testutil.NoEventTimeout):
		// Expected - no signal
	}
}

func TestConfigChangeHandler_HandleConfigValidated_ChannelFull(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	// Channel with no buffer
	configCh := make(chan *coreconfig.Config)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ConfigValidatedEvent - should not block even if channel is full
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(testConfig, nil, "v2", ""))

	// Give it time to process - should not hang
	time.Sleep(testutil.NoEventTimeout)
}

func TestConfigChangeHandler_HandleBecameLeader_NoValidatedConfig(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish BecameLeaderEvent without any prior config
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))

	// Should NOT publish any ConfigValidatedEvent (no config cached)
	testutil.AssertNoEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestConfigChangeHandler_HandleBecameLeader_WithValidatedConfig(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// First, publish a ConfigValidatedEvent to cache it
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(testConfig, nil, "initial", ""))
	time.Sleep(testutil.DebounceWait)

	// Drain any events from the channel
	testutil.DrainChannel(eventChan)

	// Now publish BecameLeaderEvent
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))

	// Should re-publish the cached ConfigValidatedEvent
	validated := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "initial", validated.Version)
}

func TestConfigChangeHandler_StateCaching(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Initially no cached config
	handler.mu.RLock()
	assert.False(t, handler.hasValidatedConfig)
	assert.Nil(t, handler.lastConfigValidatedEvent)
	handler.mu.RUnlock()

	// Publish ConfigParsedEvent (with no validators, will be immediately validated)
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigParsedEvent(testConfig, nil, "v1", "sv1"))
	time.Sleep(testutil.DebounceWait)

	// Should now have cached config
	handler.mu.RLock()
	assert.True(t, handler.hasValidatedConfig)
	assert.NotNil(t, handler.lastConfigValidatedEvent)
	assert.Equal(t, "v1", handler.lastConfigValidatedEvent.Version)
	handler.mu.RUnlock()
}

func TestConfigChangeHandler_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish unrelated event - should not cause any issues
	bus.Publish(events.NewCredentialsUpdatedEvent(nil, "v1"))

	// Handler should continue running
	time.Sleep(testutil.DebounceWait)
}

func TestConfigChangeHandler_HandleConfigParsed_WithValidators_AllValid(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// Configure validators
	validators := []string{"basic", "template"}
	handler := NewConfigChangeHandler(bus, logger, configCh, validators, testDebounceInterval)

	// Subscribe to output events BEFORE bus.Start()
	eventChan := bus.Subscribe(50)

	// Subscribe mock validators BEFORE bus.Start()
	validatorChan := bus.Subscribe(50)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)

	// Start mock validators that respond to validation requests
	go func() {
		for event := range validatorChan {
			if req, ok := event.(*events.ConfigValidationRequest); ok {
				// Respond as "basic" validator
				bus.Publish(events.NewConfigValidationResponse(
					req.RequestID(),
					"basic",
					true,
					nil,
				))
				// Respond as "template" validator
				bus.Publish(events.NewConfigValidationResponse(
					req.RequestID(),
					"template",
					true,
					nil,
				))
				return
			}
		}
	}()

	time.Sleep(testutil.StartupDelay)

	// Publish ConfigParsedEvent
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigParsedEvent(testConfig, nil, "v1", "sv1"))

	// Should publish ConfigValidatedEvent
	validated := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.Equal(t, "v1", validated.Version)
}

func TestConfigChangeHandler_HandleConfigParsed_WithValidators_ValidationFailed(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// Configure validators
	validators := []string{"basic", "template"}
	handler := NewConfigChangeHandler(bus, logger, configCh, validators, testDebounceInterval)

	// Subscribe to output events BEFORE bus.Start()
	eventChan := bus.Subscribe(50)

	// Subscribe mock validators BEFORE bus.Start()
	validatorChan := bus.Subscribe(50)

	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)

	// Start mock validators - one fails
	go func() {
		for event := range validatorChan {
			if req, ok := event.(*events.ConfigValidationRequest); ok {
				// basic validator passes
				bus.Publish(events.NewConfigValidationResponse(
					req.RequestID(),
					"basic",
					true,
					nil,
				))
				// template validator fails
				bus.Publish(events.NewConfigValidationResponse(
					req.RequestID(),
					"template",
					false,
					[]string{"template syntax error"},
				))
				return
			}
		}
	}()

	time.Sleep(testutil.StartupDelay)

	// Publish ConfigParsedEvent
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigParsedEvent(testConfig, nil, "v1", "sv1"))

	// Should publish ConfigInvalidEvent
	invalid := testutil.WaitForEvent[*events.ConfigInvalidEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.Equal(t, "v1", invalid.Version)
	assert.Contains(t, invalid.ValidationErrors, "template")
}

func TestConfigChangeHandler_HandleConfigParsed_WithValidators_Timeout(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// Configure validators that will never respond
	validators := []string{"nonexistent"}
	handler := NewConfigChangeHandler(bus, logger, configCh, validators, testDebounceInterval)

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Use short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish ConfigParsedEvent
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigParsedEvent(testConfig, nil, "v1", "sv1"))

	// Should publish ConfigInvalidEvent due to timeout
	invalid := testutil.WaitForEvent[*events.ConfigInvalidEvent](t, eventChan, 15*time.Second)
	assert.Equal(t, "v1", invalid.Version)
	assert.Contains(t, invalid.ValidationErrors, "coordinator")
}

func TestConfigChangeHandler_HandleConfigParsed_WithValidators_MissingResponder(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// Configure validators - "missing" won't respond
	validators := []string{"basic", "missing"}
	handler := NewConfigChangeHandler(bus, logger, configCh, validators, testDebounceInterval)

	// Subscribe to output events BEFORE bus.Start()
	eventChan := bus.Subscribe(50)

	// Subscribe mock validators BEFORE bus.Start()
	validatorChan := bus.Subscribe(50)

	bus.Start()

	// Use short timeout context
	ctx, cancel := context.WithTimeout(context.Background(), testutil.EventTimeout)
	defer cancel()

	go handler.Start(ctx)

	// Start mock validator - only "basic" responds
	go func() {
		for event := range validatorChan {
			if req, ok := event.(*events.ConfigValidationRequest); ok {
				// Only basic validator responds
				bus.Publish(events.NewConfigValidationResponse(
					req.RequestID(),
					"basic",
					true,
					nil,
				))
				return
			}
		}
	}()

	time.Sleep(testutil.StartupDelay)

	// Publish ConfigParsedEvent
	testConfig := &coreconfig.Config{}
	bus.Publish(events.NewConfigParsedEvent(testConfig, nil, "v1", "sv1"))

	// Should publish ConfigInvalidEvent due to missing responder
	invalid := testutil.WaitForEvent[*events.ConfigInvalidEvent](t, eventChan, 15*time.Second)
	assert.Equal(t, "v1", invalid.Version)
	// Coordinator error due to missing responder
	assert.Contains(t, invalid.ValidationErrors, "coordinator")
}

func TestConfigChangeHandler_RapidConfigChangesDebounced(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 10)

	// Use longer debounce interval for reliable testing
	debounceInterval := 100 * time.Millisecond
	handler := NewConfigChangeHandler(bus, logger, configCh, nil, debounceInterval)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish 5 rapid config changes, each faster than the debounce interval
	for i := 1; i <= 5; i++ {
		cfg := &coreconfig.Config{}
		version := fmt.Sprintf("v%d", i)
		bus.Publish(events.NewConfigValidatedEvent(cfg, nil, version, ""))
		time.Sleep(20 * time.Millisecond) // Much less than debounce interval
	}

	// Wait for debounce to complete (debounce interval + buffer)
	time.Sleep(debounceInterval + 50*time.Millisecond)

	// Should receive exactly ONE signal (the last config)
	select {
	case <-configCh:
		// First signal received - expected
	default:
		t.Fatal("expected at least one signal after debounce")
	}

	// Verify no additional signals were sent
	select {
	case <-configCh:
		t.Fatal("expected only one signal due to debouncing, but got more")
	case <-time.After(50 * time.Millisecond):
		// Expected - no additional signals
	}
}

func TestConfigChangeHandler_DebounceTimerResetOnEachChange(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 10)

	debounceInterval := 80 * time.Millisecond
	handler := NewConfigChangeHandler(bus, logger, configCh, nil, debounceInterval)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish first config change
	cfg1 := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(cfg1, nil, "v1", ""))

	// Wait 50ms (less than debounce interval)
	time.Sleep(50 * time.Millisecond)

	// No signal should be sent yet
	select {
	case <-configCh:
		t.Fatal("signal sent too early - debounce not working")
	default:
		// Expected - still debouncing
	}

	// Publish second config change - this should reset the timer
	cfg2 := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(cfg2, nil, "v2", ""))

	// Wait another 50ms (total 100ms since first, but only 50ms since second)
	time.Sleep(50 * time.Millisecond)

	// Still no signal - timer was reset
	select {
	case <-configCh:
		t.Fatal("signal sent too early - debounce timer not reset properly")
	default:
		// Expected - still debouncing from second event
	}

	// Wait for the full debounce interval from the second event
	time.Sleep(debounceInterval)

	// Now we should have the signal
	select {
	case cfg := <-configCh:
		assert.Equal(t, cfg2, cfg, "should receive the last config")
	case <-time.After(50 * time.Millisecond):
		t.Fatal("expected signal after debounce completed")
	}
}

func TestConfigChangeHandler_CleanupWithPendingDebounce(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 10)

	// Use longer debounce to ensure we can stop before it fires
	debounceInterval := 500 * time.Millisecond
	handler := NewConfigChangeHandler(bus, logger, configCh, nil, debounceInterval)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		handler.Start(ctx)
		close(done)
	}()
	time.Sleep(testutil.StartupDelay)

	// Publish config change to start debounce timer
	cfg := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(cfg, nil, "v1", ""))

	// Wait a bit for the event to be processed
	time.Sleep(50 * time.Millisecond)

	// Cancel context while debounce is pending
	cancel()

	// Wait for handler to stop
	select {
	case <-done:
		// Handler stopped
	case <-time.After(testutil.LongTimeout):
		t.Fatal("handler did not stop in time")
	}

	// Verify no signal was sent (debounce was cancelled)
	select {
	case <-configCh:
		t.Fatal("signal should not be sent after shutdown")
	default:
		// Expected - no signal because handler stopped
	}

	// Wait longer than the original debounce interval
	time.Sleep(debounceInterval + 100*time.Millisecond)

	// Still no signal - timer was properly stopped
	select {
	case <-configCh:
		t.Fatal("signal should not be sent after shutdown - timer not stopped properly")
	default:
		// Expected - timer was cleaned up
	}
}

func TestConfigChangeHandler_DefaultDebounceInterval(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// Pass 0 to use default
	handler := NewConfigChangeHandler(bus, logger, configCh, nil, 0)

	assert.Equal(t, DefaultReinitDebounceInterval, handler.debounceInterval,
		"zero debounce interval should use default")
}

func TestConfigChangeHandler_NegativeDebounceInterval(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// Pass negative to use default
	handler := NewConfigChangeHandler(bus, logger, configCh, nil, -100*time.Millisecond)

	assert.Equal(t, DefaultReinitDebounceInterval, handler.debounceInterval,
		"negative debounce interval should use default")
}
