// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestObservationProbeRejectsInitialFailure(t *testing.T) {
	failure := errors.New("readiness API failed")
	probe, err := StartObservationProbe(t.Context(), time.Hour, func(context.Context) error { return failure })
	if probe != nil {
		t.Cleanup(func() { _ = probe.Stop() })
	}
	require.ErrorIs(t, err, failure)
	require.Nil(t, probe)
}

func TestObservationProbeSamplesBothWindowBoundaries(t *testing.T) {
	var calls atomic.Int32
	initial := make(chan struct{})
	probe, err := StartObservationProbe(t.Context(), time.Hour, func(context.Context) error {
		if calls.Add(1) == 1 {
			close(initial)
		}
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = probe.Stop() })
	select {
	case <-initial:
	case <-time.After(time.Second):
		t.Fatal("initial observation did not run")
	}
	require.NoError(t, probe.Stop())
	require.Equal(t, int32(2), calls.Load())
	require.NoError(t, probe.Stop())
	require.Equal(t, int32(2), calls.Load(), "stopping twice must not observe after the window")
}

func TestObservationProbeStopDrainsInFlightSample(t *testing.T) {
	var calls atomic.Int32
	inFlight := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	probe, err := StartObservationProbe(t.Context(), time.Millisecond, func(ctx context.Context) error {
		if calls.Add(1) != 2 {
			return nil
		}
		close(inFlight)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		unblock()
		_ = probe.Stop()
	})
	select {
	case <-inFlight:
	case <-time.After(time.Second):
		t.Fatal("periodic observation did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- probe.Stop() }()
	select {
	case err := <-stopped:
		t.Fatalf("stop canceled the in-flight observation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("stop did not finish after the observation completed")
	}
	require.GreaterOrEqual(t, calls.Load(), int32(3))
}

func TestObservationProbePropagatesFinalFailure(t *testing.T) {
	failure := errors.New("final readiness API failed")
	var calls int
	probe, err := StartObservationProbe(t.Context(), time.Hour, func(context.Context) error {
		calls++
		if calls > 1 {
			return failure
		}
		return nil
	})
	require.NoError(t, err)
	require.ErrorIs(t, probe.Stop(), failure)
}

func TestObservationProbePropagatesPeriodicFailure(t *testing.T) {
	failure := errors.New("periodic readiness API failed")
	var calls int
	failed := make(chan struct{})
	probe, err := StartObservationProbe(t.Context(), time.Millisecond, func(context.Context) error {
		calls++
		if calls > 1 {
			close(failed)
			return failure
		}
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = probe.Stop() })
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("periodic observation did not fail")
	}
	require.ErrorIs(t, probe.Stop(), failure)
}

func TestObservationProbePreservesParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	probe, err := StartObservationProbe(ctx, time.Hour, func(context.Context) error { return nil })
	require.NoError(t, err)
	cancel()
	require.ErrorIs(t, probe.Stop(), context.Canceled)
}

func TestObservationProbeRejectsCanceledStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	probe, err := StartObservationProbe(ctx, time.Hour, func(context.Context) error {
		t.Error("observation started after cancellation")
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, probe)
}

func TestObservationProbeRejectsInvalidInput(t *testing.T) {
	_, err := StartObservationProbe(t.Context(), 0, func(context.Context) error { return nil })
	require.Error(t, err)
	_, err = StartObservationProbe(t.Context(), time.Hour, nil)
	require.Error(t, err)
}
