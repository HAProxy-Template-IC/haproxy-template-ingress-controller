// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// responseCollector is the gather-phase state machine for the
// scatter-gather Request() pattern. It tracks which responders have
// replied, deduplicates duplicate responders, and signals completion
// when minResponses is reached.
//
// The end-to-end Request() path is well-tested in bus_test.go, but the
// internal addResponse / result methods have several pure-logic
// branches that the integration tests don't exercise individually:
//
//   1. completed=true → addResponse no-ops (idempotent against late
//      duplicate responses arriving after we've already signaled done).
//
//   2. Duplicate responder name → response is ignored, NOT counted
//      twice. This is what protects against components that
//      accidentally publish two responses to the same request (e.g.
//      a retry inside a handler) from prematurely triggering
//      "minResponses reached" with only one unique responder.
//
//   3. minResponses reached → done channel is closed exactly once.
//      A regression that closed it twice would panic; one that never
//      closed it would deadlock the caller.
//
//   4. result() returns errors for missing expected responders,
//      independent of how addResponse classified them. A regression
//      that didn't track expected vs received separately would
//      surface here as missing or stale errors.

// stubResponse is a minimal Response implementation for testing
// addResponse / result without dragging in domain event types.
type stubResponse struct {
	requestID string
	responder string
}

func (s stubResponse) EventType() string    { return "stub.response" }
func (s stubResponse) Timestamp() time.Time { return time.Time{} }
func (s stubResponse) RequestID() string    { return s.requestID }
func (s stubResponse) Responder() string    { return s.responder }

func newCollector(reqID string, expected []string, minResponses int) *responseCollector {
	return &responseCollector{
		requestID:          reqID,
		expectedResponders: expected,
		minResponses:       minResponses,
		responders:         make(map[string]bool, len(expected)),
		done:               make(chan struct{}),
	}
}

func TestResponseCollector_AddResponseSignalsDoneAtMinResponses(t *testing.T) {
	c := newCollector("req-1", []string{"a", "b", "c"}, 2)

	// First response: not enough yet.
	c.addResponse(stubResponse{requestID: "req-1", responder: "a"})
	select {
	case <-c.done:
		t.Fatal("done channel must NOT be closed after just 1 response when " +
			"minResponses=2 — a regression that closed early would let " +
			"Request() return before the second responder replied")
	default:
		// expected
	}

	// Second response: hit minResponses → done MUST close.
	c.addResponse(stubResponse{requestID: "req-1", responder: "b"})
	select {
	case <-c.done:
		// expected
	default:
		t.Fatal("done channel MUST be closed when minResponses is reached — " +
			"a regression that failed to close would deadlock the caller " +
			"in Request() until timeout")
	}

	assert.True(t, c.completed,
		"completed flag must be set when done is closed; subsequent addResponse "+
			"calls rely on this flag for idempotency")
}

