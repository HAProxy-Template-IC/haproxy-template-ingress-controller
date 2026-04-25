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

// ServerUpdateOp is a specialized op type (not built via the generic
// CRUD builders) so its accessor and contract methods need direct tests
// — they're the surface the orchestrator's runtime-optimized path
// reads to make version-cached executor calls.
//
// IsFullyRuntimeEligible is already covered by factory_server_test.go;
// the remaining methods (Type / Section / Priority / Describe /
// TriggeredReload / BackendName / ServerName / Server / CurrentServer)
// have no direct coverage.
func TestServerUpdateOp_AccessorsAndContract(t *testing.T) {
	port := int64(8080)
	current := &models.Server{Name: "srv", Address: "10.0.0.1", Port: &port}
	desired := &models.Server{Name: "srv", Address: "10.0.0.2", Port: &port}

	op := NewServerUpdate("api", current, desired).(*ServerUpdateOp)

	t.Run("Type is OperationUpdate", func(t *testing.T) {
		assert.Equal(t, OperationUpdate, op.Type())
	})

	t.Run("Section is 'server'", func(t *testing.T) {
		assert.Equal(t, "server", op.Section())
	})

	t.Run("Priority is PriorityServer * PriorityMultiplier", func(t *testing.T) {
		assert.Equal(t, PriorityServer*PriorityMultiplier, op.Priority())
	})

	t.Run("Describe matches the named-child format", func(t *testing.T) {
		// Updates use the 'in' preposition (opPreposition contract).
		assert.Equal(t, "Update server 'srv' in backend 'api'", op.Describe())
	})

	t.Run("BackendName / ServerName / Server / CurrentServer return constructor inputs", func(t *testing.T) {
		assert.Equal(t, "api", op.BackendName())
		assert.Equal(t, "srv", op.ServerName())
		assert.Same(t, desired, op.Server(), "Server() must return the desired model verbatim")
		assert.Same(t, current, op.CurrentServer(), "CurrentServer() must return the current model verbatim")
	})

	t.Run("TriggeredReload starts false and reports the last Execute outcome", func(t *testing.T) {
		// Pin that fresh ops report no reload yet — Execute mutates this
		// field, but at construction time it must be false so the
		// orchestrator's reload counter starts clean.
		assert.False(t, op.TriggeredReload(), "fresh op must report no reload until Execute runs")
	})
}

// Address-only changes are runtime-eligible per ServerIneligibleFields
// (covered in factory_server_runtime_test.go), so the constructor must
// pre-compute IsFullyRuntimeEligible=true for them. Add the per-server
// inverse case here so the construction-time eligibility computation is
// pinned for both branches.
func TestNewServerUpdate_PrecomputesEligibility(t *testing.T) {
	tests := []struct {
		name           string
		mutateDesired  func(*models.Server)
		wantEligible   bool
		wantEligReason string
	}{
		{
			name:           "address change is runtime-eligible",
			mutateDesired:  func(s *models.Server) { s.Address = "10.0.0.2" },
			wantEligible:   true,
			wantEligReason: "address is in serverRuntimeSupportedJSONFields",
		},
		{
			name:           "check change requires a reload",
			mutateDesired:  func(s *models.Server) { s.Check = "enabled" },
			wantEligible:   false,
			wantEligReason: "check is NOT in serverRuntimeSupportedJSONFields",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			port := int64(8080)
			current := &models.Server{Name: "srv", Address: "10.0.0.1", Port: &port}
			desired := &models.Server{Name: "srv", Address: "10.0.0.1", Port: &port}
			tt.mutateDesired(desired)

			op := NewServerUpdate("api", current, desired).(*ServerUpdateOp)
			assert.Equal(t, tt.wantEligible, op.IsFullyRuntimeEligible(), tt.wantEligReason)
		})
	}
}
