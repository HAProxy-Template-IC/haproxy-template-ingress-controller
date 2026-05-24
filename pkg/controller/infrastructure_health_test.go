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
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
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

	// The early checker is in use during stages 1-7 of runIteration —
	// by definition the staged startup has NOT yet finished, otherwise
	// setupInfrastructureServers would have installed the full checker
	// on top. The "initialized" entry must therefore be Healthy=false
	// regardless of configState's `initialized` field (the field is
	// flipped at the very end of runIteration, after the full checker
	// has replaced this one). This is the e2e suite's gate signal: as
	// long as the early checker is serving /healthz, the controller
	// is not yet ready to accept work.
	t.Run("initialized entry is always unhealthy in the early checker", func(t *testing.T) {
		state := &configState{}
		state.SetLoaded()
		// Even if some bug flipped initialized, the early checker
		// must still report it false — the checker contract is
		// "I'm in early-startup mode" by virtue of being installed.
		state.SetInitialized()

		got := createEarlyHealthChecker(state)()
		entry, ok := got["initialized"]
		require.True(t, ok,
			"early checker must always include the 'initialized' entry so /healthz returns 503 during stages 1-7")
		assert.False(t, entry.Healthy,
			"early checker's 'initialized' entry must be Healthy=false regardless of state.IsInitialized()")
		assert.NotEmpty(t, entry.Error,
			"unhealthy state must carry an Error message so operators see the reason")
	})
}

// computeInitializedHealth and collectComponentHealth together produce the
// "initialized" entry that the e2e suite and Kubernetes readiness probes
// gate on. The contract is non-trivial — get it wrong in either direction
// and you break a different consumer:
//
//   - Too strict (followers report Healthy=false forever) → kubelet kills
//     follower replicas in HA deployments, defeating the whole point of
//     leader election.
//   - Too loose (leader returns Healthy=true before leader-only components
//     are running) → the e2e suite and downstream automation race against
//     a controller that can't yet deploy config, producing intermittent
//     "Gateway has no address" / "HTTPRoute has no status" failures.
//
// Pin the four canonical states:
//
//  1. Follower (leader-only components in StatusStandby, others Running)
//     → Healthy=true. This is the load-bearing case for HA.
//  2. Leader after startup completes (all components Running) → Healthy=true.
//  3. Leader during the leader-acquisition window (leader-only components
//     still in StatusPending) → Healthy=false with the pending component
//     named verbatim, so CI logs show which component is stuck.
//  4. state.IsInitialized() == false → Healthy=false with "still
//     initializing" regardless of component state, so callers can tell
//     "iteration setup not done" apart from "leader election pending".
func TestBuildFullHealthChecker_InitializedGate(t *testing.T) {
	t.Run("follower with leader-only components on Standby reports Healthy=true", func(t *testing.T) {
		// On a follower replica, Registry.StartAll(ctx, false) marks
		// leader-only components as StatusStandby. The "initialized"
		// gate MUST treat Standby as terminal — otherwise the follower
		// is permanently 503 and kubelet kills it.
		status := map[string]lifecycle.ComponentInfo{
			"reconciler": {Status: lifecycle.StatusRunning},
			"discovery":  {Status: lifecycle.StatusRunning},
			"deployer":   {Status: lifecycle.StatusStandby, LeaderOnly: true},
			"scheduler":  {Status: lifecycle.StatusStandby, LeaderOnly: true},
		}
		result := map[string]introspection.ComponentHealth{}
		firstPending := collectComponentHealth(status, result)
		init := computeInitializedHealth(true, firstPending)

		assert.True(t, init.Healthy,
			"follower with leader-only components on Standby must report Healthy=true — otherwise kubelet kills HA follower pods")
		assert.Empty(t, init.Error,
			"healthy state must not carry an error string")
		assert.True(t, result["deployer"].Healthy,
			"Standby leader-only components must individually report Healthy=true on followers")
	})

	t.Run("leader with all components Running reports Healthy=true", func(t *testing.T) {
		status := map[string]lifecycle.ComponentInfo{
			"reconciler": {Status: lifecycle.StatusRunning},
			"discovery":  {Status: lifecycle.StatusRunning},
			"deployer":   {Status: lifecycle.StatusRunning, LeaderOnly: true},
		}
		result := map[string]introspection.ComponentHealth{}
		firstPending := collectComponentHealth(status, result)
		init := computeInitializedHealth(true, firstPending)

		assert.True(t, init.Healthy)
		assert.Empty(t, init.Error)
	})

	t.Run("leader with leader-only component still Pending reports Healthy=false with the component name", func(t *testing.T) {
		// This is the race window the user pointed at: SetInitialized()
		// can fire before StartLeaderOnlyComponents has transitioned
		// the deployer/scheduler from StatusPending to StatusRunning.
		// The "initialized" entry must reflect that so /healthz stays
		// 503 until the leader is actually doing work.
		status := map[string]lifecycle.ComponentInfo{
			"reconciler": {Status: lifecycle.StatusRunning},
			"deployer":   {Status: lifecycle.StatusPending, LeaderOnly: true},
		}
		result := map[string]introspection.ComponentHealth{}
		firstPending := collectComponentHealth(status, result)
		init := computeInitializedHealth(true, firstPending)

		assert.False(t, init.Healthy,
			"leader with a leader-only component still Pending must report /healthz unhealthy — otherwise the e2e suite races against a not-yet-functional leader")
		assert.Contains(t, init.Error, "deployer",
			"error message must name the pending component so CI logs show which one is stuck")
		assert.Contains(t, init.Error, "leader election",
			"error message must mention leader election as a likely cause to aid debugging")
	})

	t.Run("Starting status also counts as not-yet-terminal", func(t *testing.T) {
		// StatusStarting is the transient state between Pending and
		// Running. It must be treated as not-terminal otherwise the
		// "initialized" gate flips green during the startup chain.
		status := map[string]lifecycle.ComponentInfo{
			"reconciler": {Status: lifecycle.StatusStarting},
		}
		result := map[string]introspection.ComponentHealth{}
		firstPending := collectComponentHealth(status, result)
		init := computeInitializedHealth(true, firstPending)

		assert.False(t, init.Healthy)
		assert.Contains(t, init.Error, "reconciler")
	})

	t.Run("state.IsInitialized()==false dominates regardless of component state", func(t *testing.T) {
		// Even if every component is happy, "iteration setup not done"
		// must beat "all components running" in the error message —
		// otherwise operators chasing a stuck startup get a confusing
		// "leader election may not have acquired the lease yet"
		// pointer when the real cause is staged-startup not finished.
		status := map[string]lifecycle.ComponentInfo{
			"reconciler": {Status: lifecycle.StatusRunning},
		}
		result := map[string]introspection.ComponentHealth{}
		firstPending := collectComponentHealth(status, result)
		init := computeInitializedHealth(false, firstPending)

		assert.False(t, init.Healthy)
		assert.Equal(t, "controller still initializing", init.Error,
			"the 'still initializing' message must win over 'pending component' so operators get an unambiguous gate name")
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
