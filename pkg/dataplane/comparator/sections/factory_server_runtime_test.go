// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sections

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// ServerIneligibleFields drives the runtime-vs-reload decision in the
// orchestrator: a non-empty result means the runtime-optimized path must be
// skipped because at least one changed field requires a HAProxy reload. The
// field set must be kept in sync with serverRuntimeSupportedJSONFields and
// with buildRuntimeActions in orchestrator_execution.go — this test pins the
// observable contract so accidental drift is caught.
func TestServerIneligibleFields(t *testing.T) {
	mkServer := func(addr string, port int64, weight int64, maint string, check string) *models.Server {
		s := &models.Server{
			Name:    "srv",
			Address: addr,
			Port:    &port,
		}
		s.Weight = &weight
		s.Maintenance = maint
		s.Check = check
		return s
	}

	tests := []struct {
		name           string
		current        *models.Server
		desired        *models.Server
		wantIneligible []string
	}{
		{
			name:           "identical servers have no ineligible fields",
			current:        mkServer("10.0.0.1", 8080, 100, "", ""),
			desired:        mkServer("10.0.0.1", 8080, 100, "", ""),
			wantIneligible: nil,
		},
		{
			name:           "address change is runtime-eligible (no reload)",
			current:        mkServer("10.0.0.1", 8080, 100, "", ""),
			desired:        mkServer("10.0.0.2", 8080, 100, "", ""),
			wantIneligible: nil,
		},
		{
			name:           "port change is runtime-eligible (no reload)",
			current:        mkServer("10.0.0.1", 8080, 100, "", ""),
			desired:        mkServer("10.0.0.1", 9090, 100, "", ""),
			wantIneligible: nil,
		},
		{
			name:           "weight change is runtime-eligible (no reload)",
			current:        mkServer("10.0.0.1", 8080, 100, "", ""),
			desired:        mkServer("10.0.0.1", 8080, 50, "", ""),
			wantIneligible: nil,
		},
		{
			name:           "maintenance toggle is runtime-eligible (slot-swap pattern)",
			current:        mkServer("10.0.0.1", 8080, 100, "enabled", ""),
			desired:        mkServer("10.0.0.1", 8080, 100, "disabled", ""),
			wantIneligible: nil,
		},
		{
			// 'check' on a per-server line is the canonical reason the
			// runtime-optimized path gets skipped in production. Templates
			// should move 'check' to default-server to avoid this.
			name:           "check change requires reload",
			current:        mkServer("10.0.0.1", 8080, 100, "", "enabled"),
			desired:        mkServer("10.0.0.1", 8080, 100, "", "disabled"),
			wantIneligible: []string{"check"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServerIneligibleFields(tt.current, tt.desired)
			assert.ElementsMatch(t, tt.wantIneligible, got,
				"ineligible fields (order-insensitive)")
			// Mirror invariant: computeServerRuntimeEligibility must agree
			// with the emptiness of ServerIneligibleFields.
			assert.Equal(t, len(tt.wantIneligible) == 0, computeServerRuntimeEligibility(tt.current, tt.desired),
				"computeServerRuntimeEligibility must agree with ServerIneligibleFields emptiness")
		})
	}
}

// Edge case: a field present only in the desired server (not in current) must
// also be classified. The "new field with null value" branch is special: a
// null value means "no change" semantically, so it must NOT show up as
// ineligible. Pin both sides of that branch so a future refactor can't
// silently classify nil-as-default differences as reload-required.
func TestServerIneligibleFields_FieldOnlyInDesired(t *testing.T) {
	current := &models.Server{Name: "srv", Address: "10.0.0.1"}

	t.Run("desired adds runtime-eligible field (port) -> still eligible", func(t *testing.T) {
		port := int64(8080)
		desired := &models.Server{Name: "srv", Address: "10.0.0.1", Port: &port}
		assert.True(t, computeServerRuntimeEligibility(current, desired))
		assert.Empty(t, ServerIneligibleFields(current, desired))
	})

	t.Run("desired adds reload-required field (check) -> ineligible", func(t *testing.T) {
		desired := &models.Server{Name: "srv", Address: "10.0.0.1"}
		desired.Check = "enabled"
		got := ServerIneligibleFields(current, desired)
		assert.Contains(t, got, "check")
		assert.False(t, computeServerRuntimeEligibility(current, desired))
	})
}
