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
)

// effectivePriority is defined in factory_test.go.

// LogProfile (HAProxy DataPlane API v3.1+) — top-level CRUD factories.
// Uses the same NewTopLevelCRUD shape as backend/frontend so a single
// table-driven test pins type/section/priority/description for every CRUD
// arm. Without this, a future refactor that swapped the underlying CRUD
// builder could silently change priority or section name without breaking
// existing tests.
func TestLogProfileFactoryFunctions(t *testing.T) {
	logProfile := &models.LogProfile{Name: "audit"}

	tests := []struct {
		name             string
		factory          func(*models.LogProfile) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{name: "LogProfileOps.Create", factory: LogProfileOps.Create, wantType: OperationCreate, wantDescContains: "Create log-profile 'audit'"},
		{name: "LogProfileOps.Update", factory: LogProfileOps.Update, wantType: OperationUpdate, wantDescContains: "Update log-profile 'audit'"},
		{name: "LogProfileOps.Delete", factory: LogProfileOps.Delete, wantType: OperationDelete, wantDescContains: "Delete log-profile 'audit'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(logProfile)
			assertOperation(t, op, tt.wantType, "log_profile", tt.wantDescContains)
		})
	}
}

// AcmeProvider (HAProxy DataPlane API v3.2+) — top-level CRUD factories.
func TestAcmeProviderFactoryFunctions(t *testing.T) {
	acme := &models.AcmeProvider{Name: "letsencrypt"}

	tests := []struct {
		name             string
		factory          func(*models.AcmeProvider) Operation
		wantType         OperationType
		wantDescContains string
	}{
		{name: "AcmeProviderOps.Create", factory: AcmeProviderOps.Create, wantType: OperationCreate, wantDescContains: "Create acme-provider 'letsencrypt'"},
		{name: "AcmeProviderOps.Update", factory: AcmeProviderOps.Update, wantType: OperationUpdate, wantDescContains: "Update acme-provider 'letsencrypt'"},
		{name: "AcmeProviderOps.Delete", factory: AcmeProviderOps.Delete, wantType: OperationDelete, wantDescContains: "Delete acme-provider 'letsencrypt'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := tt.factory(acme)
			assertOperation(t, op, tt.wantType, "acme_provider", tt.wantDescContains)
		})
	}
}

// Traces (HAProxy DataPlane API v3.1+) — singleton, only an update factory
// exists because the section either is or isn't present (Traces never has a
// Name to differentiate creates/deletes). Pin the singleton-style
// description and the PriorityTraces wiring.
func TestNewTracesUpdate(t *testing.T) {
	traces := &models.Traces{}
	op := NewTracesUpdate(traces)

	assertOperation(t, op, OperationUpdate, "traces", "Update traces section")
}
