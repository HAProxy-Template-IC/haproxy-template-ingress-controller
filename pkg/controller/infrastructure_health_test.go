// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	pvtestutil "gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
)

// createEarlyHealthChecker is the health endpoint Kubernetes uses
// during the BEFORE-config window. It runs before the full lifecycle
// registry is built, so a refactor that quietly inverted the
// IsLoaded() branch, or stopped reporting the wait-message in the
// "still loading" path, would leave Kubernetes either:
//
//   - thinking the controller is healthy while it has no config to
//     reconcile (silent broken state); or
//   - thinking the controller is permanently unhealthy after config
//     loads (the pod gets killed by liveness checks during normal
//     startup).
//
// Pin the contract on three load-bearing properties:
//
//  1. The returned closure captures the SAME *configState — flipping
//     state.SetLoaded()/SetWaiting() between calls must change the
//     reported health (no stale snapshot).
//  2. Pre-load state reports Healthy=false AND surfaces the wait
//     message verbatim (used to tell operators what the controller
//     is waiting on).
//  3. Loaded state reports Healthy=true with NO error message
//     (omitting the message keeps health output clean).
//  4. The returned map is keyed under "config" specifically — the
//     introspection /debug/health endpoint groups by this key.
func TestCreateEarlyHealthChecker(t *testing.T) {
	t.Run("pre-load state reports unhealthy with the wait message verbatim", func(t *testing.T) {
		state := &configState{}
		state.SetWaiting("waiting for HAProxyTemplateConfig CRD in namespace haptic")

		checker := createEarlyHealthChecker(state)

		got := checker()
		require.Contains(t, got, "config",
			"health output must be keyed under 'config'; the introspection /debug/health endpoint groups by this key")

		entry := got["config"]
		assert.False(t, entry.Healthy,
			"pre-load state must report unhealthy so Kubernetes startup probes hold the pod in the not-ready state")
		assert.Equal(t, "waiting for HAProxyTemplateConfig CRD in namespace haptic", entry.Error,
			"the wait message must surface verbatim so operators can see what the controller is waiting on")
	})

	t.Run("loaded state reports healthy with no error message", func(t *testing.T) {
		state := &configState{}
		state.SetLoaded()

		checker := createEarlyHealthChecker(state)

		got := checker()
		entry := got["config"]
		assert.True(t, entry.Healthy,
			"loaded state must report healthy; staying unhealthy after load would let liveness checks kill the pod")
		assert.Empty(t, entry.Error,
			"healthy state must not carry an error message — otherwise health output gets noisy")
	})

	t.Run("zero-value configState reports unhealthy (default before any Set call)", func(t *testing.T) {
		// Before any caller invokes SetWaiting/SetLoaded the state
		// must be treated as "not loaded yet". The zero value of
		// configState has configLoaded=false, so the checker must
		// fall through the unhealthy branch.
		state := &configState{}

		checker := createEarlyHealthChecker(state)

		got := checker()
		entry := got["config"]
		assert.False(t, entry.Healthy,
			"zero-value configState must default to unhealthy; otherwise the pod would report ready before any config attempt")
		assert.Empty(t, entry.Error,
			"zero-value state has no message yet, so Error must be empty (caller hasn't told us what it's waiting on)")
	})

	t.Run("checker reflects state changes (no stale snapshot)", func(t *testing.T) {
		// The closure captures *configState by reference. State
		// transitions after createEarlyHealthChecker returns must
		// be visible. A refactor that snapshotted the message at
		// construction time would silently freeze the wait-text on
		// the first probe.
		state := &configState{}
		state.SetWaiting("connecting to apiserver")

		checker := createEarlyHealthChecker(state)

		got := checker()
		assert.False(t, got["config"].Healthy)
		assert.Equal(t, "connecting to apiserver", got["config"].Error)

		// Transition the state — checker must reflect it.
		state.SetWaiting("loading HAProxyTemplateConfig")
		got = checker()
		assert.False(t, got["config"].Healthy)
		assert.Equal(t, "loading HAProxyTemplateConfig", got["config"].Error,
			"checker must reflect the LATEST state, not a stale snapshot")

		// Transition to loaded.
		state.SetLoaded()
		got = checker()
		assert.True(t, got["config"].Healthy)
		assert.Empty(t, got["config"].Error,
			"transitioning loaded must clear the wait message")
	})
}

// mergePluggableValidatorHealth is the seam through which `spec.validators`
// status reaches /healthz. Pin three load-bearing properties:
//
//  1. No validators configured → no entry inserted (operators not using
//     the feature see /healthz output unchanged).
//  2. All validator sockets healthy → single Healthy=true entry under
//     "pluggable-validators", no Error string.
//  3. One or more sockets unreachable → Healthy=false with the failure
//     reasons surfaced verbatim (semicolon-joined for multi-failure).
func TestMergePluggableValidatorHealth(t *testing.T) {
	t.Run("nil manager skips the entry", func(t *testing.T) {
		result := map[string]introspection.ComponentHealth{}
		mergePluggableValidatorHealth(result, nil)
		_, present := result["pluggable-validators"]
		assert.False(t, present,
			"nil manager must not pollute /healthz output for operators not using the feature")
	})

	t.Run("manager with no validators skips the entry", func(t *testing.T) {
		mgr, err := pluggablevalidator.NewManager(slog.Default(), nil)
		require.NoError(t, err)
		result := map[string]introspection.ComponentHealth{}
		mergePluggableValidatorHealth(result, mgr)
		_, present := result["pluggable-validators"]
		assert.False(t, present,
			"manager.Configured()==false must not surface a /healthz entry")
	})

	t.Run("all sockets healthy reports healthy with no error", func(t *testing.T) {
		srv1 := pvtestutil.NewFixtureServer(t)
		srv2 := pvtestutil.NewFixtureServer(t)
		mgr, err := pluggablevalidator.NewManager(slog.Default(), []pluggablevalidator.ManagerConfig{
			{Name: "coraza", SocketPath: srv1.SocketPath, Files: []string{"/probe.toml"}},
			{Name: "otel", SocketPath: srv2.SocketPath, Files: []string{"/probe.toml"}},
		})
		require.NoError(t, err)

		result := map[string]introspection.ComponentHealth{}
		mergePluggableValidatorHealth(result, mgr)

		entry, ok := result["pluggable-validators"]
		require.True(t, ok, "all-healthy state must surface the entry so operators can see configured-and-fine")
		assert.True(t, entry.Healthy,
			"all sockets reachable → Healthy=true; otherwise liveness probes flap on a working setup")
		assert.Empty(t, entry.Error,
			"healthy state must not carry an error string")
	})

	t.Run("missing socket reports unhealthy with the failure name", func(t *testing.T) {
		srv := pvtestutil.NewFixtureServer(t)
		missing := filepath.Join(t.TempDir(), "missing.sock")
		mgr, err := pluggablevalidator.NewManager(slog.Default(), []pluggablevalidator.ManagerConfig{
			{Name: "coraza", SocketPath: srv.SocketPath, Files: []string{"/probe.toml"}},
			{Name: "otel", SocketPath: missing, Files: []string{"/probe.toml"}},
		})
		require.NoError(t, err)

		result := map[string]introspection.ComponentHealth{}
		mergePluggableValidatorHealth(result, mgr)

		entry, ok := result["pluggable-validators"]
		require.True(t, ok)
		assert.False(t, entry.Healthy,
			"any socket failure must mark the validators entry unhealthy")
		assert.Contains(t, entry.Error, "otel:",
			"the failing validator's name must surface so operators can identify the broken sidecar")
	})
}
