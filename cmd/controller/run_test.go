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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveDurationOption(t *testing.T) {
	const envName = "TEST_ADMISSION_TIMEOUT"

	t.Run("flag wins over environment", func(t *testing.T) {
		t.Setenv(envName, "7s")
		got, err := resolveDurationOption(3*time.Second, envName, time.Second)
		require.NoError(t, err)
		assert.Equal(t, 3*time.Second, got)
	})

	t.Run("environment wins over default", func(t *testing.T) {
		t.Setenv(envName, "7s")
		got, err := resolveDurationOption(0, envName, time.Second)
		require.NoError(t, err)
		assert.Equal(t, 7*time.Second, got)
	})

	t.Run("default is used when unset", func(t *testing.T) {
		got, err := resolveDurationOption(0, envName, 11*time.Second)
		require.NoError(t, err)
		assert.Equal(t, 11*time.Second, got)
	})

	t.Run("invalid environment value fails", func(t *testing.T) {
		t.Setenv(envName, "not-a-duration")
		_, err := resolveDurationOption(0, envName, time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), envName)
	})

	t.Run("non-positive values fail", func(t *testing.T) {
		t.Setenv(envName, "0s")
		_, err := resolveDurationOption(0, envName, time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be positive")
	})

	t.Run("values above the Kubernetes response-margin maximum fail", func(t *testing.T) {
		t.Setenv(envName, "30s")
		_, err := resolveDurationOption(0, envName, time.Second)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not exceed 29s")
	})
}
