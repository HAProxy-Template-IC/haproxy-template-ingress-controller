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

	"gitlab.com/haproxy-haptic/haptic/pkg/introspection"
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
	// Core state variables. Each is a Func closure capturing the provider;
	// the response shapes match the documented /debug/vars/<name> contracts.

	// config: current controller configuration + CRD resource version.
	registry.Publish("config", introspection.Func(func() (any, error) {
		cfg, version, err := provider.GetConfig()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			keyConfig:  cfg,
			keyVersion: version,
			"updated":  time.Now(),
		}, nil
	}))

	// credentials: metadata only — never exposes actual credential values.
	registry.Publish("credentials", introspection.Func(func() (any, error) {
		creds, version, err := provider.GetCredentials()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			keyVersion:            version,
			"updated":             time.Now(),
			"has_dataplane_creds": creds != nil && creds.DataplaneUsername != "" && creds.DataplanePassword != "",
		}, nil
	}))

	// rendered: most recently rendered HAProxy config + size.
	registry.Publish("rendered", introspection.Func(func() (any, error) {
		rendered, timestamp, err := provider.GetRenderedConfig()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			keyConfig:    rendered,
			keyTimestamp: timestamp,
			"size":       len(rendered),
		}, nil
	}))

	// auxfiles: auxiliary files from the last deployment + per-type counts.
	registry.Publish(keyAuxFiles, introspection.Func(func() (any, error) {
		auxFiles, timestamp, err := provider.GetAuxiliaryFiles()
		if err != nil {
			return nil, err
		}
		summary := map[string]int{
			"ssl_count":     len(auxFiles.SSLCertificates),
			"map_count":     len(auxFiles.MapFiles),
			"general_count": len(auxFiles.GeneralFiles),
		}
		return map[string]any{
			"files":      auxFiles,
			keyTimestamp: timestamp,
			"summary":    summary,
		}, nil
	}))

	// resources: resource counts by type.
	registry.Publish("resources", introspection.Func(func() (any, error) {
		return provider.GetResourceCounts()
	}))

	registry.Publish(keyEvents, &EventsVar{
		buffer:       eventBuffer,
		defaultLimit: 100,
	})

	// Full state dump (use carefully! Large response). Best effort — does not
	// fail if some parts are unavailable.
	registry.Publish("state", introspection.Func(func() (any, error) {
		cfg, cfgVer, _ := provider.GetConfig()
		rendered, renderedTime, _ := provider.GetRenderedConfig()
		auxFiles, auxTime, _ := provider.GetAuxiliaryFiles()
		resources, _ := provider.GetResourceCounts()

		recentEvents := []Event{}
		if eventBuffer != nil {
			recentEvents = eventBuffer.GetLast(100)
		}

		return map[string]any{
			keyConfig: map[string]any{
				keyConfig:  cfg,
				keyVersion: cfgVer,
			},
			"rendered": map[string]any{
				keyConfig:    rendered,
				keyTimestamp: renderedTime,
			},
			keyAuxFiles: map[string]any{
				"files":      auxFiles,
				keyTimestamp: auxTime,
			},
			"resources":     resources,
			"recent_events": recentEvents,
			"snapshot_time": time.Now(),
		}, nil
	}))

	// Pipeline status (for testing and debugging)
	registry.Publish("pipeline", introspection.Func(func() (any, error) {
		return provider.GetPipelineStatus()
	}))
	registry.Publish("validated", introspection.Func(func() (any, error) {
		return provider.GetValidatedConfig()
	}))
	registry.Publish("errors", introspection.Func(func() (any, error) {
		return provider.GetErrors()
	}))

	// Uptime (computed on-demand)
	startTime := time.Now()
	registry.Publish("uptime", introspection.Func(func() (any, error) {
		uptime := time.Since(startTime)
		return map[string]any{
			"started":        startTime,
			"uptime_seconds": uptime.Seconds(),
			"uptime_string":  uptime.String(),
		}, nil
	}))
}

// EventSource provides the event queries exposed by /debug/events.
type EventSource interface {
	GetLast(int) []Event
	FindByCorrelationID(string) []Event
}

// EventsHandler returns the /debug/events handler.
func EventsHandler(eventSource EventSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			introspection.WriteError(w, http.StatusMethodNotAllowed, "only GET is allowed")
			return
		}

		correlationID := r.URL.Query().Get("correlation_id")
		if correlationID != "" {
			events := eventSource.FindByCorrelationID(correlationID)
			introspection.WriteJSON(w, map[string]any{
				"correlation_id": correlationID,
				keyEvents:        events,
				"count":          len(events),
			})
			return
		}

		limit := 100
		if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
			if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
				limit = parsed
			}
		}

		events := eventSource.GetLast(limit)
		introspection.WriteJSON(w, map[string]any{
			keyEvents: events,
			"count":   len(events),
			"limit":   limit,
		})
	}
}

// RegisterEventsHandler registers the process-owned /debug/events endpoint.
func RegisterEventsHandler(server *introspection.Server, eventSource EventSource) {
	server.RegisterHandler("/debug/events", EventsHandler(eventSource))
}
