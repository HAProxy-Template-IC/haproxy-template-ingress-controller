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

// A hand-set CRD_NAME with a trailing comma or spaces would otherwise reach
// GetResource as an empty or space-padded name and surface as a confusing
// not-found instead of being ignored.
func TestSplitConfigNames(t *testing.T) {
	tests := []struct {
		name    string
		fromEnv string
		want    []string
	}{
		{name: "single name", fromEnv: "haptic-config", want: []string{"haptic-config"}},
		{name: "ordered list", fromEnv: "a,b,c", want: []string{"a", "b", "c"}},
		{name: "spaces after separators are trimmed", fromEnv: "a, b ,c", want: []string{"a", "b", "c"}},
		{name: "trailing separator adds no empty name", fromEnv: "a,b,", want: []string{"a", "b"}},
		{name: "leading separator adds no empty name", fromEnv: ",a", want: []string{"a"}},
		{name: "separators only yield nothing", fromEnv: ",,", want: nil},
		{name: "whitespace only yields nothing", fromEnv: "  ", want: nil},
		{name: "empty yields nothing", fromEnv: "", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitConfigNames(tt.fromEnv))
		})
	}
}

// An empty env value must fall through to the default rather than to no config
// at all, which would leave the controller with nothing to watch.
func TestResolveConfigNames(t *testing.T) {
	t.Setenv("CRD_NAME", "")
	assert.Equal(t, []string{defaultCRDName}, resolveConfigNames(nil))

	t.Setenv("CRD_NAME", ",,")
	assert.Equal(t, []string{defaultCRDName}, resolveConfigNames(nil))

	t.Setenv("CRD_NAME", "from-env")
	assert.Equal(t, []string{"from-env"}, resolveConfigNames(nil))

	// The flag wins over the env var.
	assert.Equal(t, []string{"from-flag"}, resolveConfigNames([]string{"from-flag"}))
}
