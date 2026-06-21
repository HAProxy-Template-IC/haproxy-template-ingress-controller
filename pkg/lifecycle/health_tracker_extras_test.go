// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package lifecycle

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The existing health_tracker_test.go covers the happy paths for both the
// activity-based and processing-based trackers in isolation. These extra
// tests pin three contracts that the existing suite leaves unexercised
// and which would each cause a silent failure mode in production:
//
//  1. NewProcessingTracker(name, 0) → the timeout MUST default to
//     DefaultProcessingTimeout. The internal checkStall short-circuits
//     when timeout == 0 (treating it as "check disabled"), so a
//     regression that dropped the default would mean every component
//     constructed with a zero timeout silently STOPS detecting stalls.
//     Events could hang for hours and HealthCheck() would still return
//     "healthy".
//
//  2. checkStall error format must surface BOTH the elapsed duration
//     AND the timeout it exceeded, plus the component name. Operators
//     paging on these errors need every field to know which component
//     stalled and by how much. A regression that dropped any field
//     would leave on-call without enough context to triage.
//
//  3. A tracker can be combined (both activity AND processing tracked)
//     by setting both timeout fields on the same instance. The doc
//     explicitly invites this ("can be used independently or
//     together"). The Check() call must report the FIRST stall it
//     finds — activity stall before processing stall — so a
//     long-stalled activity check isn't masked by an idle processing
//     state.
//
// Keep these tests timing-tolerant by using generous timeouts for the
// "healthy" path and short timeouts only for the "stalled" branch.

func TestNewProcessingTracker_ZeroTimeoutAppliesDefault(t *testing.T) {
	// Pass 0 — the constructor must substitute DefaultProcessingTimeout.
	tracker := NewProcessingTracker("svc", 0)

	// The contract is documented but not directly observable through
	// the public API. We verify it indirectly by asserting that the
	// processing check still trips on a stall: if 0 had been preserved,
	// checkStall would short-circuit on `timeout == 0` and Check()
	// would return nil even after an arbitrarily long processing
	// session. That would be the silent failure mode this test guards
	// against.
	tracker.StartProcessing()

	// Manipulate processingStart directly to simulate a long-running
	// stall without actually sleeping for 2 minutes. The internal
	// timeout must be > 0 for this assertion to be meaningful — if the
	// constructor regression kicked in (timeout left at 0), the
	// short-circuit would still report healthy.
	tracker.mu.Lock()
	tracker.processingStart = time.Now().Add(-DefaultProcessingTimeout - time.Second)
	tracker.mu.Unlock()

	err := tracker.Check()
	require.Error(t, err,
		"NewProcessingTracker(name, 0) MUST default to DefaultProcessingTimeout; "+
			"a regression that left timeout at 0 would silently disable the "+
			"processing-stall check (checkStall short-circuits on timeout==0) "+
			"and components would hang for hours while reporting healthy")
	assert.Contains(t, err.Error(), "svc stalled")
}

func TestHealthTracker_StallError_FormatHasComponentDurationAndTimeout(t *testing.T) {
	// The error message is the operator's primary signal during an
	// on-call page. Pin every field that must be present.
	tracker := NewActivityTracker("renderer", 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)

	err := tracker.Check()
	require.Error(t, err)

	msg := err.Error()
	// Component name (so operator knows WHICH component stalled).
	assert.Contains(t, msg, "renderer",
		"error must include the component name passed at construction; "+
			"without it operators don't know which component to investigate")
	// Slug identifying the stall TYPE (so operator knows which
	// tracking mode tripped).
	assert.Contains(t, msg, "no activity for",
		"error must identify the stall type (activity vs processing); "+
			"a single-format error would force operators to grep code "+
			"to know what kind of stall occurred")
	// Elapsed duration must be present in human-formatted form. We
	// match against a digits+unit pattern (e.g. "0s", "20ms", "1m30s")
	// rather than just the unit letter — "s" alone trivially matches
	// "stalled", "renderer", and most other words in the error.
	assert.Regexp(t, regexp.MustCompile(`\d+(\.\d+)?(ns|us|µs|ms|s|m|h)`), msg,
		"elapsed duration must be present and human-formatted; a regression "+
			"that dropped the duration entirely (or formatted it as a raw "+
			"nanosecond integer) would break operator triage")
	// Timeout value must be in the message so operators can compare
	// elapsed vs. configured timeout at a glance.
	assert.Contains(t, msg, "timeout:",
		"the configured timeout must appear so operators can see HOW LONG "+
			"the component had before being declared stalled — without it "+
			"a 60s stall on a 30s timeout looks the same as a 60s stall on "+
			"a 5min timeout, blocking quick triage")
}

func TestHealthTracker_Combined_ActivityStallSurfacesBeforeProcessing(t *testing.T) {
	// "Independently or together" — pin the combined-tracker scenario
	// with explicit ordering: activity check runs FIRST in Check(), so a
	// stalled activity timer must be reported even if the processing
	// state is idle (idle would otherwise short-circuit healthy).
	tracker := &HealthTracker{
		componentName:   "drift",
		activityTimeout: 5 * time.Millisecond,
		lastActivity:    time.Now().Add(-50 * time.Millisecond), // long-stalled
		processTimeout:  10 * time.Second,
		// processingStart left zero → idle; processing check would
		// independently report healthy, masking the activity stall if
		// Check() ran them in the wrong order.
	}

	err := tracker.Check()
	require.Error(t, err,
		"combined trackers must report any stall, regardless of which "+
			"check trips first; an idle processing state must NOT mask "+
			"a stalled activity timer")
	assert.Contains(t, err.Error(), "no activity for",
		"the activity-stall error must take precedence over a healthy "+
			"processing-idle state — Check() runs activity check first; "+
			"a regression that flipped the order would surface a "+
			"misleading 'processing for' message in some scenarios")
}

func TestHealthTracker_Combined_ProcessingStallSurfacesWhenActivityFresh(t *testing.T) {
	// Mirror of the previous test: a fresh activity timestamp must NOT
	// mask a stalled processing session. This pins that BOTH branches
	// of Check() are evaluated, not short-circuited on the first
	// healthy result.
	tracker := &HealthTracker{
		componentName:   "drift",
		activityTimeout: 10 * time.Second,
		lastActivity:    time.Now(), // fresh — would short-circuit if logic were OR'd wrong
		processTimeout:  5 * time.Millisecond,
		processingStart: time.Now().Add(-50 * time.Millisecond), // long-stalled
	}

	err := tracker.Check()
	require.Error(t, err,
		"a fresh activity timestamp must NOT mask a stalled processing "+
			"session — Check() must evaluate BOTH stall conditions, not "+
			"return healthy on the first one that passes")
	assert.Contains(t, err.Error(), "processing for",
		"with a stalled processing session the error must specifically "+
			"identify processing as the stall type, not activity")
}
