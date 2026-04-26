// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configchange

import (
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// SetInitialConfigVersion is the bootstrap-loop guard. It records
// the resourceVersion of the CRD fetched during startup so that the
// SAME version arriving later via the CRDWatcher informer's onAdd
// callback doesn't trigger reinitialization (which would loop
// forever: reinit → fresh fetch → onAdd of same version → reinit).
//
// Coverage was 0% on SetInitialConfigVersion AND the matching
// version-check branch in handleConfigValidated (line 341 of
// handler.go). Two contracts pinned by one composed test:
//
//  1. After SetInitialConfigVersion(v), a ConfigValidatedEvent
//     carrying that exact version v MUST NOT trigger the reinit
//     signal — even with reinit enabled and a real config payload.
//     A regression that dropped this guard would create the bootstrap
//     loop described above.
//
//  2. AFTER the bootstrap-skip, a ConfigValidatedEvent with a
//     DIFFERENT version MUST trigger the reinit signal as normal.
//     This is the asymmetry that makes the bootstrap guard
//     load-bearing rather than just a permanent disable.

func TestSetInitialConfigVersion_BlocksMatchingVersionThenAllowsOthers(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	configCh := make(chan *coreconfig.Config, 1)

	handler := NewConfigChangeHandler(bus, logger, configCh, nil, testDebounceInterval)
	bus.Start()

	ctx := t.Context()
	go handler.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Setup: simulate post-startup state — reinit is enabled and
	// the bootstrap version has been recorded.
	const bootstrapVersion = "v-bootstrap-7"
	handler.SetInitialConfigVersion(bootstrapVersion)
	handler.EnableReinitialization()

	// Step 1: Publish ConfigValidatedEvent with the BOOTSTRAP
	// version. The version-check guard MUST suppress the reinit
	// signal, even though reinit is enabled and the config is a
	// valid type.
	bus.Publish(events.NewConfigValidatedEvent(
		&coreconfig.Config{}, nil, bootstrapVersion, "",
	))

	select {
	case got := <-configCh:
		t.Fatalf("matching bootstrap version MUST NOT trigger reinit signal "+
			"(got config %v) — without this guard the CRDWatcher informer's "+
			"onAdd of the existing CRD at startup loops forever: reinit → "+
			"fresh fetch → onAdd of same version → reinit", got)
	case <-time.After(testDebounceInterval + testutil.NoEventTimeout):
		// Expected: signal suppressed by the version-check guard.
	}

	// Step 2: Publish ConfigValidatedEvent with a DIFFERENT
	// version. This MUST trigger the reinit signal — the bootstrap
	// guard must not be a permanent disable.
	const newVersion = "v-real-config-change-8"
	cfg := &coreconfig.Config{}
	bus.Publish(events.NewConfigValidatedEvent(cfg, nil, newVersion, ""))

	select {
	case got := <-configCh:
		if got != cfg {
			t.Fatalf("expected config pointer %p, got %p", cfg, got)
		}
		// Expected: signal fired for non-bootstrap version.
	case <-time.After(testDebounceInterval + testutil.LongTimeout):
		t.Fatal("non-bootstrap version MUST trigger reinit signal — the " +
			"version-check guard must only block the EXACT bootstrap version, " +
			"not become a permanent disable for all subsequent config changes")
	}
}
