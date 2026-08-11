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

type signalingMock struct {
	name               string
	ready              chan struct{}
	started            chan struct{}
	returnErr          chan error
	releaseAfterCancel chan struct{}
	startErr           error
	signalReady        bool
}

func newSignalingMock(name string) *signalingMock {
	return &signalingMock{
		name:    name,
		ready:   make(chan struct{}),
		started: make(chan struct{}),
	}
}

func (s *signalingMock) Name() string {
	return s.name
}

func (s *signalingMock) SubscriptionReady() <-chan struct{} {
	return s.ready
}

func (s *signalingMock) Start(ctx context.Context) error {
	close(s.started)
	if s.signalReady {
		close(s.ready)
	}
	if s.startErr != nil {
		return s.startErr
	}
	if s.returnErr != nil {
		select {
		case err := <-s.returnErr:
			return err
		case <-ctx.Done():
		}
	} else {
		<-ctx.Done()
	}
	if s.releaseAfterCancel != nil {
		<-s.releaseAfterCancel
	}
	return nil
}

func TestStartLeaderOnlyEmptyRunIsComplete(t *testing.T) {
	registry := NewRegistry()
	registry.Register(newMockComponent("all-replica"), false)

	run, err := registry.StartLeaderOnly(t.Context())
	require.NoError(t, err)
	select {
	case <-run.Done():
	default:
		t.Fatal("empty run did not complete")
	}
	require.NoError(t, run.Wait())
}

func TestStartLeaderOnlyWaitsForSubscriptionReadiness(t *testing.T) {
	registry := NewRegistry()
	leader := newSignalingMock("leader")
	registry.Register(leader, true)

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan struct {
		run *ComponentRun
		err error
	}, 1)
	go func() {
		run, err := registry.StartLeaderOnly(ctx)
		result <- struct {
			run *ComponentRun
			err error
		}{run: run, err: err}
	}()

	<-leader.started
	select {
	case <-result:
		t.Fatal("startup returned before the subscription was ready")
	default:
	}
	close(leader.ready)
	started := <-result
	require.NoError(t, started.err)
	assert.Equal(t, StatusRunning, registry.Status()[leader.name].Status)

	cancel()
	require.NoError(t, started.run.Wait())
	assert.Equal(t, StatusStopped, registry.Status()[leader.name].Status)
}

func TestStartLeaderOnlyReportsFailureBeforeReadiness(t *testing.T) {
	registry := NewRegistry()
	leader := newSignalingMock("leader")
	leader.startErr = errors.New("startup boom")
	registry.Register(leader, true)

	run, err := registry.StartLeaderOnly(t.Context())
	require.ErrorContains(t, err, "startup boom")
	require.ErrorContains(t, run.Wait(), "startup boom")
	assert.Equal(t, StatusFailed, registry.Status()[leader.name].Status)
}

func TestStartLeaderOnlyReportsFailureAfterReadiness(t *testing.T) {
	registry := NewRegistry()
	leader := newSignalingMock("leader")
	leader.signalReady = true
	leader.returnErr = make(chan error, 1)
	registry.Register(leader, true)

	run, err := registry.StartLeaderOnly(t.Context())
	require.NoError(t, err)
	leader.returnErr <- errors.New("runtime boom")
	require.ErrorContains(t, run.Wait(), "runtime boom")
	assert.Equal(t, StatusFailed, registry.Status()[leader.name].Status)
}

func TestStartLeaderOnlyCancellationWaitsForActualReturn(t *testing.T) {
	registry := NewRegistry()
	leader := newSignalingMock("leader")
	leader.releaseAfterCancel = make(chan struct{})
	registry.Register(leader, true)

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan struct {
		run *ComponentRun
		err error
	}, 1)
	go func() {
		run, err := registry.StartLeaderOnly(ctx)
		result <- struct {
			run *ComponentRun
			err error
		}{run: run, err: err}
	}()
	<-leader.started
	cancel()
	started := <-result
	require.ErrorIs(t, started.err, context.Canceled)

	select {
	case <-started.run.Done():
		t.Fatal("run completed before Component.Start returned")
	default:
	}
	assert.Equal(t, StatusStarting, registry.Status()[leader.name].Status)
	close(leader.releaseAfterCancel)
	require.NoError(t, started.run.Wait())
	assert.Equal(t, StatusStopped, registry.Status()[leader.name].Status)
}

func TestStartLeaderOnlyFailureJoinsCanceledSiblings(t *testing.T) {
	registry := NewRegistry()
	failing := newSignalingMock("failing")
	failing.startErr = errors.New("startup boom")
	blocked := newSignalingMock("blocked")
	blocked.releaseAfterCancel = make(chan struct{})
	registry.Register(failing, true)
	registry.Register(blocked, true)

	run, err := registry.StartLeaderOnly(t.Context())
	require.ErrorContains(t, err, "startup boom")
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("sibling did not start")
	}
	select {
	case <-run.Done():
		t.Fatal("run completed before the canceled sibling returned")
	default:
	}
	close(blocked.releaseAfterCancel)
	require.ErrorContains(t, run.Wait(), "startup boom")
}

func TestStartLeaderOnlyConcurrentReadinessAndCancellation(t *testing.T) {
	for range 100 {
		registry := NewRegistry()
		leader := newSignalingMock("leader")
		registry.Register(leader, true)
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan *ComponentRun, 1)
		go func() {
			run, _ := registry.StartLeaderOnly(ctx)
			result <- run
		}()
		<-leader.started
		go close(leader.ready)
		cancel()
		require.NoError(t, (<-result).Wait())
		assert.Equal(t, StatusStopped, registry.Status()[leader.name].Status)
	}
}

func TestStartAllDoesNotDemoteRunningLeader(t *testing.T) {
	registry := NewRegistry()
	allReplica := newMockComponent("all-replica")
	leader := newSignalingMock("leader")
	leader.signalReady = true
	registry.Register(allReplica, false)
	registry.Register(leader, true)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	leaderRun, err := registry.StartLeaderOnly(ctx)
	require.NoError(t, err)
	require.Equal(t, StatusRunning, registry.Status()[leader.name].Status)

	allDone := make(chan error, 1)
	go func() {
		allDone <- registry.StartAll(ctx, false)
	}()
	require.True(t, allReplica.WaitStarted(time.Second), "all-replica component did not start")
	assert.Equal(t, StatusRunning, registry.Status()[leader.name].Status)

	cancel()
	require.NoError(t, leaderRun.Wait())
	require.NoError(t, <-allDone)
}

func TestPrepareLeaderOnlyComponentsPromotesStandby(t *testing.T) {
	registry := NewRegistry()
	leader := newSignalingMock("leader")
	registry.Register(leader, true)

	registry.mu.Lock()
	registry.components[0].status = StatusStandby
	registry.mu.Unlock()

	components := registry.prepareLeaderOnlyComponents()
	require.Len(t, components, 1)
	assert.Equal(t, StatusStarting, components[0].status)
}

var (
	_ Component                 = (*signalingMock)(nil)
	_ SubscriptionReadySignaler = (*signalingMock)(nil)
)
