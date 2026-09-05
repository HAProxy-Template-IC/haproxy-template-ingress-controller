// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"
)

const incrementalComponentContextCertificateLimit = 8

const (
	incrementalComponentContextEmpty uint32 = iota
	incrementalComponentContextPreparing
	incrementalComponentContextPrepared
	incrementalComponentContextSealing
	incrementalComponentContextSealed
	incrementalComponentContextFailed
)

// IncrementalComponentContextOptions supplies one component's native capabilities.
type IncrementalComponentContextOptions struct {
	ExecutionLease  IncrementalExecutionLease
	ResourceDeriver IncrementalResourceDeriver
	EventRecorder   IncrementalEventRecorder
	StatusRecorder  IncrementalStatusPatchRecorder
	TransitionTime  string
	ContextValueKey any
	ContextValue    any
}

// IncrementalComponentContextValues supplies one component's vector bindings.
type IncrementalComponentContextValues struct {
	Source        string
	Item          map[string]any
	Props         map[string]any
	RenderSubject map[string]any
	RenderMode    string
	Resources     any
	Controller    map[string]ResourceStore
	Shared        SharedContributionContext
	HTTP          HTTPFetcher
	PlanRegistry  IncrementalBackendPlanRegistrar
}

// IncrementalComponentContextTable owns compact contexts for one immutable vector lane.
type IncrementalComponentContextTable struct {
	seal  *IncrementalComponentContextTable
	items []incrementalComponentExecutionContext
}

type incrementalComponentExecutionContext struct {
	seal  *incrementalComponentExecutionContext
	table *IncrementalComponentContextTable
	index int
	state atomic.Uint32

	parent          context.Context
	storage         *immutableStorage
	localStorage    immutableStorage
	templateContext map[string]any
	values          IncrementalComponentContextValues
	binding         incrementalImmutableBinding
	valuesOnce      sync.Once
	compactValues   bool
	executionLease  IncrementalExecutionLease
	resourceDeriver IncrementalResourceDeriver
	eventRecorder   IncrementalEventRecorder
	statusRecorder  IncrementalStatusPatchRecorder
	transitionTime  string
	contextValueKey any
	contextValue    any
	vectorContext   atomic.Pointer[incrementalVectorContextSeal]
	certificates    [incrementalComponentContextCertificateLimit]*IncrementalImmutableCertificate
	certificateLen  int
}

type unavailableIncrementalComponentExecutionLease struct{}

func (unavailableIncrementalComponentExecutionLease) BeginIncrementalExecution(
	context.Context,
	string,
) (func(), error) {
	return nil, errors.New("incremental component context is not sealed")
}

func (unavailableIncrementalComponentExecutionLease) BeforeIncrementalNativeCall(context.Context) error {
	return errors.New("incremental component context is not sealed")
}

var unavailableIncrementalExecutionLease unavailableIncrementalComponentExecutionLease

// NewIncrementalComponentContextTable allocates one contiguous context table.
func NewIncrementalComponentContextTable(count int) (*IncrementalComponentContextTable, error) {
	if count <= 0 {
		return nil, errors.New("incremental component context count must be positive")
	}
	table := &IncrementalComponentContextTable{items: make([]incrementalComponentExecutionContext, count)}
	table.seal = table
	for index := range table.items {
		item := &table.items[index]
		item.seal = item
		item.table = table
		item.index = index
	}
	return table, nil
}

