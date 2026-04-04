// Package sections provides factory functions for creating HAProxy configuration operations.
//
// This file contains generic CRUD builders that generate Create/Update/Delete operation
// factories from a single registration, eliminating repetitive boilerplate.
package sections

// TopLevelCRUD holds pre-built Create/Update/Delete factory functions for a top-level resource.
type TopLevelCRUD[T any] struct {
	Create func(model T) Operation
	Update func(model T) Operation
	Delete func(model T) Operation
}

// NewTopLevelCRUD creates a CRUD builder for a top-level resource (backend, frontend, etc.).
// The displayName is used in operation descriptions and may differ from the section name.
func NewTopLevelCRUD[T any](
	section, displayName string,
	priority int,
	nameFn func(T) string,
	createExec, updateExec, deleteExec ExecuteTopLevelFunc[T],
) TopLevelCRUD[T] {
	return TopLevelCRUD[T]{
		Create: func(model T) Operation {
			return NewTopLevelOp(
				OperationCreate, section, priority, model,
				Identity[T], nameFn, createExec,
				DescribeTopLevel(OperationCreate, displayName, nameFn(model)),
			)
		},
		Update: func(model T) Operation {
			return NewTopLevelOp(
				OperationUpdate, section, priority, model,
				Identity[T], nameFn, updateExec,
				DescribeTopLevel(OperationUpdate, displayName, nameFn(model)),
			)
		},
		Delete: func(model T) Operation {
			return NewTopLevelOp(
				OperationDelete, section, priority, model,
				Nil[T], nameFn, deleteExec,
				DescribeTopLevel(OperationDelete, displayName, nameFn(model)),
			)
		},
	}
}
