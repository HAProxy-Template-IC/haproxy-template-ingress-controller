// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StartLeaderOnlyComponentsAsync is the entry point for the EventBus
// Pause/Start pattern: the caller waits for this to return BEFORE calling
// EventBus.Start() so leader-only components are already subscribed when
// buffered events get replayed. Three contracts protect that pattern:
//
//  1. Empty set fast-path — when no leader-only components exist, the
//     returned errCh must be ALREADY CLOSED (so callers ranging over it
//     don't block) and the error must be nil. A regression that returned
//     a nil channel here would deadlock every caller doing
//     `for err := range errCh { … }`.
//
//  2. Subscription-ready barrier — when leader-only components implement
//     SubscriptionReadySignaler, the function MUST NOT return until ALL
//     of them have signaled. Returning early would race with EventBus
//     replay and let leader-only components miss the very events they
//     just paused for.
//
//  3. Error propagation through errCh — non-canceled errors from a
//     leader-only Start() must arrive on errCh; canceled errors must
//     NOT (canceled is normal shutdown and should be silent for the
//     caller's error-tracking goroutine).

// signalingMock is a Component that also implements SubscriptionReadySignaler,
// matching the production pattern documented in pkg/lifecycle/component.go.
// It signals subscription-ready when subRelease is closed (or immediately
// if subAutoSignal is true).
type signalingMock struct {
	mockComponent
	subReady      chan struct{}
	subRelease    chan struct{} // gate that holds subscription-ready until released
	subAutoSignal bool          // if true, signal as soon as Start() runs
}

// newSignalingMock returns a signaling mock named "leader-only" — every test
// in this file exercises exactly one leader-only component under that name.
func newSignalingMock() *signalingMock {
	return &signalingMock{
		mockComponent: mockComponent{name: "leader-only", startedChan: make(chan struct{})},
		subReady:      make(chan struct{}),
		subRelease:    make(chan struct{}),
	}
}

// SubscriptionReady implements lifecycle.SubscriptionReadySignaler.
func (s *signalingMock) SubscriptionReady() <-chan struct{} {
	return s.subReady
}

// Start overrides mockComponent.Start to gate the ready signal so the test
// can observe whether StartLeaderOnlyComponentsAsync waits for it.
func (s *signalingMock) Start(ctx context.Context) error {
	s.mu.Lock()
	s.started = true
	if s.startedChan != nil {
		select {
		case <-s.startedChan:
		default:
			close(s.startedChan)
		}
	}
	s.mu.Unlock()

	// Either auto-signal or wait for the test to release.
	if s.subAutoSignal {
		close(s.subReady)
	} else {
		go func() {
			select {
			case <-s.subRelease:
				close(s.subReady)
			case <-ctx.Done():
			}
		}()
	}

	if s.startErr != nil {
		return s.startErr
	}

	<-ctx.Done()
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	return nil
}

func TestStartLeaderOnlyComponentsAsync_EmptySetReturnsClosedChannel(t *testing.T) {
	// No leader-only components registered → returned errCh MUST be
	// already-closed so callers ranging over it don't block forever.
	registry := NewRegistry()
	registry.Register(newMockComponent("all-replica"))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh, err := registry.StartLeaderOnlyComponentsAsync(ctx)
	require.NoError(t, err,
		"empty leader-only set must not error — there's nothing to start")
	require.NotNil(t, errCh,
		"errCh MUST be non-nil even in the empty-set fast path; "+
			"a nil channel would deadlock every caller doing `for err := range errCh`")

	// Should be already closed → receive returns zero value immediately.
	select {
	case got, ok := <-errCh:
		assert.False(t, ok,
			"errCh MUST be closed (not just empty) so callers see the EOF; "+
				"a regression that left it open would deadlock callers waiting "+
				"on `for err := range errCh`")
		assert.Nil(t, got,
			"closed channel receive must yield nil error")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("errCh from empty leader-only set blocked instead of being closed — " +
			"regression in the empty-set fast path")
	}
}

func TestStartLeaderOnlyComponentsAsync_WaitsForSubscriptionReady(t *testing.T) {
	// The function MUST NOT return until ALL leader-only components
	// implementing SubscriptionReadySignaler have closed their ready
	// channel. This is the contract that lets the caller safely call
	// EventBus.Start() afterward without leader-only components missing
	// events that would otherwise be in the pre-Start buffer.
	registry := NewRegistry()
	registry.Register(newMockComponent("all-replica"))

	leader := newSignalingMock()
	registry.Register(leader, LeaderOnly())

	// Start all-replica components first (not as leader yet).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = registry.StartAll(ctx, false)
	}()

	// Now call the leader-only async path. This goroutine is what we
	// will time the return of.
	returnCh := make(chan struct{})
	var asyncErr error
	var errCh <-chan error
	go func() {
		errCh, asyncErr = registry.StartLeaderOnlyComponentsAsync(ctx)
		close(returnCh)
	}()

	// Give the async startup a moment to begin. It MUST still be
	// blocked because we haven't released subscription-ready yet.
	select {
	case <-returnCh:
		t.Fatal("StartLeaderOnlyComponentsAsync returned BEFORE subscription-ready " +
			"signaled — a regression here would race EventBus.Start() and let " +
			"leader-only components miss buffered events")
	case <-time.After(100 * time.Millisecond):
		// expected — function is blocked waiting for ready signal
	}

	// Now release the subscription-ready signal.
	close(leader.subRelease)

	// Within a short window, async returns nil error.
	select {
	case <-returnCh:
		require.NoError(t, asyncErr,
			"async startup must return nil after subscription-ready fires")
		require.NotNil(t, errCh,
			"errCh must be non-nil even when components started successfully — "+
				"caller's error-tracking goroutine reads from this channel")
	case <-time.After(2 * time.Second):
		t.Fatal("StartLeaderOnlyComponentsAsync did not return within 2s after " +
			"subscription-ready was signaled")
	}
}

