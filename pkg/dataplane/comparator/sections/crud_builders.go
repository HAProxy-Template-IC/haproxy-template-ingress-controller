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

// IndexChildCRUD holds pre-built Create/Update/Delete factory functions for an index-based child resource.
type IndexChildCRUD[T any] struct {
	Create func(parentName string, index int, model T) Operation
	Update func(parentName string, index int, model T) Operation
	Delete func(parentName string, index int, model T) Operation
}

// NewIndexChildCRUD creates a CRUD builder for an index-based child resource (ACL, HTTP rules, etc.).
// The childType is used in operation descriptions (e.g. "ACL", "HTTP request rule").
func NewIndexChildCRUD[T any](
	section, childType, parentType string,
	priority int,
	createExec, updateExec, deleteExec ExecuteIndexChildFunc[T],
) IndexChildCRUD[T] {
	return IndexChildCRUD[T]{
		Create: func(parentName string, index int, model T) Operation {
			return NewIndexChildOp(
				OperationCreate, section, priority, parentName, index, model,
				Identity[T], createExec,
				DescribeIndexChild(OperationCreate, childType, index, parentType, parentName),
			)
		},
		Update: func(parentName string, index int, model T) Operation {
			return NewIndexChildOp(
				OperationUpdate, section, priority, parentName, index, model,
				Identity[T], updateExec,
				DescribeIndexChild(OperationUpdate, childType, index, parentType, parentName),
			)
		},
		Delete: func(parentName string, index int, model T) Operation {
			return NewIndexChildOp(
				OperationDelete, section, priority, parentName, index, model,
				Nil[T], deleteExec,
				DescribeIndexChild(OperationDelete, childType, index, parentType, parentName),
			)
		},
	}
}

// NameChildCRUD holds pre-built Create/Update/Delete factory functions for a name-based child resource.
type NameChildCRUD[T any] struct {
	Create func(parentName string, model T) Operation
	Update func(parentName string, model T) Operation
	Delete func(parentName string, model T) Operation
}

// NewNameChildCRUD creates a CRUD builder for a name-based child resource (bind, server template).
// The childType and parentType are used in operation descriptions.
func NewNameChildCRUD[T any](
	section, childType, parentType string,
	priority int,
	nameFn func(T) string,
	createExec, updateExec, deleteExec ExecuteNameChildFunc[T],
) NameChildCRUD[T] {
	return NameChildCRUD[T]{
		Create: func(parentName string, model T) Operation {
			return NewNameChildOp(
				OperationCreate, section, priority, parentName, nameFn(model), model,
				Identity[T], createExec,
				DescribeNamedChild(OperationCreate, childType, nameFn(model), parentType, parentName),
			)
		},
		Update: func(parentName string, model T) Operation {
			return NewNameChildOp(
				OperationUpdate, section, priority, parentName, nameFn(model), model,
				Identity[T], updateExec,
				DescribeNamedChild(OperationUpdate, childType, nameFn(model), parentType, parentName),
			)
		},
		Delete: func(parentName string, model T) Operation {
			return NewNameChildOp(
				OperationDelete, section, priority, parentName, nameFn(model), model,
				Nil[T], deleteExec,
				DescribeNamedChild(OperationDelete, childType, nameFn(model), parentType, parentName),
			)
		},
	}
}