// Prepare binds construction-only inputs and returns the context for one item.
func (t *IncrementalComponentContextTable) Prepare(
	index int,
	parent context.Context,
	options IncrementalComponentContextOptions,
	certificates ...*IncrementalImmutableCertificate,
) (context.Context, error) {
	item, err := t.item(index)
	if err != nil {
		return nil, err
	}
	if !item.state.CompareAndSwap(incrementalComponentContextEmpty, incrementalComponentContextPreparing) {
		return nil, fmt.Errorf("incremental component context item %d was already prepared", index)
	}
	fail := func(err error) (context.Context, error) {
		item.state.Store(incrementalComponentContextFailed)
		return nil, err
	}
	if parent == nil {
		return fail(errors.New("incremental component context parent is nil"))
	}
	if isNilValue(options.ExecutionLease) {
		return fail(errors.New("incremental component context execution lease is nil"))
	}
	if len(certificates) == 0 || len(certificates) > len(item.certificates) {
		return fail(fmt.Errorf(
			"incremental component context has %d certificates, want 1-%d",
			len(certificates),
			len(item.certificates),
		))
	}
	for certificateIndex, certificate := range certificates {
		if certificate == nil {
			return fail(fmt.Errorf("incremental component context certificate %d is nil", certificateIndex))
		}
		item.certificates[certificateIndex] = certificate
	}
	if isNilValue(options.ResourceDeriver) {
		options.ResourceDeriver = nil
	}
	if isNilValue(options.EventRecorder) {
		options.EventRecorder = nil
	}
	if isNilValue(options.StatusRecorder) {
		options.StatusRecorder = nil
	}
	if options.StatusRecorder == nil && options.TransitionTime != "" {
		return fail(errors.New("incremental component transition time has no status recorder"))
	}
	if options.StatusRecorder != nil && options.TransitionTime == "" {
		return fail(errors.New("incremental component status recorder has no transition time"))
	}
	if (options.ContextValueKey == nil) != (options.ContextValue == nil) {
		return fail(errors.New("incremental component context value is incomplete"))
	}
	if options.ContextValueKey != nil && !reflect.TypeOf(options.ContextValueKey).Comparable() {
		return fail(errors.New("incremental component context value key is not comparable"))
	}
	item.parent = parent
	item.executionLease = options.ExecutionLease
	item.resourceDeriver = options.ResourceDeriver
	item.eventRecorder = options.EventRecorder
	item.statusRecorder = options.StatusRecorder
	item.transitionTime = options.TransitionTime
	item.contextValueKey = options.ContextValueKey
	item.contextValue = options.ContextValue
	item.certificateLen = len(certificates)
	item.state.Store(incrementalComponentContextPrepared)
	return item, nil
}

// Seal authenticates the render bindings and publishes one context atomically.
func (t *IncrementalComponentContextTable) Seal(
	index int,
	templateContext map[string]any,
	capabilities ...any,
) error {
	item, err := t.item(index)
	if err != nil {
		return err
	}
	if !item.state.CompareAndSwap(incrementalComponentContextPrepared, incrementalComponentContextSealing) {
		return fmt.Errorf("incremental component context item %d is not prepared", index)
	}
	storage := &item.localStorage
	storage.addCertificates(item.certificates[:item.certificateLen]...)
	storage.addCapabilities(capabilities...)
	if err := bindIncrementalImmutableInputs(templateContext, storage); err != nil {
		item.state.Store(incrementalComponentContextFailed)
		return err
	}
	item.storage = storage
	item.templateContext = templateContext
	item.certificates = [incrementalComponentContextCertificateLimit]*IncrementalImmutableCertificate{}
	item.certificateLen = 0
	item.state.Store(incrementalComponentContextSealed)
	return nil
}

// SealValues authenticates vector bindings without materializing a context map.
func (t *IncrementalComponentContextTable) SealValues(
	index int,
	values IncrementalComponentContextValues,
) error {
	item, err := t.item(index)
	if err != nil {
		return err
	}
	if !item.state.CompareAndSwap(incrementalComponentContextPrepared, incrementalComponentContextSealing) {
		return fmt.Errorf("incremental component context item %d is not prepared", index)
	}
	fail := func(err error) error {
		item.state.Store(incrementalComponentContextFailed)
		return err
	}
	storage := &item.localStorage
	storage.addCertificates(item.certificates[:item.certificateLen]...)
	storage.addBorrowedCapabilities(values.Controller, values.Shared, values.HTTP, values.PlanRegistry)
	binding, err := newIncrementalImmutableValueBinding(storage, &values)
	if err != nil {
		return fail(err)
	}
	item.storage = storage
	item.values = values
	item.binding = binding
	item.binding.seal = &item.binding
	item.compactValues = true
	item.certificates = [incrementalComponentContextCertificateLimit]*IncrementalImmutableCertificate{}
	item.certificateLen = 0
	item.state.Store(incrementalComponentContextSealed)
	return nil
}

func (t *IncrementalComponentContextTable) item(index int) (*incrementalComponentExecutionContext, error) {
	if t == nil || t.seal != t || index < 0 || index >= len(t.items) {
		return nil, fmt.Errorf("incremental component context item %d is invalid", index)
	}
	item := &t.items[index]
	if item.seal != item || item.table != t || item.index != index {
		return nil, fmt.Errorf("incremental component context item %d has invalid provenance", index)
	}
	return item, nil
}

func (c *incrementalComponentExecutionContext) valid() bool {
	return c != nil && c.seal == c && c.table != nil && c.table.seal == c.table &&
		c.index >= 0 && c.index < len(c.table.items) && &c.table.items[c.index] == c
}

