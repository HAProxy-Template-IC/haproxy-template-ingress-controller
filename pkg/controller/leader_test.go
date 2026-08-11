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

package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

type blockedLeaderComponent struct {
	ready   chan struct{}
	started chan struct{}
	release chan struct{}
}

func newBlockedLeaderComponent() *blockedLeaderComponent {
	return &blockedLeaderComponent{
		ready:   make(chan struct{}),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (*blockedLeaderComponent) Name() string {
	return "blocked-leader"
}

func (c *blockedLeaderComponent) SubscriptionReady() <-chan struct{} {
	return c.ready
}

func (c *blockedLeaderComponent) Start(ctx context.Context) error {
	close(c.started)
	<-ctx.Done()
	<-c.release
	return nil
}

// TestSuperviseElection covers the three exit shapes of the leader-election
// loop. The critical case is "lease lost without shutdown": client-go's
// LeaderElector.Run returns permanently after a missed lease renewal, so a
// nil return with a live context MUST become an iteration-fatal error —
// otherwise the replica stays a follower with a dead elector forever
// (issue #57).
func TestSuperviseElection(t *testing.T) {
	logger := slog.Default()

	t.Run("lease lost without shutdown fails the iteration", func(t *testing.T) {
		ctx := context.Background()
		// Elector returns nil while the iteration context is still alive —
		// the lost-lease shape.
		err := superviseElection(ctx, func(context.Context) error { return nil }, logger)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lease lost without shutdown")
	})

	t.Run("normal teardown is not an error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		// Elector returns because the iteration context was cancelled
		// (shutdown or config-change reinitialization).
		err := superviseElection(ctx, func(ctx context.Context) error {
			cancel()
			<-ctx.Done()
			return nil
		}, logger)
		assert.NoError(t, err)
	})

	t.Run("elector error propagates", func(t *testing.T) {
		ctx := context.Background()
		electErr := errors.New("creating leader elector: boom")
		err := superviseElection(ctx, func(context.Context) error { return electErr }, logger)
		require.Error(t, err)
		assert.ErrorIs(t, err, electErr)
	})
}

func TestLeaderCallbacksCancelIterationBeforeJoiningAfterLeaseLoss(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := lifecycle.NewRegistry().WithLogger(logger)
	component := newBlockedLeaderComponent()
	registry.Register(component, true)
	iterCtx, iterCancelCause := context.WithCancelCause(t.Context())
	iterCancel := func() { iterCancelCause(nil) }
	defer iterCancel()
	group, _ := errgroup.WithContext(iterCtx)
	callbacks, _ := makeLeaderCallbacks(leaderCallbackDeps{
		registry:    registry,
		logger:      logger,
		cancel:      iterCancel,
		cancelCause: iterCancelCause,
		errGroup:    group,
	})
	leaderCtx, loseLeadership := context.WithCancel(iterCtx)

	startedCallbackDone := make(chan struct{})
	go func() {
		callbacks.OnStartedLeading(leaderCtx)
		close(startedCallbackDone)
	}()
	<-component.started
	loseLeadership()

	stoppedCallbackDone := make(chan struct{})
	go func() {
		callbacks.OnStoppedLeading()
		close(stoppedCallbackDone)
	}()
	select {
	case <-iterCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("leadership stop did not cancel the iteration before joining components")
	}
	var leadershipLost *leadershipLostError
	require.ErrorAs(t, context.Cause(iterCtx), &leadershipLost)
	select {
	case <-startedCallbackDone:
	case <-time.After(time.Second):
		t.Fatal("leader startup remained blocked after authority was canceled")
	}
	select {
	case <-stoppedCallbackDone:
		t.Fatal("leadership stop returned before the component did")
	default:
	}

	close(component.release)
	select {
	case <-stoppedCallbackDone:
	case <-time.After(time.Second):
		t.Fatal("leadership stop did not join component completion")
	}
	require.NoError(t, group.Wait())
}

func TestLeaderCallbacksRejectDelayedStartAfterStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := lifecycle.NewRegistry().WithLogger(logger)
	component := newBlockedLeaderComponent()
	registry.Register(component, true)
	ctx, cancelCause := context.WithCancelCause(t.Context())
	cancel := func() { cancelCause(nil) }
	defer cancel()
	group, _ := errgroup.WithContext(ctx)
	callbacks, _ := makeLeaderCallbacks(leaderCallbackDeps{
		registry:    registry,
		logger:      logger,
		cancel:      cancel,
		cancelCause: cancelCause,
		errGroup:    group,
	})

	callbacks.OnStoppedLeading()
	callbacks.OnStartedLeading(ctx)
	select {
	case <-component.started:
		t.Fatal("delayed OnStartedLeading started a retired term")
	default:
	}
	require.NoError(t, group.Wait())
}

