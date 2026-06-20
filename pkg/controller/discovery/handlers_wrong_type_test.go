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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

// handleConfigValidated and handleCredentialsUpdated both perform a
// type-assertion at the top because the event payloads are `any`-typed
// (events package can't import the concrete config types without a
// circular dependency). The wrong-type guard is the load-bearing
// barrier that prevents:
//
//   - Promoting hasDataplanePort to true with an unparsed port
//     (would make tryInitialDiscovery fire with a port of 0,
//      causing every Dataplane API call to hit "127.0.0.1:0"
//      and fail in production)
//   - Promoting hasCredentials to true without real credentials
//     (would make tryInitialDiscovery fire with a nil-deref
//      inside the DataPlane API client)
//
// The existing component_test.go covers these via a real EventBus
// and the fake HAProxy executor installed in TestMain. This file
// additionally exercises the same guard at the pure unit level so
// the contract is pinned independently of the fake.

func TestHandleConfigValidated_WrongTypeLeavesStateUntouched(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	require.False(t, c.hasDataplanePort,
		"baseline: hasDataplanePort must start false for the negative assertion to be meaningful")
	require.Zero(t, c.dataplanePort,
		"baseline: dataplanePort must start zero so a regression that wrote a non-zero would be visible")

	// Pass a plain string instead of *coreconfig.Config.
	// NewConfigValidatedEvent's parameter is `any`; the receiver is
	// the only line of defense.
	evt := events.NewConfigValidatedEvent("not-a-config", nil, "v1", "secret-v1")

	require.NotPanics(t, func() { c.handleConfigValidated(evt) },
		"wrong-type ConfigValidatedEvent must NOT panic — the comma-ok "+
			"type assertion must catch the mismatch and log instead")

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.hasDataplanePort,
		"hasDataplanePort MUST stay false on the wrong-type guard — "+
			"a regression that flipped it to true would let tryInitialDiscovery "+
			"proceed with dataplanePort=0, hitting '127.0.0.1:0' on every "+
			"DataPlane API call in production")
	assert.Zero(t, c.dataplanePort,
		"dataplanePort MUST stay zero on the wrong-type branch — "+
			"a regression that read fields from a wrong-typed struct would either "+
			"panic on the type assertion or store zero-typed garbage")
	assert.Nil(t, c.discovery,
		"the Discovery instance MUST NOT be recreated on the wrong-type "+
			"branch — recreating it with a zero port would silently break "+
			"every subsequent triggerDiscovery call")
}

func TestHandleCredentialsUpdated_WrongTypeLeavesStateUntouched(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)
	require.False(t, c.hasCredentials,
		"baseline: hasCredentials must start false")
	require.Nil(t, c.credentials,
		"baseline: credentials must start nil")

	// NewCredentialsUpdatedEvent's first parameter is `any`. Pass
	// a map instead of *coreconfig.Credentials.
	evt := events.NewCredentialsUpdatedEvent(
		map[string]string{"username": "admin"},
		"secret-v1",
	)

	require.NotPanics(t, func() { c.handleCredentialsUpdated(evt) },
		"wrong-type CredentialsUpdatedEvent must NOT panic — the comma-ok "+
			"type assertion must catch the mismatch and log instead")

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.hasCredentials,
		"hasCredentials MUST stay false on the wrong-type guard — "+
			"a regression that flipped it to true with no real credentials "+
			"available would let tryInitialDiscovery proceed and nil-deref "+
			"inside the DataPlane API client (which dereferences the "+
			"credentials struct without a guard)")
	assert.Nil(t, c.credentials,
		"credentials MUST stay nil on the wrong-type branch — a regression "+
			"that stored the wrong-typed value would set up a downstream "+
			"type-assertion panic in triggerDiscovery, far from the original "+
			"event-producer bug")
}
