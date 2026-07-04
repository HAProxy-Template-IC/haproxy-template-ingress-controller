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

package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

// createEarlyHealthChecker creates a health checker that reports
// unhealthy until config is loaded AND the staged startup has finished.
// This is used during early startup before the full lifecycle-based
// checker is available.
//
// The "initialized" entry is always Healthy=false here because the
// early checker is in use during stages 1-7 — by definition the
// staged startup has not finished yet, otherwise setupInfrastructureServers
// would have installed the full checker over the top of this one.
// Operators (and the e2e suite) get a single signal — /healthz 200 —
// for "controller is ready to accept work" regardless of which checker
// happens to be installed at the moment of the request.
// msgStillInitializing is the shared "initialized" gate message and
// healthKeyInitialized its entry key (goconst).
const (
	msgStillInitializing = "controller still initializing"
	healthKeyInitialized = "initialized"
)

// beginIteration performs the per-iteration bookkeeping runIteration starts
// with: reinit-grace accounting (a voluntary restart must not flip /healthz
// unhealthy for the bounded rebuild window — see InReinitGrace), clearing
// the persistent introspection registry of the previous iteration's
// entries, and a fresh per-iteration health state.
func beginIteration(infra *persistentInfra) *configState {
	infra.NoteIterationStart()
	infra.IntrospectionRegistry.Clear()
	return &configState{}
}

// applyReinitGrace rewrites unhealthy entries as healthy-with-annotation
// while a voluntary reinitialization is within persistentInfra's grace
// window (see ReinitGraceWindow). The entry detail is preserved so
// operators can still see what is re-initializing; only the aggregate
// HTTP status (and thus the liveness/readiness verdict) is softened.
func applyReinitGrace(infra *persistentInfra, entries map[string]introspection.ComponentHealth) map[string]introspection.ComponentHealth {
	allHealthy := true
	for _, e := range entries {
		if !e.Healthy {
			allHealthy = false
			break
		}
	}
	if allHealthy {
		// Fully healthy = the iteration has settled; end the grace so any
		// LATER unhealthiness in this iteration surfaces immediately.
		infra.NoteSettled()
		return entries
	}
	if !infra.InReinitGrace() {
		return entries
	}
	for name, e := range entries {
		if !e.Healthy {
			entries[name] = introspection.ComponentHealth{
				Healthy: true,
				Error:   "reinitializing (grace period): " + e.Error,
			}
		}
	}
	return entries
}

func createEarlyHealthChecker(state *configState, infra *persistentInfra) func() map[string]introspection.ComponentHealth {
	return func() map[string]introspection.ComponentHealth {
		result := map[string]introspection.ComponentHealth{
			healthKeyInitialized: {Healthy: false, Error: msgStillInitializing},
		}
		if !state.IsLoaded() {
			result["config"] = introspection.ComponentHealth{
				Healthy: false,
				Error:   state.Message(),
			}
		} else {
			result["config"] = introspection.ComponentHealth{Healthy: true}
		}
		return applyReinitGrace(infra, result)
	}
}

