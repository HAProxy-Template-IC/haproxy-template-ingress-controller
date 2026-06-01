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

// opBase carries the fields and trivial accessor methods shared by every
// generic operation type. Embedding it removes ~30 lines of identical struct
// fields, getter methods, and constructor assignments per operation type.
type opBase struct {
	opType      OperationType
	sectionName string
	describeFn  func() string
}

func (b *opBase) Type() OperationType { return b.opType }
func (b *opBase) Section() string     { return b.sectionName }
func (b *opBase) Describe() string    { return b.describeFn() }

// TopLevelOp describes operations for top-level named resources like backend,
// frontend, defaults.
type TopLevelOp[TModel any] struct {
	opBase
}

// NewTopLevelOp creates a new top-level operation descriptor.
func NewTopLevelOp[TModel any](
	opType OperationType,
	sectionName string,
	describeFn func() string,
) *TopLevelOp[TModel] {
	return &TopLevelOp[TModel]{
		opBase: opBase{
			opType:      opType,
			sectionName: sectionName,
			describeFn:  describeFn,
		},
	}
}

// IndexChildOp describes operations for index-based child resources like ACL,
// HTTP rules, TCP rules.
type IndexChildOp[TModel any] struct {
	opBase
}

// NewIndexChildOp creates a new index-based child operation descriptor.
func NewIndexChildOp[TModel any](
	opType OperationType,
	sectionName string,
	describeFn func() string,
) *IndexChildOp[TModel] {
	return &IndexChildOp[TModel]{
		opBase: opBase{
			opType:      opType,
			sectionName: sectionName,
			describeFn:  describeFn,
		},
	}
}

// NameChildOp describes operations for name-based child resources like bind,
// server_template.
type NameChildOp[TModel any] struct {
	opBase
}

// NewNameChildOp creates a new name-based child operation descriptor.
func NewNameChildOp[TModel any](
	opType OperationType,
	sectionName string,
	describeFn func() string,
) *NameChildOp[TModel] {
	return &NameChildOp[TModel]{
		opBase: opBase{
			opType:      opType,
			sectionName: sectionName,
			describeFn:  describeFn,
		},
	}
}

// SingletonOp describes operations for singleton sections like global.
type SingletonOp[TModel any] struct {
	opBase
}

// NewSingletonOp creates a new singleton operation descriptor.
func NewSingletonOp[TModel any](
	opType OperationType,
	sectionName string,
	describeFn func() string,
) *SingletonOp[TModel] {
	return &SingletonOp[TModel]{
		opBase: opBase{
			opType:      opType,
			sectionName: sectionName,
			describeFn:  describeFn,
		},
	}
}

// ContainerChildOp describes operations for container child resources like
// user, mailer_entry.
type ContainerChildOp[TModel any] struct {
	opBase
}

// NewContainerChildOp creates a new container child operation descriptor.
func NewContainerChildOp[TModel any](
	opType OperationType,
	sectionName string,
	describeFn func() string,
) *ContainerChildOp[TModel] {
	return &ContainerChildOp[TModel]{
		opBase: opBase{
			opType:      opType,
			sectionName: sectionName,
			describeFn:  describeFn,
		},
	}
}
