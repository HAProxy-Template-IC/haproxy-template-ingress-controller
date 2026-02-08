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
	"os"
	"strconv"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
	pkgmetrics "gitlab.com/haproxy-haptic/haptic/pkg/metrics"
)

// createEarlyHealthChecker creates a health checker that reports unhealthy until config is loaded.
// This is used during early startup before the full lifecycle-based checker is available.
func createEarlyHealthChecker(state *configState) func() map[string]introspection.ComponentHealth {
	return func() map[string]introspection.ComponentHealth {
		if !state.IsLoaded() {
			return map[string]introspection.ComponentHealth{
				"config": {
					Healthy: false,
					Error:   state.Message(),
				},
			}
		}
		return map[string]introspection.ComponentHealth{
			"config": {Healthy: true},
		}
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
	infra.IntrospectionServer.SetHealthChecker(createEarlyHealthChecker(state))

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
				logger.Error("introspection server failed", "error", err, "port", debugPort)
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

	// Start metrics HTTP server with default port
	// We use a default because config hasn't been loaded yet
	defaultMetricsPort := 9090
	if envPort := os.Getenv("METRICS_PORT"); envPort != "" {
		if port, err := strconv.Atoi(envPort); err == nil {
			defaultMetricsPort = port
		}
	}

	if defaultMetricsPort > 0 {
		metricsServer := pkgmetrics.NewServer(fmt.Sprintf(":%d", defaultMetricsPort), setup.MetricsRegistry)
		go func() {
			if err := metricsServer.Start(ctx); err != nil {
				logger.Error("metrics server failed", "error", err, "port", defaultMetricsPort)
			}
		}()
		logger.Info("Metrics HTTP server scheduled (early startup)",
			"port", defaultMetricsPort,
			"bind_address", fmt.Sprintf("0.0.0.0:%d", defaultMetricsPort),
			"endpoint", "/metrics")
	} else {
		logger.Debug("Metrics HTTP server disabled (port=0)")
	}
}

// setupInfrastructureServers registers debug variables after config is loaded.
// The introspection server is already started by startEarlyInfrastructureServers, so this
// function registers debug variables and updates the health checker to use the lifecycle registry.
//
// Note: /debug/events is already registered in startEarlyInfrastructureServers via two-phase
// initialization (Setup/Serve pattern), so it's available even during early startup.
func setupInfrastructureServers(
	ctx context.Context,
	setup *componentSetup,
	stateCache *StateCache,
	eventBuffer *debug.EventBuffer, // Pre-created buffer (created before EventBus.Start())
	logger *slog.Logger,
) {
	logger.Info("Stage 8: Registering debug variables and updating health checker")

	// Start event buffer (created before EventBus.Start() to ensure proper subscription)
	go func() {
		if err := eventBuffer.Start(ctx); err != nil {
			logger.Error("event buffer failed", "error", err)
		}
	}()

	// Register debug variables with the shared introspection registry
	debug.RegisterVariables(setup.IntrospectionRegistry, stateCache, eventBuffer)

	// Update health checker to use the full lifecycle registry
	// This replaces the initial simple health checker set in startEarlyInfrastructureServers
	setup.IntrospectionServer.SetHealthChecker(func() map[string]introspection.ComponentHealth {
		status := setup.Registry.Status()
		result := make(map[string]introspection.ComponentHealth, len(status))
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
		}
		return result
	})

	logger.Info("Debug variables registered and health checker updated",
		"endpoints", "/debug/vars, /debug/pprof, /healthz")
}
