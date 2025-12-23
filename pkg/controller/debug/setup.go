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

package debug

import (
	"net/http"
	"strconv"
	"time"

	"haptic/pkg/introspection"
)

// RegisterVariables registers all controller debug variables with the registry.
//
// This function should be called during controller initialization, after
// components are set up but before the debug server starts.
//
// Registered variables:
//   - config: Current controller configuration
//   - credentials: Credential metadata (not actual values)
//   - rendered: Last rendered HAProxy config
//   - auxfiles: Auxiliary files (SSL, maps, etc.)
//   - resources: Resource counts by type
//   - events: Recent events (default: last 100)
//   - state: Full state dump (use carefully - large response)
//   - uptime: Time since controller started
//   - pipeline: Reconciliation pipeline status (trigger, render, validate, deploy)
//   - validated: Last successfully validated HAProxy config
//   - errors: Aggregated error summary across all phases
//
// Example:
//
//	registry := introspection.NewRegistry()
//	eventBuffer := debug.NewEventBuffer(1000, bus)
//	debug.RegisterVariables(registry, controller, eventBuffer)
//
//	server := introspection.NewServer(":6060", registry)
//	go server.Start(ctx)
func RegisterVariables(
	registry *introspection.Registry,
	provider StateProvider,
	eventBuffer *EventBuffer,
) {
	// Core state variables
	registry.Publish("config", &ConfigVar{provider: provider})
	registry.Publish("credentials", &CredentialsVar{provider: provider})
	registry.Publish("rendered", &RenderedVar{provider: provider})
	registry.Publish("auxfiles", &AuxFilesVar{provider: provider})
	registry.Publish("resources", &ResourcesVar{provider: provider})

	// Events
	registry.Publish("events", &EventsVar{
		buffer:       eventBuffer,
		defaultLimit: 100,
	})

	// Full state dump (use carefully!)
	registry.Publish("state", &FullStateVar{
		provider:    provider,
		eventBuffer: eventBuffer,
	})

	// Pipeline status (for testing and debugging)
	registry.Publish("pipeline", &PipelineVar{provider: provider})
	registry.Publish("validated", &ValidatedVar{provider: provider})
	registry.Publish("errors", &ErrorsVar{provider: provider})

	// Uptime (computed on-demand)
	startTime := time.Now()
	registry.Publish("uptime", introspection.Func(func() (interface{}, error) {
		uptime := time.Since(startTime)
		return map[string]interface{}{
			"started":        startTime,
			"uptime_seconds": uptime.Seconds(),
			"uptime_string":  uptime.String(),
		}, nil
	}))
}

// RegisterEventsHandler registers the /debug/events endpoint with correlation query support.
//
// This handler supports:
//   - GET /debug/events - Returns last 100 events
//   - GET /debug/events?limit=N - Returns last N events
//   - GET /debug/events?correlation_id=<id> - Returns events with matching correlation ID
//
// Example:
//
//	debug.RegisterEventsHandler(server, eventBuffer)
//	go server.Start(ctx)
func RegisterEventsHandler(server *introspection.Server, eventBuffer *EventBuffer) {
	server.RegisterHandler("/debug/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			introspection.WriteError(w, http.StatusMethodNotAllowed, "only GET is allowed")
			return
		}

		// Check for correlation_id query parameter
		correlationID := r.URL.Query().Get("correlation_id")
		if correlationID != "" {
			events := eventBuffer.FindByCorrelationID(correlationID)
			introspection.WriteJSON(w, map[string]interface{}{
				"correlation_id": correlationID,
				"events":         events,
				"count":          len(events),
			})
			return
		}

		// Default: return last N events
		limit := 100
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
				limit = parsed
			}
		}

		events := eventBuffer.GetLast(limit)
		introspection.WriteJSON(w, map[string]interface{}{
			"events": events,
			"count":  len(events),
			"limit":  limit,
		})
	})
}
