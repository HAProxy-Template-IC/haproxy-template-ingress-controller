// Package sections provides generic operation types for HAProxy configuration management.
//
// This file contains type-safe generic operation implementations that replace
// the repetitive per-section operation struct definitions. Each generic type
// handles a specific "shape" of API operation pattern.
package sections

import (
	"context"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// OperationType represents the type of HAProxy configuration operation.
type OperationType int

const (
	OperationCreate OperationType = iota
	OperationUpdate
	OperationDelete
)

// PriorityMultiplier is used to create sub-priority space for index-based operations.
// All operation types multiply their base priority by this value to create effective priorities.
// For IndexChildOp, the index is added (for creates) or subtracted from 999 (for deletes)
// to ensure correct ordering within the same base priority level.
const PriorityMultiplier = 1000

// Priority constants for operation ordering (base priorities, before multiplier).
// Lower priority = executed first for Creates, executed last for Deletes.
// Higher priority = executed last for Creates, executed first for Deletes.
// Note: Effective priority = BasePriority * PriorityMultiplier (+ index adjustment for IndexChildOp).
const (
	// Priority 10 - Top-level sections that must exist first.
	PriorityGlobal     = 10
	PriorityDefaults   = 20
	PriorityUserlist   = 10
	PriorityCrtStore   = 10
	PriorityLogForward = 10
	PriorityFCGIApp    = 10
	PriorityProgram    = 10

	// Priority 15 - Container sections.
	PriorityPeer     = 15
	PriorityRing     = 15
	PriorityMailers  = 15
	PriorityUser     = 15
	PriorityCache    = 15
	PriorityResolver = 15

	// Priority 15 - Observability sections (v3.1+ features).
	PriorityLogProfile = 15
	PriorityTraces     = 15

	// Priority 15 - Certificate automation sections (v3.2+ features).
	PriorityAcmeProvider = 15

	// Priority 20-25 - HTTP errors and other mid-level.
	PriorityHTTPErrors = 25

	// Priority 30 - Frontend/Backend sections.
	PriorityFrontend = 30
	PriorityBackend  = 30

	// Priority 40 - Direct children of frontends/backends.
	PriorityBind        = 40
	PriorityServer      = 40
	PriorityMailerEntry = 40
	PriorityPeerEntry   = 40
	PriorityNameserver  = 40

	// Priority 50 - ACLs.
	PriorityACL = 50

	// Priority 60 - Rules (depend on ACLs).
	PriorityRule                 = 60
	PriorityCapture              = 60
	PriorityStickRule            = 60
	PriorityHTTPAfterRule        = 60
	PriorityServerSwitchingRule  = 60
	PriorityBackendSwitchingRule = 60
	PriorityHTTPCheck            = 60
	PriorityLogTarget            = 60
	PriorityTCPCheck             = 60
	PriorityFilter               = 60
	PriorityQUICInitialRule      = 60 // v3.1+ only
)

// ExecuteTopLevelFunc is the function signature for top-level resource operations.
// Used by backend, frontend, defaults, cache, etc.
type ExecuteTopLevelFunc[TAPI any] func(
	ctx context.Context,
	c *client.DataplaneClient,
	txID string,
	model TAPI,
	name string,
) error

// ExecuteIndexChildFunc is the function signature for index-based child operations.
// Used by ACL, HTTP rules, TCP rules, filters, etc.
type ExecuteIndexChildFunc[TAPI any] func(
	ctx context.Context,
	c *client.DataplaneClient,
	txID string,
	parent string,
	index int,
	model TAPI,
) error

// ExecuteNameChildFunc is the function signature for name-based child operations.
// Used by bind, server_template.
type ExecuteNameChildFunc[TAPI any] func(
	ctx context.Context,
	c *client.DataplaneClient,
	txID string,
	parent string,
	childName string,
	model TAPI,
) error

// ExecuteSingletonFunc is the function signature for singleton operations.
// Used by global section which only supports update.
type ExecuteSingletonFunc[TAPI any] func(
	ctx context.Context,
	c *client.DataplaneClient,
	txID string,
	model TAPI,
) error

// ExecuteContainerChildFunc is the function signature for container child operations.
// Used by user, mailer_entry, peer_entry, nameserver where parent is in params.
type ExecuteContainerChildFunc[TAPI any] func(
	ctx context.Context,
	c *client.DataplaneClient,
	txID string,
	containerName string,
	childName string,
	model TAPI,
) error

// TopLevelOp handles operations for top-level named resources like backend, frontend, defaults.
// These resources are identified by a single name and use DispatchCreate/Update/Delete.
type TopLevelOp[TModel any, TAPI any] struct {
	opType      OperationType
	sectionName string
	priorityVal int
	model       TModel
	transformFn func(TModel) TAPI
	nameFn      func(TModel) string
	executeFn   ExecuteTopLevelFunc[TAPI]
	describeFn  func() string
}

// NewTopLevelOp creates a new top-level operation.
func NewTopLevelOp[TModel any, TAPI any](
	opType OperationType,
	sectionName string,
	priority int,
	model TModel,
	transformFn func(TModel) TAPI,
	nameFn func(TModel) string,
	executeFn ExecuteTopLevelFunc[TAPI],
	describeFn func() string,
) *TopLevelOp[TModel, TAPI] {
	return &TopLevelOp[TModel, TAPI]{
		opType:      opType,
		sectionName: sectionName,
		priorityVal: priority,
		model:       model,
		transformFn: transformFn,
		nameFn:      nameFn,
		executeFn:   executeFn,
		describeFn:  describeFn,
	}
}

func (op *TopLevelOp[TModel, TAPI]) Type() OperationType { return op.opType }
func (op *TopLevelOp[TModel, TAPI]) Section() string     { return op.sectionName }

// Priority returns the effective priority. Uses multiplier for compatibility with IndexChildOp.
func (op *TopLevelOp[TModel, TAPI]) Priority() int    { return op.priorityVal * 1000 }
func (op *TopLevelOp[TModel, TAPI]) Describe() string { return op.describeFn() }

func (op *TopLevelOp[TModel, TAPI]) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	name := op.nameFn(op.model)

	// For delete operations, we may not need to transform
	if op.opType == OperationDelete {
		var zero TAPI
		return op.executeFn(ctx, c, txID, zero, name)
	}

	// Transform model for create/update
	apiModel := op.transformFn(op.model)
	var zero TAPI
	if any(apiModel) == any(zero) {
		return fmt.Errorf("failed to transform %s", op.sectionName)
	}

	return op.executeFn(ctx, c, txID, apiModel, name)
}

