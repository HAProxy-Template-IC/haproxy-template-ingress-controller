package comparator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/comparator/sections"
)

// mockTestOperation implements Operation for testing OrderOperations.
type mockTestOperation struct {
	opType   sections.OperationType
	section  string
	priority int
	desc     string
}

func (m *mockTestOperation) Type() sections.OperationType { return m.opType }
func (m *mockTestOperation) Section() string              { return m.section }
func (m *mockTestOperation) Priority() int                { return m.priority }
func (m *mockTestOperation) Describe() string             { return m.desc }
func (m *mockTestOperation) Execute(_ context.Context, _ *client.DataplaneClient, _ string) error {
	return nil
}

// newTestOp creates a mock operation for testing.
func newTestOp(opType sections.OperationType, section string, priority int) *mockTestOperation {
	return &mockTestOperation{
		opType:   opType,
		section:  section,
		priority: priority,
		desc:     section + " op",
	}
}

// TestNewDiffSummary tests the NewDiffSummary constructor.
func TestNewDiffSummary(t *testing.T) {
	summary := NewDiffSummary()

	assert.NotNil(t, summary.ServersAdded)
	assert.NotNil(t, summary.ServersModified)
	assert.NotNil(t, summary.ServersDeleted)
	assert.NotNil(t, summary.OtherChanges)
	assert.Equal(t, 0, summary.TotalCreates)
	assert.Equal(t, 0, summary.TotalUpdates)
	assert.Equal(t, 0, summary.TotalDeletes)
}

// TestDiffSummary_HasChanges tests the HasChanges method.
func TestDiffSummary_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		summary  DiffSummary
		expected bool
	}{
		{
			name:     "no changes",
			summary:  DiffSummary{},
			expected: false,
		},
		{
			name:     "has creates",
			summary:  DiffSummary{TotalCreates: 1},
			expected: true,
		},
		{
			name:     "has updates",
			summary:  DiffSummary{TotalUpdates: 1},
			expected: true,
		},
		{
			name:     "has deletes",
			summary:  DiffSummary{TotalDeletes: 1},
			expected: true,
		},
		{
			name:     "has all",
			summary:  DiffSummary{TotalCreates: 1, TotalUpdates: 2, TotalDeletes: 3},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.summary.HasChanges())
		})
	}
}

// TestDiffSummary_TotalOperations tests the TotalOperations method.
func TestDiffSummary_TotalOperations(t *testing.T) {
	tests := []struct {
		name     string
		summary  DiffSummary
		expected int
	}{
		{
			name:     "no operations",
			summary:  DiffSummary{},
			expected: 0,
		},
		{
			name:     "creates only",
			summary:  DiffSummary{TotalCreates: 5},
			expected: 5,
		},
		{
			name:     "mixed operations",
			summary:  DiffSummary{TotalCreates: 2, TotalUpdates: 3, TotalDeletes: 4},
			expected: 9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.summary.TotalOperations())
		})
	}
}

// TestDiffSummary_String tests the String method.
func TestDiffSummary_String(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		summary := DiffSummary{}
		assert.Equal(t, "No changes", summary.String())
	})

	t.Run("basic changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates: 2,
			TotalUpdates: 1,
			TotalDeletes: 0,
		}
		str := summary.String()
		assert.Contains(t, str, "Total: 3 operations")
		assert.Contains(t, str, "2 creates")
		assert.Contains(t, str, "1 updates")
	})

	t.Run("global changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalUpdates:  1,
			GlobalChanged: true,
		}
		str := summary.String()
		assert.Contains(t, str, "Global settings modified")
	})

	t.Run("defaults changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalUpdates:    1,
			DefaultsChanged: true,
		}
		str := summary.String()
		assert.Contains(t, str, "Defaults modified")
	})

	t.Run("frontend changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates:      2,
			TotalDeletes:      1,
			FrontendsAdded:    []string{"http", "https"},
			FrontendsDeleted:  []string{"old-frontend"},
			FrontendsModified: []string{"main"},
		}
		str := summary.String()
		assert.Contains(t, str, "Frontends added: http, https")
		assert.Contains(t, str, "Frontends deleted: old-frontend")
	})

	t.Run("backend changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates:     1,
			BackendsAdded:    []string{"api"},
			BackendsModified: []string{"web"},
		}
		str := summary.String()
		assert.Contains(t, str, "Backends added: api")
	})

	t.Run("server changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates: 2,
			ServersAdded: map[string][]string{
				"backend1": {"srv1", "srv2"},
			},
			ServersModified: make(map[string][]string),
			ServersDeleted:  make(map[string][]string),
		}
		str := summary.String()
		assert.Contains(t, str, "Servers added")
		assert.Contains(t, str, "backend1: 2")
	})
}

// TestOrderOperations_EmptyList tests OrderOperations with empty input.
func TestOrderOperations_EmptyList(t *testing.T) {
	result := OrderOperations(nil)
	assert.Empty(t, result)

	result = OrderOperations([]Operation{})
	assert.Empty(t, result)
}

// TestOrderOperations_SingleOperation tests OrderOperations with single operation.
func TestOrderOperations_SingleOperation(t *testing.T) {
	ops := []Operation{
		newTestOp(sections.OperationCreate, "backend", 20),
	}

	result := OrderOperations(ops)

	require.Len(t, result, 1)
	assert.Equal(t, "backend", result[0].Section())
}