func (c *incrementalComponentExecutionContext) parentContext() context.Context {
	if !c.valid() || c.parent == nil {
		return context.Background()
	}
	return c.parent
}

func (c *incrementalComponentExecutionContext) Deadline() (time.Time, bool) {
	return c.parentContext().Deadline()
}

func (c *incrementalComponentExecutionContext) Done() <-chan struct{} {
	return c.parentContext().Done()
}

func (c *incrementalComponentExecutionContext) Err() error {
	return c.parentContext().Err()
}

func (c *incrementalComponentExecutionContext) unsealedValue(key any) any {
	switch key.(type) {
	case incrementalExecutionLeaseContextKey:
		return unavailableIncrementalExecutionLease
	case renderContextKey,
		immutableStorageContextKey,
		incrementalResourceDeriverContextKey,
		incrementalEventRecorderContextKey,
		incrementalStatusPatchRecorderContextKey,
		incrementalTransitionTimeContextKey:
		return nil
	default:
		return c.parentContext().Value(key)
	}
}

func (c *incrementalComponentExecutionContext) Value(key any) any {
	if !c.valid() {
		return c.parentContext().Value(key)
	}
	if c.state.Load() != incrementalComponentContextSealed {
		return c.unsealedValue(key)
	}
	if c.contextValueKey != nil && key == c.contextValueKey {
		return c.contextValue
	}
	switch key.(type) {
	case renderContextKey:
		if c.compactValues {
			return c.compatibilityValues()
		}
		return c.templateContext
	case incrementalRenderContextValuesKey:
		if c.compactValues {
			return c
		}
		return c.parentContext().Value(key)
	case incrementalVectorContextKey:
		return c.vectorContext.Load()
	case immutableStorageContextKey:
		return c.storage
	case incrementalExecutionLeaseContextKey:
		return c.executionLease
	case incrementalResourceDeriverContextKey:
		return c.resourceDeriver
	case incrementalEventRecorderContextKey:
		return c.eventRecorder
	case incrementalStatusPatchRecorderContextKey:
		return c.statusRecorder
	case incrementalTransitionTimeContextKey:
		if c.transitionTime != "" {
			return c.transitionTime
		}
		return nil
	default:
		return c.parentContext().Value(key)
	}
}

func (c *incrementalComponentExecutionContext) compatibilityValues() map[string]any {
	c.valuesOnce.Do(func() {
		c.templateContext = map[string]any{
			declSource:        c.values.Source,
			declItem:          c.values.Item,
			declProps:         c.values.Props,
			declRenderSubject: c.values.RenderSubject,
			declRenderMode:    c.values.RenderMode,
			declResources:     c.values.Resources,
			declController:    c.values.Controller,
			declShared:        c.values.Shared,
			declHTTP:          c.values.HTTP,
			declPlanRegistry:  c.values.PlanRegistry,
			incrementalImmutableBindingTemplateContextKey: &c.binding,
		}
	})
	return c.templateContext
}

func (c *incrementalComponentExecutionContext) incrementalRenderContextValue(name string) (any, bool) {
	if !c.valid() || c.state.Load() != incrementalComponentContextSealed || !c.compactValues {
		return nil, false
	}
	switch name {
	case declSource:
		return c.values.Source, true
	case declItem:
		return c.values.Item, true
	case declProps:
		return c.values.Props, true
	case declRenderSubject:
		return c.values.RenderSubject, true
	case declRenderMode:
		return c.values.RenderMode, true
	case declResources:
		return c.values.Resources, true
	case declController:
		return c.values.Controller, true
	case declShared:
		return c.values.Shared, true
	case declHTTP:
		return c.values.HTTP, true
	case declPlanRegistry:
		return c.values.PlanRegistry, true
	case ResourceDeriverContextName:
		if c.resourceDeriver != nil {
			return c.resourceDeriver, true
		}
	}
	return nil, false
}

func (c *incrementalComponentExecutionContext) bindVectorContext(
	seal *incrementalVectorContextSeal,
) error {
	if !c.valid() || c.state.Load() != incrementalComponentContextSealed ||
		seal == nil || seal.seal != seal || seal.index < 0 {
		return errors.New("incremental component vector context has invalid provenance")
	}
	if !c.vectorContext.CompareAndSwap(nil, seal) {
		return errors.New("incremental component vector context is already bound")
	}
	return nil
}