// IndexChildOp handles operations for index-based child resources like ACL, HTTP rules, TCP rules.
// These resources belong to a parent (frontend/backend) and are identified by index position.
type IndexChildOp[TModel any, TAPI any] struct {
	opType      OperationType
	sectionName string
	priorityVal int
	parentName  string
	index       int
	model       TModel
	transformFn func(TModel) TAPI
	executeFn   ExecuteIndexChildFunc[TAPI]
	describeFn  func() string
}

// NewIndexChildOp creates a new index-based child operation.
func NewIndexChildOp[TModel any, TAPI any](
	opType OperationType,
	sectionName string,
	priority int,
	parentName string,
	index int,
	model TModel,
	transformFn func(TModel) TAPI,
	executeFn ExecuteIndexChildFunc[TAPI],
	describeFn func() string,
) *IndexChildOp[TModel, TAPI] {
	return &IndexChildOp[TModel, TAPI]{
		opType:      opType,
		sectionName: sectionName,
		priorityVal: priority,
		parentName:  parentName,
		index:       index,
		model:       model,
		transformFn: transformFn,
		executeFn:   executeFn,
		describeFn:  describeFn,
	}
}

func (op *IndexChildOp[TModel, TAPI]) Type() OperationType { return op.opType }
func (op *IndexChildOp[TModel, TAPI]) Section() string     { return op.sectionName }

