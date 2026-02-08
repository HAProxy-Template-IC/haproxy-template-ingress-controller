package comparator

import (
	"context"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// mockOperation implements Operation for testing and benchmarking.
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

// newMockOp creates a mock operation for testing.
func newMockOp(opType sections.OperationType, section string, priority int) *mockOperation {
	return &mockOperation{
		opType:   opType,
		section:  section,
		priority: priority,
		desc:     section + " op",
	}
}

// generateMockOperations creates a slice of mock operations for benchmarking.
func generateMockOperations(count int) []Operation {
	ops := make([]Operation, count)
	types := []sections.OperationType{sections.OperationCreate, sections.OperationUpdate, sections.OperationDelete}

	for i := 0; i < count; i++ {
		ops[i] = &mockOperation{
			opType:   types[i%3],
			section:  fmt.Sprintf("section_%d", i%10),
			priority: i % 100,
			desc:     "mock operation",
		}
	}
	return ops
}
