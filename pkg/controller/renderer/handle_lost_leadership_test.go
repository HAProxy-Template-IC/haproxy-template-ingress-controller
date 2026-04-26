// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package renderer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// handleLostLeadership is the leadership-transition cleanup hook for
// the renderer's content-deduplication cache. The renderer suppresses
// duplicate TemplateRenderedEvents by hashing rendered output and
// comparing against lastRenderedChecksum — without clearing this
// cache on leadership loss, the next leader's first render would
// short-circuit as "unchanged" and never publish the
// TemplateRenderedEvent that downstream leader-only components
// (validator, deployer scheduler) need to start their pipelines.
//
// Two branches; both are critical:
//
//  1. Cache empty (lastRenderedChecksum == "") → no-op. The
//     log message must NOT fire on every leadership event during
//     startup churn or in unit tests where no render has happened
//     yet — that would create misleading "cleared cache" log spam.
//
//  2. Cache populated → cache cleared. This is the load-bearing
//     contract: the next render after leadership regain MUST publish
//     a TemplateRenderedEvent (no skip-as-duplicate) to wake up
//     leader-only downstream components. A regression that left
//     the checksum populated would silently stick the pipeline
//     after every leadership transition.
//
// The function only touches lastRenderedChecksum and the logger;
// no event bus or rendering setup needed. We can construct a
// minimal Component directly.

func TestHandleLostLeadership_EmptyChecksumIsNoOp(t *testing.T) {
	c := &Component{
		logger:               testutil.NewTestLogger(),
		lastRenderedChecksum: "", // baseline state
	}

	// Function must be a pure no-op when there's nothing to clear.
	require.NotPanics(t, func() {
		c.handleLostLeadership(events.NewLostLeadershipEvent("test-pod", "test-reason"))
	})

	assert.Empty(t, c.lastRenderedChecksum,
		"empty cache must remain empty — defensive sanity check")
}

func TestHandleLostLeadership_PopulatedChecksumIsCleared(t *testing.T) {
	const populatedChecksum = "abc123def456 — last successful render"
	c := &Component{
		logger:               testutil.NewTestLogger(),
		lastRenderedChecksum: populatedChecksum,
	}

	c.handleLostLeadership(events.NewLostLeadershipEvent("test-pod", "test-reason"))

	assert.Empty(t, c.lastRenderedChecksum,
		"lastRenderedChecksum MUST be cleared on leadership loss — "+
			"a regression that left it populated would silently stall the "+
			"pipeline after every leadership transition: the next render "+
			"would short-circuit as 'unchanged' and never publish the "+
			"TemplateRenderedEvent that downstream leader-only components "+
			"(validator, deployer scheduler) wait for")
}

// Idempotent: a second LostLeadershipEvent in a row (which can
// happen during rapid leader churn) must keep the cache cleared and
// not re-fire the log message side effect — assert state stays
// empty across multiple invocations.
func TestHandleLostLeadership_IsIdempotentAcrossMultipleEvents(t *testing.T) {
	c := &Component{
		logger:               testutil.NewTestLogger(),
		lastRenderedChecksum: "first-render-checksum",
	}

	for i := range 3 {
		c.handleLostLeadership(events.NewLostLeadershipEvent("test-pod", "test-reason"))
		assert.Empty(t, c.lastRenderedChecksum,
			"after invocation #%d, the cache must remain empty — "+
				"idempotency is required because rapid leader churn can "+
				"deliver back-to-back LostLeadershipEvents and any "+
				"behaviour difference between first and Nth invocation "+
				"would surface as a flaky leadership-transition regression",
			i+1)
	}
}
