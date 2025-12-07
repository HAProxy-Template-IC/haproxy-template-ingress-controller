package dataplane

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDiffDetails(t *testing.T) {
	details := NewDiffDetails()

	require.NotNil(t, details.ServersAdded)
	require.NotNil(t, details.ServersModified)
	require.NotNil(t, details.ServersDeleted)
	require.NotNil(t, details.ACLsAdded)
	require.NotNil(t, details.ACLsModified)
	require.NotNil(t, details.ACLsDeleted)
	require.NotNil(t, details.HTTPRulesAdded)
	require.NotNil(t, details.HTTPRulesModified)
	require.NotNil(t, details.HTTPRulesDeleted)

	assert.Empty(t, details.ServersAdded)
	assert.Equal(t, 0, details.TotalOperations)
}

func TestSyncResult_String(t *testing.T) {
	tests := []struct {
		name     string
		result   SyncResult
		contains []string
	}{
		{
			name: "success with operations",
			result: SyncResult{
				Success:  true,
				Duration: 500 * time.Millisecond,
				Retries:  1,
				AppliedOperations: []AppliedOperation{
					{Type: "create", Section: "backend", Resource: "api"},
					{Type: "create", Section: "server", Resource: "web1"},
				},
				Details: DiffDetails{
					TotalOperations: 2,
					Creates:         2,
					BackendsAdded:   []string{"api"},
				},
			},
			contains: []string{"SUCCESS", "500ms", "retries: 1", "Fine-grained sync", "Applied: 2 operations", "Creates: 2"},
		},
		{
			name: "failure with fallback",
			result: SyncResult{
				Success:       false,
				Duration:      1 * time.Second,
				FallbackToRaw: true,
				Message:       "fallback required due to conflict",
			},
			contains: []string{"FAILED", "1s", "Raw config push (fallback)", "Message: fallback required"},
		},
		{
			name: "success with reload",
			result: SyncResult{
				Success:         true,
				Duration:        200 * time.Millisecond,
				ReloadTriggered: true,
				ReloadID:        "reload-12345",
			},
			contains: []string{"SUCCESS", "Reload: Triggered (ID: reload-12345)"},
		},
		{
			name: "success with reload but no ID",
			result: SyncResult{
				Success:         true,
				Duration:        200 * time.Millisecond,
				ReloadTriggered: true,
			},
			contains: []string{"Reload: Triggered"},
		},
		{
			name: "success without reload (runtime API)",
			result: SyncResult{
				Success:         true,
				Duration:        100 * time.Millisecond,
				ReloadTriggered: false,
			},
			contains: []string{"Reload: Not triggered (runtime API used)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.result.String()

			for _, s := range tt.contains {
				assert.Contains(t, result, s, "expected to contain: %s", s)
			}
		})
	}
}

func TestDiffDetails_String(t *testing.T) {
	tests := []struct {
		name     string
		details  DiffDetails
		contains []string
	}{
		{
			name:     "no changes",
			details:  DiffDetails{TotalOperations: 0},
			contains: []string{"No changes detected"},
		},
		{
			name: "global and defaults changed",
			details: DiffDetails{
				TotalOperations: 2,
				GlobalChanged:   true,
				DefaultsChanged: true,
			},
			contains: []string{"Global settings modified", "Defaults modified"},
		},
		{
			name: "frontends changed",
			details: DiffDetails{
				TotalOperations:   3,
				FrontendsAdded:    []string{"http", "https"},
				FrontendsModified: []string{"stats"},
				FrontendsDeleted:  []string{"old-frontend"},
			},
			contains: []string{"Frontends added: http, https", "Frontends modified: stats", "Frontends deleted: old-frontend"},
		},
		{
			name: "backends changed",
			details: DiffDetails{
				TotalOperations:  2,
				BackendsAdded:    []string{"api-backend"},
				BackendsModified: []string{"web-backend"},
			},
			contains: []string{"Backends added: api-backend", "Backends modified: web-backend"},
		},
		{
			name: "servers changed across backends",
			details: DiffDetails{
				TotalOperations: 4,
				ServersAdded: map[string][]string{
					"api": {"server1", "server2"},
					"web": {"web1"},
				},
				ServersModified: map[string][]string{
					"api": {"server3"},
				},
			},
			contains: []string{"Servers added: 3", "Servers modified: 1"},
		},
		{
			name: "ACLs changed",
			details: DiffDetails{
				TotalOperations: 2,
				ACLsAdded: map[string][]string{
					"frontend-http": {"is_api", "is_static"},
				},
				ACLsDeleted: map[string][]string{
					"frontend-http": {"old_acl"},
				},
			},
			contains: []string{"ACLs added: 2", "ACLs deleted: 1"},
		},
		{
			name: "HTTP rules changed",
			details: DiffDetails{
				TotalOperations: 5,
				HTTPRulesAdded: map[string]int{
					"frontend-http": 3,
				},
				HTTPRulesModified: map[string]int{
					"frontend-http": 1,
				},
				HTTPRulesDeleted: map[string]int{
					"frontend-http": 1,
				},
			},
			contains: []string{"HTTP rules added: 3", "HTTP rules modified: 1", "HTTP rules deleted: 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.details.String()

			for _, s := range tt.contains {
				assert.Contains(t, result, s, "expected to contain: %s", s)
			}
		})
	}
}

