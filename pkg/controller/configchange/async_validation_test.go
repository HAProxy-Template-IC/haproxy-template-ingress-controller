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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// TestConfigChangeHandler_BecameLeaderReplay_NotBlockedByInFlightValidation
// pins the issue #55 publish-stall mechanism (CI jobs 15179436894,
// 15180658393, 15180832368): after a CRD-triggered reinitialization, the new
// iteration's config validation runs 13-15s+ (the validationtests validator
// executes the config's entire embedded suite). When leadership was acquired
// during that window, the BecameLeaderEvent sat queued behind the event loop
// — which was blocked inside the validation scatter-gather — so the state
// replay that hands the last validated config to the freshly-subscribed
// leader-only config-publisher never ran. The publisher dropped every
// HAProxyCfg publish with "Cannot publish configuration, missing cached
// state" until validation completed, blowing the e2e convergence budget
// (waitForControllerDeployed: marker never observed in spec.content).
//
// The pin: with a validation deliberately held in flight (a required
// validator that never responds), a BecameLeaderEvent must still be replayed
// promptly. Before the async-validation fix this timed out (the replay
// would only happen after the 45s scatter-gather timeout).
func TestConfigChangeHandler_BecameLeaderReplay_NotBlockedByInFlightValidation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	// One expected validator that never responds: the scatter-gather stays in
	// flight for the whole test (stand-in for a long validationtests run).
	handler := NewConfigChangeHandler(bus, logger, configCh, []string{"slow"}, testDebounceInterval)

	eventChan := bus.Subscribe("test-sub", 50)
	validatorChan := bus.Subscribe("test-validator", 50)
	bus.Start()

	go handler.Start(t.Context())
	time.Sleep(testutil.StartupDelay)

	// Cache a validated config for leadership replay (production equivalent:
	// the synthetic bootstrap ConfigValidatedEvent every iteration publishes
	// at startup). Consume its delivery on eventChan so the replay below is
	// unambiguously a SECOND publish of the same version.
	cached := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(cached, nil, "initial", ""))
	first := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "initial", first.Version)

	// Start a validation that will not complete within the test.
	bus.Publish(events.NewConfigParsedEvent(&coreconfig.Config{}, nil, "v2", "sv2"))
	testutil.WaitForEvent[*events.ConfigValidationRequest](t, validatorChan, testutil.LongTimeout)

	// The validation is now in flight. The loop must still process side
	// events: leadership acquisition must replay the cached config promptly,
	// not after the scatter-gather finishes.
	bus.Publish(events.NewBecameLeaderEvent("test-identity"))

	replayed := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "initial", replayed.Version,
		"BecameLeaderEvent must replay the cached validated config while a validation is in flight")
}

// TestConfigChangeHandler_InFlightValidation_SupersededParsedCoalesced pins
// the single-flight + latest-wins semantics of asynchronous validation:
// parsed configs arriving while a validation is in flight are parked (newest
// wins), exactly one validation runs at a time, and a superseded config is
// never validated — so ConfigValidatedEvents keep their publish order and no
// validator cycle is wasted on a config that has already been replaced.
func TestConfigChangeHandler_InFlightValidation_SupersededParsedCoalesced(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, []string{"gated"}, testDebounceInterval)

	eventChan := bus.Subscribe("test-sub", 50)
	validatorChan := bus.Subscribe("test-validator", 50)
	bus.Start()

	go handler.Start(t.Context())

	// Mock validator: holds the FIRST request until the gate opens, then
	// responds valid; every later request is answered immediately.
	gate := make(chan struct{})
	firstReceived := make(chan struct{})
	go func() {
		first := true
		for event := range validatorChan {
			req, ok := event.(*events.ConfigValidationRequest)
			if !ok {
				continue
			}
			if first {
				first = false
				close(firstReceived)
				<-gate
			}
			bus.Publish(events.NewConfigValidationResponse(req.RequestID(), "gated", true, nil))
		}
	}()

	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewConfigParsedEvent(&coreconfig.Config{}, nil, "v1", "sv1"))
	select {
	case <-firstReceived:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("timeout waiting for the first validation request to reach the validator")
	}

	// v1's validation is now in flight and gated. Two newer configs arrive;
	// v2 is superseded by v3 before any validation slot frees up.
	bus.Publish(events.NewConfigParsedEvent(&coreconfig.Config{}, nil, "v2", "sv2"))
	bus.Publish(events.NewConfigParsedEvent(&coreconfig.Config{}, nil, "v3", "sv3"))
	time.Sleep(testutil.StartupDelay) // let the loop park them (latest wins)

	close(gate)

	got1 := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.Equal(t, "v1", got1.Version, "the in-flight validation must complete first")

	got2 := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.Equal(t, "v3", got2.Version, "only the latest parked config is validated after the in-flight one")

	// The superseded v2 must never produce a ConfigValidatedEvent.
	testutil.AssertNoEvent[*events.ConfigValidatedEvent](t, eventChan, testutil.NoEventTimeout)
}
