// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Six concrete event types in this package implement the
// CoalescibleEvent interface from pkg/events. The coalesce package
// (pkg/controller/coalesce) tests its drain logic with a mock event
// type but never against the real production events — so a refactor
// that flipped the polarity of one Coalescible() return value, or
// silently dropped the coalescible field from a constructor, would
// pass coalesce_test.go and only blow up at runtime by either:
//
//   - dropping commands (e.g. drift_prevention) that must be processed,
//     because they were treated as state updates and coalesced away; or
//   - causing reconciliation thrashing for state updates because they
//     stopped coalescing.
//
// Pin the contract:
//
//   - The two ALWAYS-true events (HAProxyPodsDiscoveredEvent,
//     HTTPResourceUpdatedEvent) hard-code true. They are pure state
//     updates: only the latest pod list / latest HTTP content matters.
//   - The four CONFIGURABLE events take a coalescible bool in their
//     constructor and Coalescible() must return that exact value.
//     This is the bool the Reconciler / pipeline use to mark commands
//     vs state updates throughout the reconciliation chain.
//
// The implements-CoalescibleEvent assertion belongs here too — the
// coalesce package will type-assert on the interface, so missing
// the interface check entirely is also a regression worth catching.
func TestCoalescibleContract(t *testing.T) {
	tests := []struct {
		name    string
		event   busevents.Event
		wantVal bool
	}{
		// Always-true events: state updates that must always coalesce.
		{
			name:    "HAProxyPodsDiscoveredEvent always coalescible (pure state, only latest pod list matters)",
			event:   NewHAProxyPodsDiscoveredEvent(nil, 0),
			wantVal: true,
		},
		{
			name:    "HTTPResourceUpdatedEvent always coalescible (pure state, only latest content matters)",
			event:   NewHTTPResourceUpdatedEvent("http://example.com", "checksum-x", 0),
			wantVal: true,
		},

		// Configurable events: round-trip the constructor's bool.
		// Pin BOTH polarities for each so a hardcoded-true bug
		// (e.g. accidentally always returning true and ignoring the
		// stored field) couldn't pass.
		{
			name:    "ReconciliationTriggeredEvent honours coalescible=true (state update, e.g. debounce_timer)",
			event:   NewReconciliationTriggeredEvent("debounce_timer", true),
			wantVal: true,
		},
		{
			name:    "ReconciliationTriggeredEvent honours coalescible=false (command, e.g. drift_prevention)",
			event:   NewReconciliationTriggeredEvent("drift_prevention", false),
			wantVal: false,
		},
		{
			name:    "TemplateRenderedEvent honours coalescible=true",
			event:   NewTemplateRenderedEvent("cfg", nil, nil, 0, 0, "", "", true),
			wantVal: true,
		},
		{
			name:    "TemplateRenderedEvent honours coalescible=false",
			event:   NewTemplateRenderedEvent("cfg", nil, nil, 0, 0, "", "", false),
			wantVal: false,
		},
		{
			name:    "ValidationCompletedEvent honours coalescible=true",
			event:   NewValidationCompletedEvent(nil, 0, "", nil, true),
			wantVal: true,
		},
		{
			name:    "ValidationCompletedEvent honours coalescible=false",
			event:   NewValidationCompletedEvent(nil, 0, "", nil, false),
			wantVal: false,
		},
		{
			name:    "DeploymentScheduledEvent honours coalescible=true",
			event:   NewDeploymentScheduledEvent("", nil, nil, nil, "", "", "", "", true),
			wantVal: true,
		},
		{
			name:    "DeploymentScheduledEvent honours coalescible=false",
			event:   NewDeploymentScheduledEvent("", nil, nil, nil, "", "", "", "", false),
			wantVal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coalescible, ok := tt.event.(busevents.CoalescibleEvent)
			if !assert.True(t, ok,
				"%T must implement busevents.CoalescibleEvent so the coalesce package can drain on it",
				tt.event) {
				return
			}
			assert.Equal(t, tt.wantVal, coalescible.Coalescible(),
				"Coalescible() must mirror the constructor's stored value; "+
					"a flipped polarity would cause coalesce to either drop commands "+
					"or thrash on state updates")
		})
	}
}
