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
	"fmt"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
)

func TestShutdownKeepsDependenciesAliveUntilAdmissionFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		signalCtx, signal := context.WithCancel(t.Context())
		procCtx, stop := context.WithCancel(context.WithoutCancel(signalCtx))
		defer stop()
		entered, release := make(chan struct{}), make(chan struct{})
		drainErr := errors.New("drain failure")
		var validationCtxErr error
		done := drainOnCancellation(signalCtx, procCtx, stop, func(ctx context.Context) error {
			close(entered)
			<-release
			validationCtxErr = ctx.Err()
			return drainErr
		})
		signal()
		<-entered
		require.NoError(t, procCtx.Err())
		close(release)
		result := <-done
		require.NoError(t, validationCtxErr, "validation dependencies stopped before requests finished")
		require.ErrorIs(t, result.err, drainErr)
		require.ErrorIs(t, procCtx.Err(), context.Canceled)
	})
}

func TestShutdownDoesNotDrainAfterProcessFailure(t *testing.T) {
	procCtx, stop := context.WithCancel(t.Context())
	stop()
	done := drainOnCancellation(t.Context(), procCtx, stop, func(context.Context) error {
		t.Error("failed dependencies cannot validate during a drain")
		return nil
	})
	require.NoError(t, (<-done).err)
}

func TestShutdownOverridesHealthyReinitialization(t *testing.T) {
	infra := &persistentInfra{}
	infra.draining.Store(true)
	entries := applyReinitGrace(infra, 0, map[string]introspection.ComponentHealth{
		"initialized": {Healthy: true},
	})
	require.False(t, entries["shutdown"].Healthy)
	require.True(t, entries["initialized"].Healthy)
}

func TestShutdownDefersWebhookReinitializationUntilDrainFinishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		infra := &persistentInfra{}
		infra.draining.Store(true)
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		attempts := 0
		done := make(chan error, 1)
		go func() {
			done <- runIterations(ctx, logger, time.Hour, func() error {
				attempts++
				_, err := infra.EnsureWebhookServer(ctx, nil, logger)
				return fmt.Errorf("starting webhook listener: %w", err)
			})
		}()
		synctest.Wait()
		require.NoError(t, ctx.Err())
		require.Empty(t, logs.String())
		require.True(t, infra.webhookMu.TryLock(), "draining must retain access to the listener")
		infra.webhookMu.Unlock()
		select {
		case err := <-done:
			t.Fatalf("iteration ended before admission draining finished: %v", err)
		default:
		}
		cancel()
		require.NoError(t, <-done)
		require.Equal(t, 1, attempts)
		require.NotContains(t, logs.String(), "level=ERROR")
	})
}
