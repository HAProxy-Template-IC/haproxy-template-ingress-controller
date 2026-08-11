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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