func TestStartLeaderOnlyComponentsAsync_PropagatesNonCanceledStartError(t *testing.T) {
	// Errors from a leader-only component's Start() must arrive on errCh.
	// This is the only signal the caller has that a leader-only component
	// failed mid-flight.
	registry := NewRegistry()

	leader := newSignalingMock()
	leader.subAutoSignal = true                  // signal ready immediately
	leader.startErr = errors.New("startup boom") // then fail
	registry.Register(leader, LeaderOnly())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh, err := registry.StartLeaderOnlyComponentsAsync(ctx)
	require.NoError(t, err,
		"async startup itself must succeed — the error surfaces via errCh")
	require.NotNil(t, errCh)

	// Wait for the start error to arrive on errCh.
	select {
	case got, ok := <-errCh:
		require.True(t, ok,
			"errCh must receive the propagated error (NOT be silently closed) — "+
				"a regression that swallowed the error would leave the caller "+
				"unaware that a leader-only component crashed")
		require.Error(t, got)
		assert.Contains(t, got.Error(), "startup boom",
			"the propagated error must include the underlying message so the "+
				"caller's logger / alerting can surface the actual cause")
	case <-time.After(2 * time.Second):
		t.Fatal("errCh never received the start error — leader-only failures " +
			"would silently disappear in production")
	}
}

func TestStartLeaderOnlyComponentsAsync_CanceledContextDoesNotPropagateAsError(t *testing.T) {
	// If the context cancels (normal shutdown), the resulting
	// context.Canceled error must NOT be sent on errCh — that's a
	// shutdown signal, not a failure. errCh must close cleanly so the
	// caller's error-tracking goroutine doesn't surface false alerts.
	registry := NewRegistry()

	leader := newSignalingMock()
	leader.subAutoSignal = true
	registry.Register(leader, LeaderOnly())

	ctx, cancel := context.WithCancel(context.Background())

	errCh, err := registry.StartLeaderOnlyComponentsAsync(ctx)
	require.NoError(t, err)
	require.NotNil(t, errCh)

	// Cancel the context — leader's Start() will return ctx.Err()
	// (context.Canceled), which the async tracker should swallow.
	cancel()

	// errCh should close WITHOUT sending an error.
	select {
	case got, ok := <-errCh:
		assert.False(t, ok,
			"errCh MUST close cleanly on context cancellation — "+
				"a regression that propagated context.Canceled would surface "+
				"every leader-pod shutdown as a false-positive alert")
		assert.Nil(t, got,
			"errCh must yield nil on close (no error from canceled shutdown)")
	case <-time.After(2 * time.Second):
		t.Fatal("errCh did not close after context cancellation — caller's " +
			"error-tracking goroutine would block forever")
	}
}

func TestStartLeaderOnlyComponentsAsync_PromotesStandbyToStarting(t *testing.T) {
	// Re-entrancy contract: components in StatusStandby (not just
	// StatusPending) get promoted to StatusStarting. This matters for
	// leader-election failover: a component that was previously a
	// non-leader sits in Standby; when leadership is acquired it must
	// actually start, not be silently skipped.
	registry := NewRegistry()
	leader := newSignalingMock()
	leader.subAutoSignal = true
	registry.Register(leader, LeaderOnly())

	// Manually put the component into Standby (simulates the state
	// after StartAll(ctx, isLeader=false) without actually running).
	registry.mu.Lock()
	for _, comp := range registry.components {
		if comp.config.leaderOnly {
			comp.status = StatusStandby
		}
	}
	registry.mu.Unlock()

	// prepareLeaderOnlyComponents (called by Async) MUST find the
	// Standby component and promote it.
	componentsToStart, err := registry.prepareLeaderOnlyComponents()
	require.NoError(t, err)
	require.Len(t, componentsToStart, 1,
		"a Standby leader-only component MUST be promoted on a subsequent "+
			"start call — a regression that only matched StatusPending would "+
			"silently skip components that previously sat as non-leaders, "+
			"breaking leader-election failover")
	assert.Equal(t, "leader-only", componentsToStart[0].component.Name())
}

// Compile-time guard that signalingMock satisfies both the Component
// and SubscriptionReadySignaler interfaces — catches a refactor that
// changed either method signature.
var (
	_ Component                 = (*signalingMock)(nil)
	_ SubscriptionReadySignaler = (*signalingMock)(nil)
)
