// Copyright 2026 Philipp Hossner
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
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestLogBackgroundComponentError(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	deadlineCtx, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelDeadline()

	tests := []struct {
		name    string
		ctx     context.Context
		err     error
		wantLog bool
	}{
		{name: "success", ctx: context.Background()},
		{name: "canceled", ctx: canceledCtx, err: context.Canceled},
		{name: "wrapped cancellation", ctx: canceledCtx, err: errors.Join(errors.New("stopped"), context.Canceled)},
		{name: "parent deadline exceeded", ctx: deadlineCtx, err: context.DeadlineExceeded},
		{name: "independent deadline exceeded", ctx: context.Background(), err: context.DeadlineExceeded, wantLog: true},
		{name: "component failure during cancellation", ctx: canceledCtx, err: errors.New("event loop failed"), wantLog: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, nil))

			logBackgroundComponentError(test.ctx, logger, "Metrics component", test.err)

			if test.wantLog {
				assert.Contains(t, output.String(), "Metrics component failed")
				assert.Contains(t, output.String(), test.err.Error())
				return
			}
			assert.Empty(t, output.String())
		})
	}
}

func TestStartNonFatalInErrGroupTracksWithoutCancelling(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	group, groupCtx := errgroup.WithContext(ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	finished := make(chan struct{})
	var calls atomic.Int64

	startNonFatalInErrGroup(group, groupCtx, logger, "test component", func(context.Context) error {
		calls.Add(1)
		close(finished)
		return errors.New("component failed")
	})

	<-finished
	assert.NoError(t, groupCtx.Err())
	cancel()
	require.NoError(t, group.Wait())
	assert.Equal(t, int64(1), calls.Load())
}

func TestStartInErrGroupIgnoresContextTermination(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	group, groupCtx := errgroup.WithContext(ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	started := make(chan struct{})

	startInErrGroup(group, groupCtx, logger, cancel, "test component", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	cancel()
	require.NoError(t, group.Wait())
}

func TestStartInErrGroupRejectsUnexpectedStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	group, groupCtx := errgroup.WithContext(ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	startInErrGroup(group, groupCtx, logger, cancel, "test component", func(context.Context) error {
		return nil
	})

	require.ErrorContains(t, group.Wait(), "test component stopped unexpectedly")
}

func TestWaitForGoroutinesToFinishTimesOut(t *testing.T) {
	group := &errgroup.Group{}
	release := make(chan struct{})
	finished := make(chan struct{})
	group.Go(func() error {
		defer close(finished)
		<-release
		return nil
	})

	err := waitForGoroutinesToFinish(
		group,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"test teardown",
		10*time.Millisecond,
	)
	var timeoutErr *iterationTeardownTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	close(release)
	<-finished
}

func TestTeardownIterationNormalizesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	group, groupCtx := errgroup.WithContext(ctx)
	started := make(chan struct{})
	group.Go(func() error {
		close(started)
		<-groupCtx.Done()
		return groupCtx.Err()
	})
	<-started
	var cleaned atomic.Bool
	setup := &componentSetup{
		IterCtx:  groupCtx,
		Cancel:   cancel,
		ErrGroup: group,
	}
	setup.AddCleanup(func() {
		cleaned.Store(true)
	})

	require.NoError(t, teardownIteration(setup, slog.New(slog.NewTextHandler(io.Discard, nil))))
	assert.True(t, cleaned.Load())
}

func TestRunIterationsDoesNotRetryUnjoinedIteration(t *testing.T) {
	var attempts atomic.Int64
	err := runIterations(
		t.Context(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		0,
		func() error {
			attempts.Add(1)
			return &iterationTeardownTimeoutError{phase: "iteration teardown", timeout: time.Second}
		},
	)
	var timeoutErr *iterationTeardownTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.Equal(t, int64(1), attempts.Load())
}

func TestRunIterationsDoesNotRetryStoppedWebhookServer(t *testing.T) {
	var attempts atomic.Int64
	err := runIterations(
		t.Context(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		0,
		func() error {
			attempts.Add(1)
			return &persistentWebhookServerError{err: errors.New("serve failed")}
		},
	)
	var serverErr *persistentWebhookServerError
	require.ErrorAs(t, err, &serverErr)
	assert.Equal(t, int64(1), attempts.Load())
}