// TestOrderOperations_DeletesBeforeCreates tests that deletes come before creates.
func TestOrderOperations_DeletesBeforeCreates(t *testing.T) {
	ops := []Operation{
		newTestOp(sections.OperationCreate, "backend", 20),
		newTestOp(sections.OperationDelete, "frontend", 10),
		newTestOp(sections.OperationCreate, "server", 30),
		newTestOp(sections.OperationDelete, "acl", 50),
	}

	result := OrderOperations(ops)

	require.Len(t, result, 4)

	// First two should be deletes
	assert.Equal(t, sections.OperationDelete, result[0].Type())
	assert.Equal(t, sections.OperationDelete, result[1].Type())

	// Last two should be creates
	assert.Equal(t, sections.OperationCreate, result[2].Type())
	assert.Equal(t, sections.OperationCreate, result[3].Type())
}

// TestOrderOperations_UpdatesAfterCreates tests that updates come after creates.
func TestOrderOperations_UpdatesAfterCreates(t *testing.T) {
	ops := []Operation{
		newTestOp(sections.OperationUpdate, "backend", 20),
		newTestOp(sections.OperationCreate, "frontend", 10),
		newTestOp(sections.OperationUpdate, "server", 30),
	}

	result := OrderOperations(ops)

	require.Len(t, result, 3)

	// First should be create
	assert.Equal(t, sections.OperationCreate, result[0].Type())

	// Last two should be updates
	assert.Equal(t, sections.OperationUpdate, result[1].Type())
	assert.Equal(t, sections.OperationUpdate, result[2].Type())
}

// TestOrderOperations_DeletesPriorityDescending tests that deletes are sorted by descending priority.
func TestOrderOperations_DeletesPriorityDescending(t *testing.T) {
	// Higher priority deletes first (children before parents)
	ops := []Operation{
		newTestOp(sections.OperationDelete, "backend", 20),
		newTestOp(sections.OperationDelete, "server", 30),
		newTestOp(sections.OperationDelete, "acl", 50),
		newTestOp(sections.OperationDelete, "frontend", 10),
	}

	result := OrderOperations(ops)

	require.Len(t, result, 4)

	// Should be ordered by descending priority: acl(50), server(30), backend(20), frontend(10)
	assert.Equal(t, 50, result[0].Priority()) // acl
	assert.Equal(t, 30, result[1].Priority()) // server
	assert.Equal(t, 20, result[2].Priority()) // backend
	assert.Equal(t, 10, result[3].Priority()) // frontend
}

// TestOrderOperations_CreatesPriorityAscending tests that creates are sorted by ascending priority.
func TestOrderOperations_CreatesPriorityAscending(t *testing.T) {
	// Lower priority creates first (parents before children)
	ops := []Operation{
		newTestOp(sections.OperationCreate, "server", 30),
		newTestOp(sections.OperationCreate, "acl", 50),
		newTestOp(sections.OperationCreate, "backend", 20),
		newTestOp(sections.OperationCreate, "frontend", 10),
	}

	result := OrderOperations(ops)

	require.Len(t, result, 4)

	// Should be ordered by ascending priority: frontend(10), backend(20), server(30), acl(50)
	assert.Equal(t, 10, result[0].Priority()) // frontend
	assert.Equal(t, 20, result[1].Priority()) // backend
	assert.Equal(t, 30, result[2].Priority()) // server
	assert.Equal(t, 50, result[3].Priority()) // acl
}

// TestOrderOperations_FullExample tests a realistic mix of operations.
func TestOrderOperations_FullExample(t *testing.T) {
	ops := []Operation{
		newTestOp(sections.OperationCreate, "frontend", 10),
		newTestOp(sections.OperationDelete, "old-server", 30),
		newTestOp(sections.OperationUpdate, "backend", 20),
		newTestOp(sections.OperationCreate, "server", 30),
		newTestOp(sections.OperationDelete, "old-backend", 20),
		newTestOp(sections.OperationUpdate, "frontend", 10),
	}

	result := OrderOperations(ops)

	require.Len(t, result, 6)

	// Expected order:
	// 1. Deletes (descending priority): old-server(30), old-backend(20)
	// 2. Creates (ascending priority): frontend(10), server(30)
	// 3. Updates: backend(20), frontend(10)

	// First two are deletes
	assert.Equal(t, sections.OperationDelete, result[0].Type())
	assert.Equal(t, sections.OperationDelete, result[1].Type())
	assert.Equal(t, 30, result[0].Priority()) // old-server first (higher priority)
	assert.Equal(t, 20, result[1].Priority()) // old-backend second

	// Next two are creates
	assert.Equal(t, sections.OperationCreate, result[2].Type())
	assert.Equal(t, sections.OperationCreate, result[3].Type())
	assert.Equal(t, 10, result[2].Priority()) // frontend first (lower priority)
	assert.Equal(t, 30, result[3].Priority()) // server second

	// Last two are updates
	assert.Equal(t, sections.OperationUpdate, result[4].Type())
	assert.Equal(t, sections.OperationUpdate, result[5].Type())
}
