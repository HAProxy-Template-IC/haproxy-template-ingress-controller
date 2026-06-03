// Package sections provides generic operation types for HAProxy configuration management.
//
// Operations are descriptors of a single diff entry — what kind of change, which
// section, a human-readable string. They are produced by the comparator, consumed
// by the orchestrator to decide push strategy (raw-push always; runtime actions
// for server field updates), and inspected by metrics/logging. They no longer
// carry an Execute method — the orchestrator pushes the full rendered config via
// the dataplane API's raw endpoint and never calls per-section API endpoints.
package sections

// OperationType represents the type of HAProxy configuration operation.
type OperationType int

const (
	OperationCreate OperationType = iota
	OperationUpdate
	OperationDelete
)

// genericOp is the descriptor used by every section operation except the
// data-carrying ServerUpdateOp: the kind of change, the section it touches, and
// a lazily-computed human-readable string. There is deliberately only one such
// type — the earlier per-shape structs (top-level, index-child, name-child,
// singleton, container-child) embedded nothing but these fields and carried a
// phantom type parameter that no code ever read, so they collapsed to one.
// Section-specific behaviour lives in the factory closures (see crud_builders.go
// and factory_*.go), not in the operation type.
type genericOp struct {
	opType      OperationType
	sectionName string
	describeFn  func() string
}

// newOp builds a generic operation descriptor.
func newOp(opType OperationType, sectionName string, describeFn func() string) *genericOp {
	return &genericOp{
		opType:      opType,
		sectionName: sectionName,
		describeFn:  describeFn,
	}
}

func (o *genericOp) Type() OperationType { return o.opType }
func (o *genericOp) Section() string     { return o.sectionName }
func (o *genericOp) Describe() string    { return o.describeFn() }