func TestDiffResult_String(t *testing.T) {
	tests := []struct {
		name     string
		result   DiffResult
		contains []string
	}{
		{
			name: "no changes",
			result: DiffResult{
				HasChanges: false,
			},
			contains: []string{"No changes detected"},
		},
		{
			name: "has changes",
			result: DiffResult{
				HasChanges: true,
				PlannedOperations: []PlannedOperation{
					{Type: "create", Section: "backend", Resource: "api"},
					{Type: "update", Section: "frontend", Resource: "http"},
				},
				Details: DiffDetails{
					TotalOperations: 2,
					Creates:         1,
					Updates:         1,
					BackendsAdded:   []string{"api"},
				},
			},
			contains: []string{"Total operations: 2", "Backends added: api"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.result.String()

			for _, s := range tt.contains {
				assert.Contains(t, result, s, "expected to contain: %s", s)
			}
		})
	}
}

func TestDiffDetails_AppendResourceChanges(t *testing.T) {
	details := &DiffDetails{}
	parts := details.appendResourceChanges(
		[]string{},
		[]string{"item1", "item2"},
		[]string{"item3"},
		[]string{"item4", "item5", "item6"},
		"Resources",
	)

	require.Len(t, parts, 3)
	assert.Contains(t, parts[0], "Resources added: item1, item2")
	assert.Contains(t, parts[1], "Resources modified: item3")
	assert.Contains(t, parts[2], "Resources deleted: item4, item5, item6")
}

func TestDiffDetails_AppendResourceChanges_Empty(t *testing.T) {
	details := &DiffDetails{}
	parts := details.appendResourceChanges(
		[]string{},
		[]string{},
		[]string{},
		[]string{},
		"Resources",
	)

	assert.Empty(t, parts)
}

func TestDiffDetails_AppendMapCountChanges(t *testing.T) {
	details := &DiffDetails{}
	parts := details.appendMapCountChanges(
		[]string{},
		map[string][]string{"a": {"1", "2"}, "b": {"3"}},
		map[string][]string{"c": {"4"}},
		map[string][]string{"d": {"5", "6"}},
		"Items",
	)

	require.Len(t, parts, 3)
	assert.Contains(t, parts[0], "Items added: 3")
	assert.Contains(t, parts[1], "Items modified: 1")
	assert.Contains(t, parts[2], "Items deleted: 2")
}

func TestDiffDetails_AppendIntMapCountChanges(t *testing.T) {
	details := &DiffDetails{}
	parts := details.appendIntMapCountChanges(
		[]string{},
		map[string]int{"a": 5, "b": 3},
		map[string]int{"c": 2},
		map[string]int{"d": 1},
		"Rules",
	)

	require.Len(t, parts, 3)
	assert.Contains(t, parts[0], "Rules added: 8")
	assert.Contains(t, parts[1], "Rules modified: 2")
	assert.Contains(t, parts[2], "Rules deleted: 1")
}