func TestResponseCollector_DuplicateResponderIsIgnored(t *testing.T) {
	c := newCollector("req-1", []string{"a", "b"}, 2)

	// Same responder twice — must NOT count as 2 distinct responders
	// (otherwise a retry-inside-handler bug would prematurely satisfy
	// minResponses with only one component having actually responded).
	c.addResponse(stubResponse{requestID: "req-1", responder: "a"})
	c.addResponse(stubResponse{requestID: "req-1", responder: "a"})

	select {
	case <-c.done:
		t.Fatal("done channel must NOT close after two responses from the SAME " +
			"responder — a regression that double-counted would prematurely " +
			"complete with incomplete coverage of expected responders, hiding " +
			"a real outage from one of them")
	default:
		// expected
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	assert.Equal(t, 1, len(c.responses),
		"only the FIRST response from each unique responder must be recorded")
	assert.True(t, c.responders["a"],
		"the responder name must be tracked in the map for dedup")
}

func TestResponseCollector_AddResponseAfterCompletionIsNoOp(t *testing.T) {
	c := newCollector("req-1", []string{"a", "b"}, 2)

	// Reach completion.
	c.addResponse(stubResponse{requestID: "req-1", responder: "a"})
	c.addResponse(stubResponse{requestID: "req-1", responder: "b"})
	require.True(t, c.completed)

	// A late response arrives — must be silently ignored, NOT panic
	// from a second close(done) and NOT added to the responses slice.
	require.NotPanics(t, func() {
		c.addResponse(stubResponse{requestID: "req-1", responder: "c"})
	}, "addResponse after completion MUST be idempotent — late responses "+
		"arriving after timeout/completion must NOT cause a second close(done) "+
		"panic, which would crash the bus")

	c.mu.Lock()
	defer c.mu.Unlock()
	assert.Equal(t, 2, len(c.responses),
		"the post-completion response must NOT be added to the responses "+
			"slice — only the first minResponses are recorded")
	assert.False(t, c.responders["c"],
		"the late responder must NOT pollute the dedup map either")
}

func TestResponseCollector_ResultListsMissingExpectedResponders(t *testing.T) {
	c := newCollector("req-1", []string{"basic", "template", "jsonpath"}, 2)

	// Two responders reply, "jsonpath" does not.
	c.addResponse(stubResponse{requestID: "req-1", responder: "basic"})
	c.addResponse(stubResponse{requestID: "req-1", responder: "template"})

	res := c.result()

	require.Len(t, res.Responses, 2,
		"sanity: both received responses appear in the result")
	require.Len(t, res.Errors, 1,
		"the missing 'jsonpath' responder must produce exactly one error — "+
			"a regression that didn't compare expected vs received would either "+
			"miss this error (silently accepting incomplete validation) or "+
			"emit duplicates")
	assert.Contains(t, res.Errors[0], "jsonpath",
		"the error message must name the missing responder so the operator "+
			"sees exactly which validator failed to reply")
	assert.Contains(t, res.Errors[0], "no response from",
		"the error format must use the documented 'no response from <name>' "+
			"phrase so log scrapers can grep for it")
}

func TestResponseCollector_ResultEmptyExpectedRespondersProducesNoErrors(t *testing.T) {
	// When ExpectedResponders is empty, MinResponses-only mode is in
	// use — there's no concept of "missing" responders. result() must
	// return a non-nil but empty Errors slice (callers iterate it).
	c := newCollector("req-1", nil, 1)
	c.addResponse(stubResponse{requestID: "req-1", responder: "a"})

	res := c.result()

	assert.Len(t, res.Responses, 1)
	assert.NotNil(t, res.Errors,
		"Errors must be a non-nil empty slice — a regression that returned "+
			"nil would force every caller to nil-check before iterating, "+
			"breaking the documented 'iterate Errors' pattern")
	assert.Empty(t, res.Errors,
		"with no ExpectedResponders, missing-responder errors are conceptually "+
			"impossible — must be empty")
}

func TestResponseCollector_ListenIgnoresUnrelatedRequestIDs(t *testing.T) {
	// listen() filters by requestID — responses for OTHER requests
	// must be ignored entirely (not just deduped, not counted).
	c := newCollector("req-mine", []string{"a"}, 1)

	eventCh := make(chan Event, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Inject a wrong-requestID response THEN the correct one. The
	// wrong one must be ignored; the correct one must complete.
	eventCh <- stubResponse{requestID: "req-other", responder: "x"}
	eventCh <- stubResponse{requestID: "req-mine", responder: "a"}

	go c.listen(ctx, eventCh)

	select {
	case <-c.done:
		// expected
	case <-ctx.Done():
		t.Fatal("listen must process the matching response within the context " +
			"deadline — a regression that mis-filtered would either ignore " +
			"the matching response (timeout) or accept the wrong-ID one " +
			"(complete with bad data)")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	require.Len(t, c.responses, 1,
		"only the matching-ID response must be recorded; the unrelated one "+
			"must NOT pollute the responses slice")
	assert.Equal(t, "a", c.responses[0].Responder(),
		"the recorded response must be the matching one, not the unrelated one")
}
