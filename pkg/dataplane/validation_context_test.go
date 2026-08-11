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

package dataplane

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type contextExecutor struct {
	version func(context.Context) (string, error)
	check   func(context.Context, string, ...string) ([]byte, error)
}

func (e contextExecutor) Version(ctx context.Context) (string, error) {
	if e.version != nil {
		return e.version(ctx)
	}
	return fakeHAProxyVersionLine, nil
}

func (e contextExecutor) Check(ctx context.Context, workDir string, args ...string) ([]byte, error) {
	if e.check != nil {
		return e.check(ctx, workDir, args...)
	}
	return nil, nil
}

func TestValidateSemanticsContextCancelsRunningCheck(t *testing.T) {
	started := make(chan struct{})
	restore := SetHAProxyExecutor(contextExecutor{check: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		close(started)
		<-ctx.Done()
		return nil, nil
	}})
	t.Cleanup(restore)

	cause := errors.New("retired validation")
	ctx, cancel := context.WithCancelCause(t.Context())
	paths := testValidationPaths(t)
	done := make(chan error, 1)
	go func() {
		done <- ValidateSemanticsContext(ctx, "global\n    daemon\n", nil, paths, false)
	}()

	<-started
	cancel(cause)
	require.ErrorIs(t, <-done, cause)
}

func TestCanceledHAProxyCheckWaiterNeverExecutes(t *testing.T) {
	var checks atomic.Int32
	restore := SetHAProxyExecutor(contextExecutor{check: func(context.Context, string, ...string) ([]byte, error) {
		checks.Add(1)
		return nil, nil
	}})
	t.Cleanup(restore)

	haproxyCheckGate <- struct{}{}
	t.Cleanup(func() {
		select {
		case <-haproxyCheckGate:
		default:
		}
	})

	cause := errors.New("validation queue retired")
	ctx, cancel := context.WithCancelCause(t.Context())
	configPath := testValidationPaths(t).ConfigFile
	done := make(chan error, 1)
	go func() {
		done <- runHAProxyCheck(ctx, configPath, "global\n", false)
	}()
	cancel(cause)

	require.ErrorIs(t, <-done, cause)
	assert.Zero(t, checks.Load())
}

func TestDetectLocalVersionContextCancelsExecutor(t *testing.T) {
	started := make(chan struct{})
	restore := SetHAProxyExecutor(contextExecutor{version: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		return "", errors.New("executor failed")
	}})
	t.Cleanup(restore)

	cause := errors.New("startup retired")
	ctx, cancel := context.WithCancelCause(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := DetectLocalVersionContext(ctx)
		done <- err
	}()

	<-started
	cancel(cause)
	require.ErrorIs(t, <-done, cause)
}

func TestValidationCacheDoesNotCommitAfterCancellationWhileWaiting(t *testing.T) {
	validationCache.mu.Lock()
	previousConfig := validationCache.lastConfigHash
	previousAux := validationCache.lastAuxHash
	previousVersion := validationCache.lastVersionHash
	validationCache.lastConfigHash = "baseline"
	validationCache.lastAuxHash = "baseline"
	validationCache.lastVersionHash = "baseline"

	cause := errors.New("validation retired while caching")
	ctx, cancel := context.WithCancelCause(t.Context())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- cacheValidationResult(ctx, "new", "new", "new")
	}()
	<-started
	cancel(cause)
	validationCache.mu.Unlock()

	require.ErrorIs(t, <-done, cause)
	validationCache.mu.Lock()
	assert.Equal(t, "baseline", validationCache.lastConfigHash)
	assert.Equal(t, "baseline", validationCache.lastAuxHash)
	assert.Equal(t, "baseline", validationCache.lastVersionHash)
	validationCache.lastConfigHash = previousConfig
	validationCache.lastAuxHash = previousAux
	validationCache.lastVersionHash = previousVersion
	validationCache.mu.Unlock()
}