func TestLeaderCallbacksPreserveEarlierIterationFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	cancel := func() { cancelCause(nil) }
	group, _ := errgroup.WithContext(iterCtx)
	callbacks, _ := makeLeaderCallbacks(leaderCallbackDeps{
		registry:    lifecycle.NewRegistry().WithLogger(logger),
		logger:      logger,
		cancel:      cancel,
		cancelCause: cancelCause,
		errGroup:    group,
	})
	failure := errors.New("required component failed")
	cancelCause(failure)

	callbacks.OnStoppedLeading()

	require.ErrorIs(t, context.Cause(iterCtx), failure)
	require.NoError(t, group.Wait())
}

func TestSuperviseElectionPropagatesCallbackLeadershipLoss(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	cancel := func() { cancelCause(nil) }
	group, _ := errgroup.WithContext(iterCtx)
	callbacks, _ := makeLeaderCallbacks(leaderCallbackDeps{
		registry:    lifecycle.NewRegistry().WithLogger(logger),
		logger:      logger,
		cancel:      cancel,
		cancelCause: cancelCause,
		errGroup:    group,
	})

	err := superviseElection(iterCtx, func(context.Context) error {
		callbacks.OnStoppedLeading()
		return nil
	}, logger)

	var leadershipLost *leadershipLostError
	require.ErrorAs(t, err, &leadershipLost)
	require.ErrorAs(t, context.Cause(iterCtx), &leadershipLost)
	require.NoError(t, group.Wait())
}

func TestStandaloneCanceledHandoffDoesNotResumeEventBus(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus := busevents.NewEventBus(2)
	received := bus.Subscribe("receiver", 2)
	bus.Start()
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	cancel := func() { cancelCause(nil) }
	group, groupCtx := errgroup.WithContext(iterCtx)
	setup := &componentSetup{
		Bus:         bus,
		Registry:    lifecycle.NewRegistry().WithLogger(logger),
		IterCtx:     groupCtx,
		Cancel:      cancel,
		CancelCause: cancelCause,
		ErrGroup:    group,
	}
	failure := errors.New("required component failed")
	cancelCause(failure)

	_, err := setupLeaderElection(setup, &coreconfig.Config{}, nil, logger)

	require.ErrorIs(t, err, failure)
	bus.Publish(deliveryTestEvent{timestamp: time.Now()})
	select {
	case event := <-received:
		t.Fatalf("delivered %T after canceled standalone handoff", event)
	default:
	}
	require.NoError(t, group.Wait())
}

func TestTeardownCancelsLeaderStartupBeforeWaiting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := lifecycle.NewRegistry().WithLogger(logger)
	component := newBlockedLeaderComponent()
	registry.Register(component, true)
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	cancel := func() { cancelCause(nil) }
	group, groupCtx := errgroup.WithContext(iterCtx)
	callbacks, state := makeLeaderCallbacks(leaderCallbackDeps{
		registry:    registry,
		logger:      logger,
		cancel:      cancel,
		cancelCause: cancelCause,
		errGroup:    group,
	})
	setup := &componentSetup{
		IterCtx:     groupCtx,
		Cancel:      cancel,
		CancelCause: cancelCause,
		ErrGroup:    group,
		LeaderState: state,
	}

	startedCallbackDone := make(chan struct{})
	go func() {
		callbacks.OnStartedLeading(groupCtx)
		close(startedCallbackDone)
	}()
	<-component.started
	teardownDone := make(chan error, 1)
	go func() {
		teardownDone <- teardownIteration(setup, logger)
	}()
	select {
	case <-startedCallbackDone:
	case <-time.After(time.Second):
		t.Fatal("iteration teardown did not cancel leader startup")
	}
	select {
	case <-teardownDone:
		t.Fatal("iteration teardown passed an active leader component")
	default:
	}

	close(component.release)
	require.NoError(t, <-teardownDone)
}

var _ lifecycle.SubscriptionReadySignaler = (*blockedLeaderComponent)(nil)
