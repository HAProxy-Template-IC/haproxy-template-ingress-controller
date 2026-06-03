// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// tryInitialDiscovery is the gate that ensures EXACTLY ONE initial
// discovery happens after all four startup-time conditions are met.
// Three different handlers (ConfigValidated, CredentialsUpdated,
// ResourceSyncComplete) race to call it, and the function must:
//
//  1. Idempotency: once initialDiscoveryDone == true, every
//     subsequent call must be a no-op (otherwise duplicate discovery
//     calls would race and produce inconsistent endpoint sets).
//  2. Wait for sync: !initialSyncComplete must skip (firing before
//     the pod store has its initial set would discover an empty
//     endpoint list and propagate it as the truth).
//  3. Wait for credentials: !hasCredentials must skip (discovery
//     hits the DataPlane API which requires creds).
//  4. Wait for config: !hasDataplanePort must skip (we don't know
//     which port to hit).
//  5. Wait for pod store: podStore == nil must skip (nothing to
//     iterate).
//
// All five guards must hold, in any order. A regression that dropped
// any one would fire discovery prematurely and produce silent bugs:
// a discovery without credentials would fail on the API call; a
// discovery before sync would publish an empty endpoint list as
// "truth" and confuse downstream consumers.
//
// The "happy path" (all five conditions met → triggerDiscovery)
// requires real Discovery wiring with DetectLocalVersion shelling
// out to a real haproxy binary, which is out of scope for a unit
// test. The five SKIP branches are testable by leaving one
// condition unmet at a time, building a Component without a real
// Discovery, and asserting initialDiscoveryDone stays false.

// minimalComponentForGuardTest builds a Component populated only
// with the fields tryInitialDiscovery's guards inspect. The
// discovery field is left nil; every test below verifies the
// function returns BEFORE reaching triggerDiscovery (which would
// nil-deref discovery).
func minimalComponentForGuardTest(t *testing.T) *Component {
	t.Helper()
	_, logger := testutil.NewTestBusAndLogger()
	return &Component{
		logger: logger,
	}
}

// fakePodStoreForGuard is the tiniest types.Store implementation
// needed to satisfy the "podStore != nil" guard test. It is never
// queried because the test verifies tryInitialDiscovery skips
// BEFORE triggerDiscovery runs.
type fakePodStoreForGuard struct{}

func (fakePodStoreForGuard) Get(_ ...string) ([]any, error) { return nil, nil }
func (fakePodStoreForGuard) List() ([]any, error)           { return nil, nil }
func (fakePodStoreForGuard) Add(_ any, _ []string) error    { return nil }
func (fakePodStoreForGuard) Update(_ any, _ []string) error { return nil }
func (fakePodStoreForGuard) Delete(_ ...string) error       { return nil }
func (fakePodStoreForGuard) Clear() error                   { return nil }

func TestComponent_TryInitialDiscovery_SkipsWhenAlreadyDone(t *testing.T) {
	c := minimalComponentForGuardTest(t)
	// Set initialDiscoveryDone = true. Even if every other guard
	// is also true, the function MUST be a no-op — not call
	// triggerDiscovery (which would nil-deref c.discovery).
	c.initialDiscoveryDone = true
	c.initialSyncComplete = true
	c.hasCredentials = true
	c.hasDataplanePort = true
	c.credentials = &coreconfig.Credentials{}
	c.podStore = fakePodStoreForGuard{}

	require.NotPanics(t, func() { c.tryInitialDiscovery("test") },
		"already-done state must short-circuit BEFORE touching discovery; "+
			"a regression that re-entered would either panic on nil discovery or fire a duplicate discovery")

	// The flag stays true (idempotent — must not flip back).
	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.True(t, c.initialDiscoveryDone,
		"already-done flag must not flip back to false on a re-entry")
}

func TestComponent_TryInitialDiscovery_SkipsWhenSyncIncomplete(t *testing.T) {
	c := minimalComponentForGuardTest(t)
	// Every other guard true, sync incomplete.
	c.initialSyncComplete = false
	c.hasCredentials = true
	c.hasDataplanePort = true
	c.credentials = &coreconfig.Credentials{}
	c.podStore = fakePodStoreForGuard{}

	require.NotPanics(t, func() { c.tryInitialDiscovery("test") })

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.initialDiscoveryDone,
		"discovery must NOT proceed before initialSyncComplete; "+
			"firing early would publish an empty endpoint set as 'truth' and mislead downstream consumers")
}

func TestComponent_TryInitialDiscovery_SkipsWhenNoCredentials(t *testing.T) {
	c := minimalComponentForGuardTest(t)
	c.initialSyncComplete = true
	c.hasCredentials = false
	c.hasDataplanePort = true
	c.podStore = fakePodStoreForGuard{}

	require.NotPanics(t, func() { c.tryInitialDiscovery("test") })

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.initialDiscoveryDone,
		"discovery requires credentials to authenticate against the DataPlane API; "+
			"firing without credentials would 401 every endpoint")
}

func TestComponent_TryInitialDiscovery_SkipsWhenNoDataplanePort(t *testing.T) {
	c := minimalComponentForGuardTest(t)
	c.initialSyncComplete = true
	c.hasCredentials = true
	c.hasDataplanePort = false
	c.credentials = &coreconfig.Credentials{}
	c.podStore = fakePodStoreForGuard{}

	require.NotPanics(t, func() { c.tryInitialDiscovery("test") })

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.initialDiscoveryDone,
		"discovery requires a known DataPlane port (set from config); "+
			"firing without it would either use a wrong port or zero-port crash")
}

func TestComponent_TryInitialDiscovery_SkipsWhenNoPodStore(t *testing.T) {
	c := minimalComponentForGuardTest(t)
	c.initialSyncComplete = true
	c.hasCredentials = true
	c.hasDataplanePort = true
	c.credentials = &coreconfig.Credentials{}
	c.podStore = nil // explicit

	require.NotPanics(t, func() { c.tryInitialDiscovery("test") })

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.initialDiscoveryDone,
		"discovery requires a podStore to iterate; "+
			"firing with a nil store would nil-deref inside DiscoverEndpoints")
}

// Synthetic test: ensure the "missing requirements" branch is the
// composite OR of the three credential / port / store guards. A
// regression that AND-ed the conditions instead of OR-ing the
// negations would fire discovery if any single requirement was met.
func TestComponent_TryInitialDiscovery_SkipsOnAnyMissingRequirement(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Component)
	}{
		{
			name: "only credentials missing",
			mut:  func(c *Component) { c.hasCredentials = false },
		},
		{
			name: "only port missing",
			mut:  func(c *Component) { c.hasDataplanePort = false },
		},
		{
			name: "only podStore missing",
			mut:  func(c *Component) { c.podStore = nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := minimalComponentForGuardTest(t)
			// Start from the "all met" baseline, then knock out one.
			c.initialSyncComplete = true
			c.hasCredentials = true
			c.hasDataplanePort = true
			c.credentials = &coreconfig.Credentials{}
			c.podStore = fakePodStoreForGuard{}
			tt.mut(c)

			c.tryInitialDiscovery("test-mut")
			c.mu.RLock()
			defer c.mu.RUnlock()
			assert.False(t, c.initialDiscoveryDone,
				"any single missing requirement must skip the discovery")
		})
	}
}