// startEarlyInfrastructureServers starts debug and metrics HTTP servers early in startup.
// This function is called BEFORE fetching the initial configuration, so servers are available
// for debugging even if the controller fails to fetch config (e.g., RBAC issues).
//
// Unlike setupInfrastructureServers, this uses default/environment-based metrics port
// since the config hasn't been loaded yet.
//
// The introspection server persists across iterations to prevent port binding race conditions.
// On first iteration: Setup routes and start serving
// On subsequent iterations: Only update the health checker
//
// The introspection server uses two-phase initialization (Setup + Serve):
// 1. Register custom handlers (including /debug/events) before Setup()
// 2. Call Setup() to finalize routes
// 3. Call Serve() to start serving HTTP requests
//
// This pattern allows the /debug/events endpoint to be available while still starting
// health checks early for Kubernetes probes during the config waiting phase.
func startEarlyInfrastructureServers(
	ctx context.Context,
	debugPort int,
	infra *persistentInfra,
	setup *componentSetup,
	state *configState,
	eventBuffer *debug.EventBuffer,
	logger *slog.Logger,
) {
	// Copy server reference to setup for later use by other functions
	setup.IntrospectionServer = infra.IntrospectionServer

	// Set health checker to use this iteration's state
	infra.IntrospectionServer.SetHealthChecker(createEarlyHealthChecker(state, infra))

	if !infra.serverStarted {
		// First iteration: set up and start the introspection server
		logger.Info("Starting infrastructure servers (first iteration)")

		// Register /debug/events handler BEFORE Setup()
		// EventBuffer was created before this function to ensure proper subscription ordering
		debug.RegisterEventsHandler(infra.IntrospectionServer, eventBuffer)

		// Setup routes (including custom handlers) - must be called before Serve()
		infra.IntrospectionServer.Setup()

		// Start serving HTTP requests with the main context (not iteration context)
		// This ensures the server stays running across iterations
		go func() {
			if err := infra.IntrospectionServer.Serve(ctx); err != nil {
				logger.Error("Introspection server failed", "error", err, "port", debugPort)
			}
		}()

		logger.Info("Introspection HTTP server started (early startup)",
			"port", debugPort,
			"bind_address", fmt.Sprintf("0.0.0.0:%d", debugPort),
			"endpoints", "/healthz, /debug/vars, /debug/pprof, /debug/events")

		infra.serverStarted = true
	} else {
		// Subsequent iterations: health checker already updated above
		logger.Info("Reusing existing infrastructure servers (reinitialization)")
	}

	// Swap metrics registry for this iteration and start server if first time
	if infra.MetricsServer != nil {
		infra.MetricsServer.SetRegistry(setup.MetricsRegistry)

		if !infra.metricsServerStarted {
			go func() {
				if err := infra.MetricsServer.Start(ctx); err != nil {
					logger.Error("Metrics server failed", "error", err)
				}
			}()
			logger.Info("Metrics HTTP server started (first iteration)",
				"addr", infra.MetricsServer.Addr(),
				"endpoint", "/metrics")
			infra.metricsServerStarted = true
		} else {
			logger.Info("Metrics registry swapped (reinitialization)")
		}
	}
}

// setupInfrastructureServers registers debug variables after config is loaded.
// The introspection server is already started by startEarlyInfrastructureServers, so this
// function registers debug variables and updates the health checker to use the lifecycle registry.
//
// Note: /debug/events is already registered in startEarlyInfrastructureServers via two-phase
// initialization (Setup/Serve pattern), so it's available even during early startup.
//
// The pluggable-validator manager is consulted on every probe via its
// Healthy() method (sub-millisecond happy path). Each configured
// validator socket gets its own entry in the /healthz response so
// operators can tell which sidecar is broken when the probe fails.
func setupInfrastructureServers(
	ctx context.Context,
	setup *componentSetup,
	state *configState,
	infra *persistentInfra,
	stateCache *StateCache,
	eventBuffer *debug.EventBuffer, // Pre-created buffer (created before EventBus.Start())
	pluggableMgr *pluggablevalidator.Manager,
	logger *slog.Logger,
) {
	logger.Info("Stage 8: Registering debug variables and updating health checker")

	// Start event buffer (created before EventBus.Start() to ensure proper subscription)
	go func() {
		if err := eventBuffer.Start(ctx); err != nil {
			logger.Error("Event buffer failed", "error", err)
		}
	}()

	// Register debug variables with the shared introspection registry
	debug.RegisterVariables(setup.IntrospectionRegistry, stateCache, eventBuffer)

	// Update health checker to use the full lifecycle registry.
	// This replaces the initial simple health checker set in
	// startEarlyInfrastructureServers. See buildFullHealthChecker for
	// the readiness contract.
	setup.IntrospectionServer.SetHealthChecker(buildFullHealthChecker(setup.Registry, state, infra, pluggableMgr))

	logger.Info("Debug variables registered and health checker updated",
		"endpoints", "/debug/vars, /debug/pprof, /healthz")
}