// Priority returns the effective priority incorporating the index for correct ordering.
// For creates: lower indexes run first (index 0 before index 1).
// For deletes: higher indexes run first (index 1 before index 0).
// This ensures index-based operations execute in the correct order even with parallel execution.
func (op *IndexChildOp[TModel, TAPI]) Priority() int {
	// Use multiplier to create sub-priority space within each base priority level.
	// Max 1000 indexes per parent should be sufficient.
	basePriority := op.priorityVal * 1000
	if op.opType == OperationDelete {
		// Deletes: higher index = lower effective priority (runs first)
		return basePriority + (999 - op.index)
	}
	// Creates/Updates: lower index = lower effective priority (runs first)
	return basePriority + op.index
}
func (op *IndexChildOp[TModel, TAPI]) Describe() string { return op.describeFn() }

func (op *IndexChildOp[TModel, TAPI]) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	// For delete operations, we don't need to transform
	if op.opType == OperationDelete {
		var zero TAPI
		return op.executeFn(ctx, c, txID, op.parentName, op.index, zero)
	}

	// Transform model for create/update
	apiModel := op.transformFn(op.model)
	var zero TAPI
	if any(apiModel) == any(zero) {
		return fmt.Errorf("failed to transform %s", op.sectionName)
	}

	return op.executeFn(ctx, c, txID, op.parentName, op.index, apiModel)
}

// NameChildOp handles operations for name-based child resources like bind, server_template.
// These resources belong to a parent and are identified by name (not index).
type NameChildOp[TModel any, TAPI any] struct {
	opType      OperationType
	sectionName string
	priorityVal int
	parentName  string
	childName   string
	model       TModel
	transformFn func(TModel) TAPI
	executeFn   ExecuteNameChildFunc[TAPI]
	describeFn  func() string
}

// NewNameChildOp creates a new name-based child operation.
func NewNameChildOp[TModel any, TAPI any](
	opType OperationType,
	sectionName string,
	priority int,
	parentName string,
	childName string,
	model TModel,
	transformFn func(TModel) TAPI,
	executeFn ExecuteNameChildFunc[TAPI],
	describeFn func() string,
) *NameChildOp[TModel, TAPI] {
	return &NameChildOp[TModel, TAPI]{
		opType:      opType,
		sectionName: sectionName,
		priorityVal: priority,
		parentName:  parentName,
		childName:   childName,
		model:       model,
		transformFn: transformFn,
		executeFn:   executeFn,
		describeFn:  describeFn,
	}
}

func (op *NameChildOp[TModel, TAPI]) Type() OperationType { return op.opType }
func (op *NameChildOp[TModel, TAPI]) Section() string     { return op.sectionName }

// Priority returns the effective priority. Uses multiplier for compatibility with IndexChildOp.
func (op *NameChildOp[TModel, TAPI]) Priority() int    { return op.priorityVal * 1000 }
func (op *NameChildOp[TModel, TAPI]) Describe() string { return op.describeFn() }

func (op *NameChildOp[TModel, TAPI]) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	// For delete operations, we don't need to transform
	if op.opType == OperationDelete {
		var zero TAPI
		return op.executeFn(ctx, c, txID, op.parentName, op.childName, zero)
	}

	// Transform model for create/update
	apiModel := op.transformFn(op.model)
	var zero TAPI
	if any(apiModel) == any(zero) {
		return fmt.Errorf("failed to transform %s", op.sectionName)
	}

	return op.executeFn(ctx, c, txID, op.parentName, op.childName, apiModel)
}

// SingletonOp handles operations for singleton sections like global, traces, or waf-global.
// Supports create, update, and delete operations for singleton resources.
type SingletonOp[TModel any, TAPI any] struct {
	opType      OperationType
	sectionName string
	priorityVal int
	model       TModel
	transformFn func(TModel) TAPI
	executeFn   ExecuteSingletonFunc[TAPI]
	describeFn  func() string
}

