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

package main

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigureContentionProfiling(t *testing.T) {
	t.Cleanup(func() {
		runtime.SetMutexProfileFraction(0)
		runtime.SetBlockProfileRate(0)
	})

	t.Setenv("MUTEX_PROFILE_FRACTION", "10")
	t.Setenv("BLOCK_PROFILE_RATE", "1000000")
	mutexFraction, blockRate, err := configureContentionProfiling()
	require.NoError(t, err)
	assert.Equal(t, 10, mutexFraction)
	assert.Equal(t, 1000000, blockRate)
	assert.Equal(t, 10, runtime.SetMutexProfileFraction(-1))

	t.Setenv("MUTEX_PROFILE_FRACTION", "")
	t.Setenv("BLOCK_PROFILE_RATE", "")
	mutexFraction, blockRate, err = configureContentionProfiling()
	require.NoError(t, err)
	assert.Zero(t, mutexFraction)
	assert.Zero(t, blockRate)
	assert.Zero(t, runtime.SetMutexProfileFraction(-1))

	for _, raw := range []string{"-1", "ten", "1.5"} {
		t.Setenv("MUTEX_PROFILE_FRACTION", raw)
		_, _, err = configureContentionProfiling()
		assert.ErrorContains(t, err, "MUTEX_PROFILE_FRACTION must be a non-negative integer", raw)
	}
}
