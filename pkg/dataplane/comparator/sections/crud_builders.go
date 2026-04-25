// Package sections provides factory functions for creating HAProxy configuration operations.
//
// This file contains generic CRUD builders that generate Create/Update/Delete operation
// factories from a single registration, eliminating repetitive boilerplate.
package sections

import (
	"context"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

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

// ContainerChildCRUD holds pre-built Create/Update/Delete factory functions for a
// container-child resource (user-in-userlist, nameserver-in-resolver, etc.).
type ContainerChildCRUD[T any] struct {
	Create func(containerName string, model T) Operation
	Update func(containerName string, model T) Operation
	Delete func(containerName string, model T) Operation
}

// containerChildExecFactory matches the literal return type of executors.UserCreate
// and friends: a function that, given a container name, yields the per-operation
// executor. Spelled out so call sites can pass the executor factories directly
// without an intermediate cast to ExecuteContainerChildFunc.
type containerChildExecFactory[T any] func(string) func(ctx context.Context, c *client.DataplaneClient, txID string, containerName string, childName string, model T) error

// NewContainerChildCRUD creates a CRUD builder for a container-child resource.
// section is the API section name (e.g. "user"), displayName is used in
// descriptions (e.g. "user"), and containerType describes the parent in
// descriptions (e.g. "userlist"). The exec factories take the container name
// at call time and return the per-operation executor.
func NewContainerChildCRUD[T any](
	section, displayName, containerType string,
	priority int,
	nameFn func(T) string,
	createExecFactory, updateExecFactory, deleteExecFactory containerChildExecFactory[T],
) ContainerChildCRUD[T] {
	return ContainerChildCRUD[T]{
		Create: func(containerName string, model T) Operation {
			return NewContainerChildOp(
				OperationCreate, section, priority, containerName, model,
				Identity[T], nameFn, createExecFactory(containerName),
				DescribeNamedChild(OperationCreate, displayName, nameFn(model), containerType, containerName),
			)
		},
		Update: func(containerName string, model T) Operation {
			return NewContainerChildOp(
				OperationUpdate, section, priority, containerName, model,
				Identity[T], nameFn, updateExecFactory(containerName),
				DescribeNamedChild(OperationUpdate, displayName, nameFn(model), containerType, containerName),
			)
		},
		Delete: func(containerName string, model T) Operation {
			return NewContainerChildOp(
				OperationDelete, section, priority, containerName, model,
				Nil[T], nameFn, deleteExecFactory(containerName),
				DescribeNamedChild(OperationDelete, displayName, nameFn(model), containerType, containerName),
			)
		},
	}
}