// buildFullHealthChecker returns the /healthz callback installed once the
// staged startup is complete. The "initialized" entry is the authoritative
// "controller is ready to accept work" signal: it stays Healthy=false until
// BOTH conditions hold:
//
//  1. state.IsInitialized() — iteration setup finished wiring and starting
//     all components (set at the very end of runIteration).
//  2. Every lifecycle.Registry component has left the transient
//     Pending/Starting states. On a follower replica that means leader-only
//     components reached StatusStandby (which Registry.StartAll(ctx, false)
//     assigns synchronously) — so followers report /healthz 200 immediately
//     after their staged startup completes and kubelet does NOT kill them.
//     On the leader replica it means StartLeaderOnlyComponents finished
//     bringing the deployer / scheduler / coordinator / etc. up to
//     StatusRunning, i.e. the lease was acquired AND the leader-only chain
//     finished starting.
//
// This gives operators (and the e2e suite) a single /healthz 200 signal
// meaning the controller is genuinely operational, not just that
// runIteration's goroutine fan-out returned.
func buildFullHealthChecker(
	registry *lifecycle.Registry,
	state *configState,
	infra *persistentInfra,
	pluggableMgr *pluggablevalidator.Manager,
) func() map[string]introspection.ComponentHealth {
	return func() map[string]introspection.ComponentHealth {
		status := registry.Status()
		result := make(map[string]introspection.ComponentHealth, len(status)+2)
		firstPending := collectComponentHealth(status, result)
		mergePluggableValidatorHealth(result, pluggableMgr)
		result[healthKeyInitialized] = computeInitializedHealth(state.IsInitialized(), firstPending)
		return applyReinitGrace(infra, result)
	}
}

// collectComponentHealth copies each registered component's health into
// `result` and returns the name of the first component still in
// StatusPending / StatusStarting, or "" if all components have reached a
// terminal state. Returning the first-pending name (instead of just a
// boolean) lets operators see WHICH component is holding up readiness
// without scanning the whole map — invaluable when the e2e wait loop
// surfaces the /healthz body in CI logs.
func collectComponentHealth(
	status map[string]lifecycle.ComponentInfo,
	result map[string]introspection.ComponentHealth,
) string {
	var firstPending string
	for name, info := range status {
		// StatusStandby is healthy - component is intentionally not active
		// (e.g., leader-only components on non-leader pods)
		healthy := info.Status == lifecycle.StatusRunning || info.Status == lifecycle.StatusStandby
		if info.Healthy != nil {
			healthy = *info.Healthy
		}
		result[name] = introspection.ComponentHealth{
			Healthy: healthy,
			Error:   info.Error,
		}
		if firstPending == "" && (info.Status == lifecycle.StatusPending || info.Status == lifecycle.StatusStarting) {
			firstPending = name
		}
	}
	return firstPending
}

// computeInitializedHealth builds the "initialized" /healthz entry. It is
// only Healthy when iteration setup is done AND no component is still
// transitioning through Pending/Starting (firstPending == ""). The error
// message names the specific gate so operators can tell apart
// "still in staged startup" from "leader election hasn't acquired the
// lease yet (deployer pending)".
func computeInitializedHealth(initialized bool, firstPending string) introspection.ComponentHealth {
	healthy := initialized && firstPending == ""
	if healthy {
		return introspection.ComponentHealth{Healthy: true}
	}
	switch {
	case !initialized:
		return introspection.ComponentHealth{Healthy: false, Error: msgStillInitializing}
	default:
		return introspection.ComponentHealth{
			Healthy: false,
			Error:   fmt.Sprintf("waiting for components to start (e.g. %s still pending — leader election may not have acquired the lease yet)", firstPending),
		}
	}
}

// mergePluggableValidatorHealth adds a "pluggable-validators" entry to
// the health-checker map summarising the configured validator sockets.
// When no validators are configured the entry is omitted entirely so
// /healthz output stays unchanged for operators not using the feature.
//
// Behaviour when validators are configured:
//   - All sockets healthy → Healthy=true, no Error.
//   - Any socket unreachable → Healthy=false, Error lists every failing
//     "<name>: <reason>" entry semicolon-joined so operators can see in
//     one line which sidecar is broken.
//
// Sub-millisecond happy path so the probe stays cheap on every interval.
func mergePluggableValidatorHealth(
	result map[string]introspection.ComponentHealth,
	mgr *pluggablevalidator.Manager,
) {
	if mgr == nil || !mgr.Configured() {
		return
	}
	ok, failures := mgr.Healthy()
	entry := introspection.ComponentHealth{Healthy: ok}
	if !ok {
		entry.Error = strings.Join(failures, "; ")
	}
	result["pluggable-validators"] = entry
}
