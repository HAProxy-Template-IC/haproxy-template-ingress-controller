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

package testrunner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
)

func TestAppendAssertionResultPreservesVerdictCompletedAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result := &TestResult{Passed: true}

	incomplete := appendAssertionResult(ctx, result, &AssertionResult{
		Type:   "haproxy_valid",
		Passed: false,
		Error:  "HAProxy rejected the configuration",
	})

	assert.True(t, incomplete)
	assert.False(t, result.Passed)
	require.Len(t, result.Assertions, 1)
	assert.Equal(t, "HAProxy rejected the configuration", result.Assertions[0].Error)
}

func TestAppendAssertionResultOmitsCancellationFailure(t *testing.T) {
	result := &TestResult{Passed: true}

	incomplete := appendAssertionResult(t.Context(), result, &AssertionResult{
		Type:       "deterministic",
		Passed:     false,
		Error:      "second render failed: context canceled",
		incomplete: true,
	})

	assert.True(t, incomplete)
	assert.True(t, result.Passed)
	assert.Empty(t, result.Assertions)
}

func TestHAProxyAssertionCancellationIsIncomplete(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	restore := dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheckContext(
		func(ctx context.Context, _ string, _ []string) ([]byte, error) {
			close(started)
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case <-release:
				return nil, nil
			}
		},
	))
	t.Cleanup(func() {
		close(release)
		restore()
	})

	tempDir := t.TempDir()
	paths := &dataplane.ValidationPaths{
		MapsDir:           filepath.Join(tempDir, "maps"),
		SSLCertsDir:       filepath.Join(tempDir, "ssl"),
		GeneralStorageDir: filepath.Join(tempDir, "general"),
		ConfigFile:        filepath.Join(tempDir, "haproxy.cfg"),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan AssertionResult, 1)
	go func() {
		done <- (&Runner{}).assertHAProxyValid(ctx, "global\n    daemon\n", nil, &config.ValidationAssertion{}, paths)
	}()

	<-started
	cancel()
	var assertion AssertionResult
	select {
	case assertion = <-done:
	case <-time.After(time.Second):
		t.Fatal("HAProxy assertion did not stop after cancellation")
	}

	result := &TestResult{Passed: true}
	assert.True(t, appendAssertionResult(ctx, result, &assertion))
	assert.True(t, assertion.incomplete)
	assert.True(t, result.Passed)
	assert.Empty(t, result.Assertions)
}
