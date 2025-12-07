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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/testutil"
	coreconfig "haproxy-template-ic/pkg/core/config"
)

func TestNewConfigChangeHandler(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)
	validators := []string{"basic", "template"}

	handler := NewConfigChangeHandler(bus, logger, configCh, validators)

	require.NotNil(t, handler)
	assert.Equal(t, bus, handler.bus)
	assert.Equal(t, logger, handler.logger)
	// Can't directly compare bidirectional channel to send-only channel, just verify it's set
	assert.NotNil(t, handler.configChangeCh)
	assert.Equal(t, validators, handler.validators)
	assert.NotNil(t, handler.stopCh)
}

func TestConfigChangeHandler_StartAndStop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)
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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)
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
	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)

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

	handler := NewConfigChangeHandler(bus, logger, configCh, nil)
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
	handler := NewConfigChangeHandler(bus, logger, configCh, validators)

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
	handler := NewConfigChangeHandler(bus, logger, configCh, validators)

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
	handler := NewConfigChangeHandler(bus, logger, configCh, validators)

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
	handler := NewConfigChangeHandler(bus, logger, configCh, validators)

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
