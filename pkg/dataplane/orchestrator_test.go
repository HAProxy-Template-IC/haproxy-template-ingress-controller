package dataplane

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/dataplane/client"
	"haproxy-template-ic/pkg/dataplane/comparator"
	"haproxy-template-ic/pkg/dataplane/comparator/sections"
)

// mockOperation implements comparator.Operation for testing.
type mockOperation struct {
	opType   sections.OperationType
	section  string
	priority int
	desc     string
}

func (m *mockOperation) Type() sections.OperationType { return m.opType }
func (m *mockOperation) Section() string              { return m.section }
func (m *mockOperation) Priority() int                { return m.priority }
func (m *mockOperation) Describe() string             { return m.desc }
func (m *mockOperation) Execute(_ context.Context, _ *client.DataplaneClient, _ string) error {
	return nil
}

func TestOperationTypeToString(t *testing.T) {
	tests := []struct {
		name   string
		opType sections.OperationType
		want   string
	}{
		{
			name:   "create operation",
			opType: sections.OperationCreate,
			want:   "create",
		},
		{
			name:   "update operation",
			opType: sections.OperationUpdate,
			want:   "update",
		},
		{
			name:   "delete operation",
			opType: sections.OperationDelete,
			want:   "delete",
		},
		{
			name:   "unknown operation type",
			opType: sections.OperationType(99),
			want:   "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := operationTypeToString(tt.opType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractResourceName(t *testing.T) {
	tests := []struct {
		name string
		op   comparator.Operation
		want string
	}{
		{
			name: "standard description format",
			op: &mockOperation{
				desc: "Create backend 'api-backend'",
			},
			want: "api-backend",
		},
		{
			name: "delete description",
			op: &mockOperation{
				desc: "Delete server 'web1' from backend 'api'",
			},
			want: "web1",
		},
		{
			name: "no quotes in description",
			op: &mockOperation{
				desc: "Update server web1",
			},
			want: "unknown",
		},
		{
			name: "single quote at end only",
			op: &mockOperation{
				desc: "Update backend api'",
			},
			want: "unknown",
		},
		{
			name: "empty description",
			op: &mockOperation{
				desc: "",
			},
			want: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractResourceName(tt.op)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConvertOperationsToApplied(t *testing.T) {
	ops := []comparator.Operation{
		&mockOperation{
			opType:   sections.OperationCreate,
			section:  "backend",
			priority: 10,
			desc:     "Create backend 'api'",
		},
		&mockOperation{
			opType:   sections.OperationUpdate,
			section:  "server",
			priority: 20,
			desc:     "Update server 'web1'",
		},
		&mockOperation{
			opType:   sections.OperationDelete,
			section:  "acl",
			priority: 30,
			desc:     "Delete ACL 'old_acl'",
		},
	}

	applied := convertOperationsToApplied(ops)

	require.Len(t, applied, 3)

	assert.Equal(t, "create", applied[0].Type)
	assert.Equal(t, "backend", applied[0].Section)
	assert.Equal(t, "api", applied[0].Resource)
	assert.Equal(t, "Create backend 'api'", applied[0].Description)

	assert.Equal(t, "update", applied[1].Type)
	assert.Equal(t, "server", applied[1].Section)
	assert.Equal(t, "web1", applied[1].Resource)

	assert.Equal(t, "delete", applied[2].Type)
	assert.Equal(t, "acl", applied[2].Section)
	assert.Equal(t, "old_acl", applied[2].Resource)
}

func TestConvertOperationsToApplied_Empty(t *testing.T) {
	applied := convertOperationsToApplied([]comparator.Operation{})

	require.Empty(t, applied)
}

func TestConvertOperationsToPlanned(t *testing.T) {
	ops := []comparator.Operation{
		&mockOperation{
			opType:   sections.OperationCreate,
			section:  "frontend",
			priority: 5,
			desc:     "Create frontend 'http'",
		},
		&mockOperation{
			opType:   sections.OperationUpdate,
			section:  "bind",
			priority: 15,
			desc:     "Update bind 'https'",
		},
	}

	planned := convertOperationsToPlanned(ops)

	require.Len(t, planned, 2)

	assert.Equal(t, "create", planned[0].Type)
	assert.Equal(t, "frontend", planned[0].Section)
	assert.Equal(t, "http", planned[0].Resource)
	assert.Equal(t, 5, planned[0].Priority)

	assert.Equal(t, "update", planned[1].Type)
	assert.Equal(t, "bind", planned[1].Section)
	assert.Equal(t, "https", planned[1].Resource)
	assert.Equal(t, 15, planned[1].Priority)
}

func TestConvertDiffSummary(t *testing.T) {
	summary := &comparator.DiffSummary{
		TotalCreates:      5,
		TotalUpdates:      3,
		TotalDeletes:      2,
		GlobalChanged:     true,
		DefaultsChanged:   true,
		FrontendsAdded:    []string{"http", "https"},
		FrontendsModified: []string{"stats"},
		FrontendsDeleted:  []string{"old"},
		BackendsAdded:     []string{"api"},
		BackendsModified:  []string{"web"},
		BackendsDeleted:   nil,
		ServersAdded:      map[string][]string{"api": {"srv1", "srv2"}},
		ServersModified:   map[string][]string{"web": {"srv3"}},
		ServersDeleted:    map[string][]string{"old": {"srv4"}},
	}

	details := convertDiffSummary(summary)

	assert.Equal(t, 10, details.TotalOperations)
	assert.Equal(t, 5, details.Creates)
	assert.Equal(t, 3, details.Updates)
	assert.Equal(t, 2, details.Deletes)
	assert.True(t, details.GlobalChanged)
	assert.True(t, details.DefaultsChanged)
	assert.Equal(t, []string{"http", "https"}, details.FrontendsAdded)
	assert.Equal(t, []string{"stats"}, details.FrontendsModified)
	assert.Equal(t, []string{"old"}, details.FrontendsDeleted)
	assert.Equal(t, []string{"api"}, details.BackendsAdded)
	assert.Equal(t, []string{"web"}, details.BackendsModified)
	assert.Nil(t, details.BackendsDeleted)
	assert.Equal(t, []string{"srv1", "srv2"}, details.ServersAdded["api"])
	assert.Equal(t, []string{"srv3"}, details.ServersModified["web"])
	assert.Equal(t, []string{"srv4"}, details.ServersDeleted["old"])
	require.NotNil(t, details.ACLsAdded)
	require.NotNil(t, details.HTTPRulesAdded)
}

func TestConvertDiffSummary_Empty(t *testing.T) {
	summary := &comparator.DiffSummary{}

	details := convertDiffSummary(summary)

	assert.Equal(t, 0, details.TotalOperations)
	assert.False(t, details.GlobalChanged)
	assert.False(t, details.DefaultsChanged)
	require.NotNil(t, details.ACLsAdded)
	require.NotNil(t, details.HTTPRulesAdded)
}
