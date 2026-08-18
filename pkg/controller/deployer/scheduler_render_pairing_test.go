// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// The Coordinator publishes TemplateRenderedEvent and ValidationCompletedEvent as
// two independent Publish calls, and the bus drops per subscriber — so the
// scheduler can receive the verdict without the render it describes. Deploying
// that pair pushes the previous render's bytes with the current render's parsed
// config. These tests pin that the scheduler refuses the mismatched pair.

// seedRenderIdentity stamps an identity on a render cache that a test seeded by
// writing the scheduler's fields directly, and returns the correlation option
// that pairs a verdict with it.
//
// handleValidationCompleted only promotes a cache the verdict names, so a test
// that pokes lastRenderedConfig instead of routing a TemplateRenderedEvent must
// say which render it is pretending to have received.
func seedRenderIdentity(s *DeploymentScheduler) events.CorrelationOption {
	const renderID = "seeded-render-event-id"

	s.mu.Lock()
	s.lastRenderedEventID = renderID
	s.mu.Unlock()

	return events.WithCorrelation(renderID, renderID)
}

func renderedEvent(config string) *events.TemplateRenderedEvent {
	return events.NewTemplateRenderedEvent(
		config,
		&dataplane.AuxiliaryFiles{},
		nil, // statusPatches
		nil, // renderedResources
		0,   // auxFileCount
		1,   // durationMs
		"",  // triggerReason
		"checksum-"+config,
		nil, "", true, // coalescible
	)
}

func TestScheduler_ValidationForMissingRenderIsDiscarded(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	ctx := context.Background()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	scheduler.ctx = ctx

	// Render N-1 arrives and is cached.
	first := renderedEvent("global\n  daemon\n")
	scheduler.handleEvent(ctx, first)

	// Render N is dropped by the bus; only its verdict arrives.
	dropped := renderedEvent("global\n  daemon\n  nbthread 4\n")
	verdict := events.NewValidationCompletedEvent(nil, 10, "", nil, true,
		events.PropagateCorrelation(dropped))

	scheduler.handleEvent(ctx, verdict)

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	assert.False(t, scheduler.hasValidConfig,
		"a verdict describing a render the scheduler never received must not promote "+
			"the cached render — deploying it would push render N-1's bytes alongside "+
			"render N's parsed config, so the runtime diff is computed against a "+
			"config that is not the one being sent")
	assert.Empty(t, scheduler.lastValidatedConfig,
		"the mismatched verdict must leave the validated-config cache untouched")
}

func TestScheduler_ValidationForCachedRenderIsAccepted(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()

	ctx := context.Background()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)
	scheduler.ctx = ctx

	rendered := renderedEvent("global\n  daemon\n")
	scheduler.handleEvent(ctx, rendered)

	verdict := events.NewValidationCompletedEvent(nil, 10, "", nil, true,
		events.PropagateCorrelation(rendered))
	scheduler.handleEvent(ctx, verdict)

	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	require.True(t, scheduler.hasValidConfig,
		"the matching pair is the normal path and must still deploy")
	assert.Equal(t, "global\n  daemon\n", scheduler.lastValidatedConfig)
	assert.Equal(t, "checksum-global\n  daemon\n", scheduler.lastValidatedContentChecksum,
		"the checksum must travel with the config it describes")
}
