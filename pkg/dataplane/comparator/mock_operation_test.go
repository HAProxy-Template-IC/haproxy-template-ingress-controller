package comparator

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// mockOperation implements Operation for testing.
type mockOperation struct {
	opType  sections.OperationType
	section string
	desc    string
}

func (m *mockOperation) Type() sections.OperationType { return m.opType }
func (m *mockOperation) Section() string              { return m.section }
func (m *mockOperation) Describe() string             { return m.desc }

// newMockOp creates a mock operation for testing.
// The priority parameter is retained for callsite compatibility but ignored
// (Operation no longer exposes Priority); kept so existing tests don't need
// updating when priority-based ordering went away.
func newMockOp(opType sections.OperationType, section string, _ int) *mockOperation {
	return &mockOperation{
		opType:  opType,
		section: section,
		desc:    section + " op",
	}
}