// NewSingletonOp creates a new singleton operation with the specified operation type.
func NewSingletonOp[TModel any, TAPI any](
	opType OperationType,
	sectionName string,
	priority int,
	model TModel,
	transformFn func(TModel) TAPI,
	executeFn ExecuteSingletonFunc[TAPI],
	describeFn func() string,
) *SingletonOp[TModel, TAPI] {
	return &SingletonOp[TModel, TAPI]{
		opType:      opType,
		sectionName: sectionName,
		priorityVal: priority,
		model:       model,
		transformFn: transformFn,
		executeFn:   executeFn,
		describeFn:  describeFn,
	}
}

func (op *SingletonOp[TModel, TAPI]) Type() OperationType { return op.opType }
func (op *SingletonOp[TModel, TAPI]) Section() string     { return op.sectionName }

// Priority returns the effective priority. Uses multiplier for compatibility with IndexChildOp.
func (op *SingletonOp[TModel, TAPI]) Priority() int    { return op.priorityVal * 1000 }
func (op *SingletonOp[TModel, TAPI]) Describe() string { return op.describeFn() }

func (op *SingletonOp[TModel, TAPI]) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	// For delete operations, we don't need to transform
	if op.opType == OperationDelete {
		var zero TAPI
		return op.executeFn(ctx, c, txID, zero)
	}

	apiModel := op.transformFn(op.model)
	var zero TAPI
	if any(apiModel) == any(zero) {
		return fmt.Errorf("failed to transform %s", op.sectionName)
	}

	return op.executeFn(ctx, c, txID, apiModel)
}

// ContainerChildOp handles operations for container child resources like user, mailer_entry.
// These resources belong to a container (userlist, mailers) where the parent is passed via params.
type ContainerChildOp[TModel any, TAPI any] struct {
	opType        OperationType
	sectionName   string
	priorityVal   int
	containerName string
	model         TModel
	transformFn   func(TModel) TAPI
	nameFn        func(TModel) string
	executeFn     ExecuteContainerChildFunc[TAPI]
	describeFn    func() string
}

// NewContainerChildOp creates a new container child operation.
func NewContainerChildOp[TModel any, TAPI any](
	opType OperationType,
	sectionName string,
	priority int,
	containerName string,
	model TModel,
	transformFn func(TModel) TAPI,
	nameFn func(TModel) string,
	executeFn ExecuteContainerChildFunc[TAPI],
	describeFn func() string,
) *ContainerChildOp[TModel, TAPI] {
	return &ContainerChildOp[TModel, TAPI]{
		opType:        opType,
		sectionName:   sectionName,
		priorityVal:   priority,
		containerName: containerName,
		model:         model,
		transformFn:   transformFn,
		nameFn:        nameFn,
		executeFn:     executeFn,
		describeFn:    describeFn,
	}
}

func (op *ContainerChildOp[TModel, TAPI]) Type() OperationType { return op.opType }
func (op *ContainerChildOp[TModel, TAPI]) Section() string     { return op.sectionName }

// Priority returns the effective priority. Uses multiplier for compatibility with IndexChildOp.
func (op *ContainerChildOp[TModel, TAPI]) Priority() int    { return op.priorityVal * 1000 }
func (op *ContainerChildOp[TModel, TAPI]) Describe() string { return op.describeFn() }

func (op *ContainerChildOp[TModel, TAPI]) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	childName := op.nameFn(op.model)

	// For delete operations, we don't need to transform
	if op.opType == OperationDelete {
		var zero TAPI
		return op.executeFn(ctx, c, txID, op.containerName, childName, zero)
	}

	// Transform model for create/update
	apiModel := op.transformFn(op.model)
	var zero TAPI
	if any(apiModel) == any(zero) {
		return fmt.Errorf("failed to transform %s", op.sectionName)
	}

	return op.executeFn(ctx, c, txID, op.containerName, childName, apiModel)
}
