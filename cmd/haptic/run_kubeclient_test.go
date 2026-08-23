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

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveFloat32Option(t *testing.T) {
	const flagName = "kube-client-qps"
	const envName = "TEST_KUBE_CLIENT_QPS"

	build := func(t *testing.T, args ...string) (*cobra.Command, float32) {
		t.Helper()
		c := &cobra.Command{Use: "test"}
		var v float32
		c.Flags().Float32Var(&v, flagName, -1, "")
		require.NoError(t, c.ParseFlags(args))
		return c, v
	}

	t.Run("flag wins over environment", func(t *testing.T) {
		t.Setenv(envName, "80")
		c, v := build(t, "--"+flagName+"=50")
		got, err := resolveFloat32Option(c.Flags().Changed(flagName), v, envName)
		require.NoError(t, err)
		assert.Equal(t, float32(50), got)
	})

	t.Run("environment wins over default when flag unchanged", func(t *testing.T) {
		t.Setenv(envName, "80")
		c, v := build(t)
		got, err := resolveFloat32Option(c.Flags().Changed(flagName), v, envName)
		require.NoError(t, err)
		assert.Equal(t, float32(80), got)
	})

	t.Run("default (-1) when flag unchanged and no env", func(t *testing.T) {
		c, v := build(t)
		got, err := resolveFloat32Option(c.Flags().Changed(flagName), v, envName)
		require.NoError(t, err)
		assert.Equal(t, float32(-1), got)
	})

	t.Run("whitespace-only env falls through to default", func(t *testing.T) {
		t.Setenv(envName, "   ")
		c, v := build(t)
		got, err := resolveFloat32Option(c.Flags().Changed(flagName), v, envName)
		require.NoError(t, err)
		assert.Equal(t, float32(-1), got)
	})

	t.Run("invalid env value fails, naming the variable", func(t *testing.T) {
		t.Setenv(envName, "not-a-number")
		c, v := build(t)
		_, err := resolveFloat32Option(c.Flags().Changed(flagName), v, envName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), envName)
	})
}

func TestResolveIntOption(t *testing.T) {
	const flagName = "kube-client-burst"
	const envName = "TEST_KUBE_CLIENT_BURST"

	build := func(t *testing.T, args ...string) (*cobra.Command, int) {
		t.Helper()
		c := &cobra.Command{Use: "test"}
		var v int
		c.Flags().IntVar(&v, flagName, 0, "")
		require.NoError(t, c.ParseFlags(args))
		return c, v
	}

	t.Run("flag wins over environment", func(t *testing.T) {
		t.Setenv(envName, "300")
		c, v := build(t, "--"+flagName+"=100")
		got, err := resolveIntOption(c.Flags().Changed(flagName), v, envName)
		require.NoError(t, err)
		assert.Equal(t, 100, got)
	})

	t.Run("environment wins over default when flag unchanged", func(t *testing.T) {
		t.Setenv(envName, "300")
		c, v := build(t)
		got, err := resolveIntOption(c.Flags().Changed(flagName), v, envName)
		require.NoError(t, err)
		assert.Equal(t, 300, got)
	})

	t.Run("default (0) when flag unchanged and no env", func(t *testing.T) {
		c, v := build(t)
		got, err := resolveIntOption(c.Flags().Changed(flagName), v, envName)
		require.NoError(t, err)
		assert.Equal(t, 0, got)
	})

	t.Run("invalid env value fails, naming the variable", func(t *testing.T) {
		t.Setenv(envName, "1.5")
		c, v := build(t)
		_, err := resolveIntOption(c.Flags().Changed(flagName), v, envName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), envName)
	})
}
