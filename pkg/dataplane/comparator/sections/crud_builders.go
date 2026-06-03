// Package sections provides factory functions for creating HAProxy configuration operations.
//
// This file contains generic CRUD builders that turn a single registration into
// Create/Update/Delete operation descriptor factories. Descriptors no longer
// carry per-section execute closures — the orchestrator pushes the full
// rendered config via the dataplane raw endpoint.
package sections

import (
	"fmt"
)

// TopLevelCRUD holds pre-built Create/Update/Delete factories for a top-level resource.
type TopLevelCRUD[T any] struct {
	Create func(model T) Operation
	Update func(model T) Operation
	Delete func(model T) Operation
}

// NewTopLevelCRUD creates a CRUD builder for a top-level resource (backend, frontend, etc.).
// The displayName is used in operation descriptions and may differ from the section name.
func NewTopLevelCRUD[T any](section, displayName string, nameFn func(T) string) TopLevelCRUD[T] {
	return TopLevelCRUD[T]{
		Create: func(model T) Operation {
			return newOp(
				OperationCreate, section,
				DescribeTopLevel(OperationCreate, displayName, nameFn(model)),
			)
		},
		Update: func(model T) Operation {
			return newOp(
				OperationUpdate, section,
				DescribeTopLevel(OperationUpdate, displayName, nameFn(model)),
			)
		},
		Delete: func(model T) Operation {
			return newOp(
				OperationDelete, section,
				DescribeTopLevel(OperationDelete, displayName, nameFn(model)),
			)
		},
	}
}

// ContainerChildCRUD holds pre-built Create/Update/Delete factories for a
// container-child resource (user-in-userlist, nameserver-in-resolver, etc.).
type ContainerChildCRUD[T any] struct {
	Create func(containerName string, model T) Operation
	Update func(containerName string, model T) Operation
	Delete func(containerName string, model T) Operation
}

// NewContainerChildCRUD creates a CRUD builder for a container-child resource.
func NewContainerChildCRUD[T any](section, displayName, containerType string, nameFn func(T) string) ContainerChildCRUD[T] {
	return ContainerChildCRUD[T]{
		Create: func(containerName string, model T) Operation {
			return newOp(
				OperationCreate, section,
				DescribeNamedChild(OperationCreate, displayName, nameFn(model), containerType, containerName),
			)
		},
		Update: func(containerName string, model T) Operation {
			return newOp(
				OperationUpdate, section,
				DescribeNamedChild(OperationUpdate, displayName, nameFn(model), containerType, containerName),
			)
		},
		Delete: func(containerName string, model T) Operation {
			return newOp(
				OperationDelete, section,
				DescribeNamedChild(OperationDelete, displayName, nameFn(model), containerType, containerName),
			)
		},
	}
}

// IndexChildCRUD holds pre-built Create/Update/Delete factories for an
// index-based child resource (ACL, HTTP/TCP rules, checks, captures, etc.).
type IndexChildCRUD[T any] struct {
	Create func(parentName string, model T, index int) Operation
	Update func(parentName string, model T, index int) Operation
	Delete func(parentName string, model T, index int) Operation
}

// NewIndexChildCRUD creates a CRUD builder for an index-based child resource.
func NewIndexChildCRUD[T any](section, displayName, parentType string, identifierFn func(T) string) IndexChildCRUD[T] {
	return NewIndexChildCRUDWithDescriber[T](
		section,
		func(opType OperationType, model T, parentName string, index int) func() string {
			return DescribeTypedChild(opType, displayName, identifierFn(model),
				fmt.Sprintf("at index %d", index), parentType, parentName)
		},
	)
}

// NewIndexChildCRUDWithDescriber is the general form of NewIndexChildCRUD where
// each operation's description is built by the caller-supplied describer.
func NewIndexChildCRUDWithDescriber[T any](
	section string,
	describer func(opType OperationType, model T, parentName string, index int) func() string,
) IndexChildCRUD[T] {
	return IndexChildCRUD[T]{
		Create: func(parentName string, model T, index int) Operation {
			return newOp(OperationCreate, section, describer(OperationCreate, model, parentName, index))
		},
		Update: func(parentName string, model T, index int) Operation {
			return newOp(OperationUpdate, section, describer(OperationUpdate, model, parentName, index))
		},
		Delete: func(parentName string, model T, index int) Operation {
			return newOp(OperationDelete, section, describer(OperationDelete, model, parentName, index))
		},
	}
}

// NameChildCRUD holds pre-built Create/Update/Delete factories for a
// name-based child resource (server, server_template, bind, etc.).
type NameChildCRUD[T any] struct {
	Create func(parentName, childName string, model T) Operation
	Update func(parentName, childName string, model T) Operation
	Delete func(parentName, childName string, model T) Operation
}

// NewNameChildCRUD creates a CRUD builder for a name-based child resource.
func NewNameChildCRUD[T any](section, displayType, parentType string, descNameFn func(model T, childName string) string) NameChildCRUD[T] {
	describe := func(opType OperationType, model T, parentName, childName string) func() string {
		return DescribeNamedChild(opType, displayType, descNameFn(model, childName), parentType, parentName)
	}
	return NameChildCRUD[T]{
		Create: func(parentName, childName string, model T) Operation {
			return newOp(OperationCreate, section, describe(OperationCreate, model, parentName, childName))
		},
		Update: func(parentName, childName string, model T) Operation {
			return newOp(OperationUpdate, section, describe(OperationUpdate, model, parentName, childName))
		},
		Delete: func(parentName, childName string, model T) Operation {
			return newOp(OperationDelete, section, describe(OperationDelete, model, parentName, childName))
		},
	}
}
