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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// Discovery is the only writer of the deployer's endpoint set, and every path
// into it is an edge: a dropped haproxy-pods ResourceIndexUpdatedEvent has no
// successor. The drift tick is the level backstop that re-reads the pod store,
// so a pod missed by a dropped event still gets config within the drift interval
// instead of waiting for an unrelated pod, config, or leadership change.
//
// Handlers are driven directly rather than through the event loop: HandleEvent
// is synchronous, so the assertions need no sleeps.

func TestComponent_DriftPreventionReDrivesDiscovery(t *testing.T) {
	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)
	component.SetPodStore(createTestPodStore(t, []string{"127.0.0.1"}))

	eventChan := bus.Subscribe("test-sub", 20)
	bus.Start()

	component.HandleEvent(events.NewCredentialsUpdatedEvent(&coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "secret",
	}, "v1"))
	component.HandleEvent(events.NewResourceSyncCompleteEvent(names.HAProxyPodsResourceType, 1))
	component.HandleEvent(events.NewConfigValidatedEvent(&coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{Port: 5555},
	}, nil, "v1", "v1"))

	first := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.VeryLongTimeout)
	require.NotNil(t, first, "initial discovery must complete before the backstop is meaningful")

	// The haproxy-pods index update that would normally announce the change is
	// dropped by the bus. The drift tick must re-read the store regardless.
	component.HandleEvent(events.NewDriftPreventionTriggeredEvent(60 * time.Second))

	again := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.VeryLongTimeout)
	assert.NotNil(t, again,
		"the drift tick must re-run discovery from the pod store — without it a dropped "+
			"haproxy-pods index update is terminal and the new pod never receives config")
}

func TestComponent_DriftPreventionBeforeInitialDiscoveryIsIgnored(t *testing.T) {
	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)
	component.SetPodStore(createTestPodStore(t, []string{"127.0.0.1"}))

	eventChan := bus.Subscribe("test-sub", 20)
	bus.Start()

	// No credentials, no dataplane port, no initial discovery yet.
	component.HandleEvent(events.NewDriftPreventionTriggeredEvent(60 * time.Second))

	// A drift tick before the component has credentials and a port must not
	// publish an endpoint set — startup ordering owns the first discovery.
	testutil.AssertNoEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChan, testutil.NoEventTimeout)
}
