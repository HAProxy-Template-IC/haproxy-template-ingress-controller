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
	"io"
	"maps"
	"reflect"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalVectorContextKey struct{}

type incrementalVectorContextSeal struct {
	controller *incrementalVectorController
	index      int
	seal       *incrementalVectorContextSeal
}

type incrementalVectorController struct {
	authority *native.VectorAuthority
	lifecycle IncrementalComponentVectorLifecycle
	writer    *incrementalVectorSegmentWriter
	contexts  []*incrementalVectorContextSeal

	mu        sync.Mutex
	active    int
	pending   int
	output    string
	next      int
	failure   int
	terminal  error
	aborted   bool
	vmAborted bool
	vmAbort   int
}

type incrementalVectorSegmentWriter struct {
	outputs []strings.Builder
	active  int
}

func (e *ScriggoEngine) IncrementalComponentVectorEligibility(
	templateName string,
) (IncrementalComponentVectorEligibility, bool) {
	entryPoint, ok := e.incrementalVectorEntryPoints[templateName]
	if !ok || !validIncrementalVectorEntryPoint(e, templateName, entryPoint) {
		return IncrementalComponentVectorEligibility{}, false
	}
	names := make([]string, len(entryPoint.bindings))
	for index := range entryPoint.bindings {
		names[index] = entryPoint.bindings[index].name
	}
	return IncrementalComponentVectorEligibility{BindingNames: names}, true
}

func (e *ScriggoEngine) RenderIncrementalComponentVector(
	ctx context.Context,
	templateName string,
	input IncrementalComponentVectorInput,
) (err error) {
	entryPoint, ok := e.incrementalVectorEntryPoints[templateName]
	if !ok || !validIncrementalVectorEntryPoint(e, templateName, entryPoint) {
		return fmt.Errorf("incremental component %q is not vector eligible", templateName)
	}
	prepared, err := prepareIncrementalVectorInput(ctx, entryPoint, input)
	if err != nil {
		abortIncrementalVectorInput(input.Lifecycle, err)
		return err
	}
	controller := newIncrementalVectorController(
		prepared.authority,
		input.Lifecycle,
		prepared.contextSeals,
		input.Count,
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			controller.abort(fmt.Errorf("incremental vector panic: %v", recovered))
			panic(recovered)
		}
		if err != nil {
			controller.abort(err)
		}
	}()
	values := maps.Clone(input.SharedContext)
	if values == nil {
		values = make(map[string]any, 2)
	}
	boundary := native.NewVectorBoundary()
	values[incrementalVectorIndicesName] = makeIncrementalVectorIndices(input.Count)
	values[incrementalVectorBoundaryName] = boundary
	runOptions := &scriggo.RunOptions{
		Context:                  ctx,
		Deterministic:            true,
		ObserveMutationContext:   observeIncrementalVectorMutation,
		ObserveNativeCallContext: observeIncrementalVectorNativeCall,
		BeforeNativeCallContext:  beforeIncrementalNativeCall,
		NativeFunctionTrampolines: incrementalVectorNativeFunctionTrampolines(
			prepared.nativeFunctionTrampolines,
		),
		Vector: &scriggo.VectorRunOptions{
			Authority: prepared.authority,
			Count:     input.Count,
			Bindings:  prepared.bindings,
			Contexts:  prepared.contexts,
			VMNative:  true,
			Boundary:  boundary,
			Lifecycle: controller,
		},
	}
	if err = runScriggoTemplate(
		ctx,
		templateName,
		entryPoint.template,
		controller.writer,
		values,
		runOptions,
	); err != nil {
		return &IncrementalComponentBatchError{Index: controller.failureIndex(), Err: err}
	}
	if err = controller.complete(); err != nil {
		return &IncrementalComponentBatchError{
			Index: controller.failureIndex(),
			Err:   err,
		}
	}
	return nil
}

func incrementalVectorNativeFunctionTrampolines(
	base []*native.FunctionTrampoline,
) []*native.FunctionTrampoline {
	result := make(
		[]*native.FunctionTrampoline,
		0,
		len(incrementalNativeFunctionFrameTrampolines)+len(base),
	)
	result = append(result, incrementalNativeFunctionFrameTrampolines...)
	return append(result, base...)
}

func incrementalVectorCarrierNativeFunctionTrampolines(
	base []*native.FunctionTrampoline,
) []*native.FunctionTrampoline {
	result := make(
		[]*native.FunctionTrampoline,
		0,
		len(incrementalVectorWaveControllerTrampolines)+len(incrementalNativeFunctionFrameTrampolines)+len(base),
	)
	result = append(result, incrementalVectorWaveControllerTrampolines...)
	result = append(result, incrementalNativeFunctionFrameTrampolines...)
	return append(result, base...)
}

type preparedIncrementalVectorInput struct {
	authority                 *native.VectorAuthority
	bindings                  map[string]any
	contexts                  []context.Context
	contextSeals              []*incrementalVectorContextSeal
	nativeFunctionTrampolines []*native.FunctionTrampoline
}

func prepareIncrementalVectorInput(
	ctx context.Context,
	entryPoint *incrementalVectorEntryPoint,
	input IncrementalComponentVectorInput,
) (*preparedIncrementalVectorInput, error) {
	if ctx == nil {
		return nil, errors.New("incremental component vector context is nil")
	}
	if input.Count <= 0 {
		return nil, errors.New("incremental component vector count must be positive")
	}
	if isNilValue(input.Lifecycle) {
		return nil, errors.New("incremental component vector lifecycle is nil")
	}
	if len(input.Contexts) != input.Count {
		return nil, fmt.Errorf(
			"incremental component vector has %d contexts for %d items",
			len(input.Contexts),
			input.Count,
		)
	}
	bindings := make(map[string]incrementalVectorBinding, len(entryPoint.bindings))
	for _, binding := range entryPoint.bindings {
		bindings[binding.name] = binding
	}
	for name := range input.SharedContext {
		if _, dynamic := bindings[name]; dynamic {
			return nil, fmt.Errorf("incremental component vector binding %q is also fixed", name)
		}
		if strings.HasPrefix(name, incrementalVectorIdentifierPrefix) {
			return nil, fmt.Errorf("incremental component vector fixed name %q is reserved", name)
		}
	}
	if len(input.Bindings) != len(bindings) {
		return nil, fmt.Errorf(
			"incremental component vector has %d bindings, expected %d",
			len(input.Bindings),
			len(bindings),
		)
	}
	nativeFunctions := newIncrementalResourceNativeFunctionCollector(nil)
	originalColumns, ownedBindings, err := normalizeIncrementalVectorInputColumns(input, bindings, nativeFunctions)
	if err != nil {
		return nil, err
	}
	prepared := &preparedIncrementalVectorInput{
		authority:                 native.NewVectorAuthority(),
		bindings:                  ownedBindings,
		contexts:                  make([]context.Context, input.Count),
		contextSeals:              make([]*incrementalVectorContextSeal, input.Count),
		nativeFunctionTrampolines: nativeFunctions.trampolines,
	}
	for index := range input.Count {
		itemCtx := input.Contexts[index]
		if itemCtx == nil {
			return nil, &IncrementalComponentBatchError{
				Index: index,
				Err:   errors.New("incremental component vector item context is nil"),
			}
		}
		if err := validateIncrementalVectorItemContext(itemCtx, originalColumns, index); err != nil {
			return nil, &IncrementalComponentBatchError{Index: index, Err: err}
		}
		seal := &incrementalVectorContextSeal{index: index}
		seal.seal = seal
		prepared.contextSeals[index] = seal
		boundContext, bindErr := bindIncrementalVectorContext(itemCtx, seal)
		if bindErr != nil {
			return nil, &IncrementalComponentBatchError{Index: index, Err: bindErr}
		}
		prepared.contexts[index] = boundContext
	}
	return prepared, nil
}

func bindIncrementalVectorContext(
	ctx context.Context,
	seal *incrementalVectorContextSeal,
) (context.Context, error) {
	if ctx == nil || seal == nil || seal.seal != seal || seal.index < 0 {
		return nil, errors.New("incremental component vector context has invalid provenance")
	}
	if component, ok := ctx.(*incrementalComponentExecutionContext); ok {
		if err := component.bindVectorContext(seal); err != nil {
			return nil, err
		}
		return component, nil
	}
	return context.WithValue(ctx, incrementalVectorContextKey{}, seal), nil
}

func validateIncrementalVectorItemContext(
	ctx context.Context,
	originalColumns map[string]reflect.Value,
	index int,
) error {
	if compact, ok := ctx.Value(incrementalRenderContextValuesKey{}).(*incrementalComponentExecutionContext); ok {
		return validateIncrementalVectorCompactItemContext(compact, originalColumns, index)
	}
	return validateIncrementalVectorMapItemContext(ctx, originalColumns, index)
}

func validateIncrementalVectorCompactItemContext(
	compact *incrementalComponentExecutionContext,
	originalColumns map[string]reflect.Value,
	index int,
) error {
	if !compact.valid() || !compact.compactValues || compact.storage == nil ||
		compact.binding.storage != compact.storage || compact.binding.seal != &compact.binding {
		return errors.New("incremental component vector item has an invalid compact render context")
	}
	if err := compact.binding.matchesValues(&compact.values); err != nil {
		return fmt.Errorf("incremental component vector item immutable binding: %w", err)
	}
	for name, column := range originalColumns {
		actual, exists := compact.incrementalRenderContextValue(name)
		if !exists || !sameIncrementalVectorValue(column.Index(index), reflect.ValueOf(actual)) {
			return fmt.Errorf("incremental component vector item does not own binding %q", name)
		}
	}
	if compact.values.Shared == nil {
		return errors.New("incremental component vector item has invalid shared context")
	}
	return nil
}

func validateIncrementalVectorMapItemContext(
	ctx context.Context,
	originalColumns map[string]reflect.Value,
	index int,
) error {
	values, ok := ctx.Value(RenderContextContextKey).(map[string]any)
	if !ok || values == nil {
		return errors.New("incremental component vector item has no render context")
	}
	if _, err := withBoundIncrementalImmutableInputs(ctx, values, nil); err != nil {
		return fmt.Errorf("incremental component vector item immutable binding: %w", err)
	}
	for name, column := range originalColumns {
		actual, exists := values[name]
		if !exists || !sameIncrementalVectorValue(column.Index(index), reflect.ValueOf(actual)) {
			return fmt.Errorf("incremental component vector item does not own binding %q", name)
		}
	}
	renderSubject, ok := values[declRenderSubject].(map[string]any)
	if !ok || renderSubject == nil {
		return errors.New("incremental component vector item has invalid renderSubject")
	}
	mode, ok := renderSubject["mode"].(string)
	if !ok || (mode != renderModeReconcile && mode != renderModeAdmission) || values[declRenderMode] != mode {
		return errors.New("incremental component vector item has invalid render mode")
	}
	shared, ok := values[declShared].(SharedContributionContext)
	if !ok || isNilValue(shared) {
		return errors.New("incremental component vector item has invalid shared context")
	}
	return nil
}

func normalizeIncrementalVectorInputColumns(
	input IncrementalComponentVectorInput,
	bindings map[string]incrementalVectorBinding,
	nativeFunctions *incrementalResourceNativeFunctionCollector,
) (originalColumns map[string]reflect.Value, ownedBindings map[string]any, err error) {
	originalColumns = make(map[string]reflect.Value, len(bindings))
	ownedBindings = make(map[string]any, len(bindings))
	for name, binding := range bindings {
		column, exists := input.Bindings[name]
		if !exists {
			return nil, nil, fmt.Errorf("incremental component vector binding %q is missing", name)
		}
		value := reflect.ValueOf(column)
		if !value.IsValid() || value.Kind() != reflect.Slice || value.Len() != input.Count {
			return nil, nil, fmt.Errorf(
				"incremental component vector binding %q must be a concrete slice of length %d",
				name,
				input.Count,
			)
		}
		owned, err := normalizeIncrementalVectorColumn(name, value, binding.variableType, input.Count)
		if err != nil {
			return nil, nil, err
		}
		originalColumns[name] = value
		ownedBindings[name] = owned.Interface()
		if name == declResources {
			for index := range input.Count {
				nativeFunctions.add(value.Index(index).Interface())
			}
		}
	}
	for name := range input.Bindings {
		if _, exists := bindings[name]; !exists {
			return nil, nil, fmt.Errorf("incremental component vector binding %q is not eligible", name)
		}
	}
	return originalColumns, ownedBindings, nil
}

func normalizeIncrementalVectorColumn(
	name string,
	column reflect.Value,
	variableType reflect.Type,
	count int,
) (reflect.Value, error) {
	if err := validateIncrementalVectorColumn(name, column, variableType); err != nil {
		return reflect.Value{}, err
	}
	owned := reflect.MakeSlice(reflect.SliceOf(variableType), count, count)
	for index := range count {
		value, err := normalizeIncrementalVectorValue(column.Index(index), variableType)
		if err != nil {
			return reflect.Value{}, fmt.Errorf(
				"incremental component vector binding %q item %d: %w",
				name,
				index,
				err,
			)
		}
		owned.Index(index).Set(value)
	}
	return owned, nil
}

func validateIncrementalVectorColumn(
	name string,
	column reflect.Value,
	variableType reflect.Type,
) error {
	elementType := column.Type().Elem()
	direct := elementType == variableType
	indirect := elementType.Kind() == reflect.Pointer && elementType.Elem().AssignableTo(variableType)
	if !direct && !indirect {
		return fmt.Errorf(
			"incremental component vector binding %q must have element type %v or a pointer assignable to it",
			name,
			variableType,
		)
	}
	return nil
}

func normalizeIncrementalVectorValue(value reflect.Value, variableType reflect.Type) (reflect.Value, error) {
	if !value.IsValid() {
		if variableType.Kind() == reflect.Interface {
			return reflect.Zero(variableType), nil
		}
		return reflect.Value{}, errors.New("value is nil")
	}
	if value.Type() == variableType {
		return value, nil
	}
	if value.Kind() == reflect.Pointer && value.Type().Elem().AssignableTo(variableType) {
		if value.IsNil() {
			return reflect.Value{}, errors.New("pointer is nil")
		}
		return value.Elem(), nil
	}
	if variableType.Kind() == reflect.Interface && value.Type().AssignableTo(variableType) {
		return value.Convert(variableType), nil
	}
	return reflect.Value{}, fmt.Errorf("value has type %v, want %v", value.Type(), variableType)
}

func sameIncrementalVectorValue(left, right reflect.Value) bool {
	for left.IsValid() && left.Kind() == reflect.Interface {
		if left.IsNil() {
			return !right.IsValid() || right.Kind() == reflect.Interface && right.IsNil()
		}
		left = left.Elem()
	}
	for right.IsValid() && right.Kind() == reflect.Interface {
		if right.IsNil() {
			return false
		}
		right = right.Elem()
	}
	if !left.IsValid() || !right.IsValid() || left.Type() != right.Type() {
		return !left.IsValid() && !right.IsValid()
	}
	return sameIncrementalVectorConcreteValue(left, right)
}

func sameIncrementalVectorConcreteValue(left, right reflect.Value) bool {
	switch left.Kind() {
	case reflect.Map, reflect.Pointer, reflect.UnsafePointer, reflect.Chan, reflect.Func:
		return left.IsNil() == right.IsNil() && (left.IsNil() || left.Pointer() == right.Pointer())
	case reflect.Slice:
		return left.IsNil() == right.IsNil() && left.Len() == right.Len() &&
			left.Cap() == right.Cap() && (left.IsNil() || left.Cap() == 0 || left.Pointer() == right.Pointer())
	default:
		return reflect.DeepEqual(left.Interface(), right.Interface())
	}
}

func newIncrementalVectorController(
	authority *native.VectorAuthority,
	lifecycle IncrementalComponentVectorLifecycle,
	contextSeals []*incrementalVectorContextSeal,
	count int,
) *incrementalVectorController {
	controller := &incrementalVectorController{
		authority: authority,
		lifecycle: lifecycle,
		writer:    newIncrementalVectorSegmentWriter(count),
		contexts:  contextSeals,
		active:    -1,
		pending:   -1,
		failure:   -1,
		vmAbort:   -1,
	}
	for index := range contextSeals {
		contextSeals[index].controller = controller
	}
	return controller
}

func (controller *incrementalVectorController) Begin(ctx context.Context, index int) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.terminal != nil {
		return controller.terminal
	}
	if index != controller.next || controller.active >= 0 || controller.pending >= 0 ||
		index < 0 || index >= len(controller.contexts) {
		return fmt.Errorf("incremental component vector begin index %d is invalid", index)
	}
	controller.failure = index
	controller.active = index
	seal, _ := ctx.Value(incrementalVectorContextKey{}).(*incrementalVectorContextSeal)
	if seal == nil || seal.seal != seal || seal.controller != controller || seal.index != index ||
		controller.contexts[index] != seal {
		return errors.New("incremental component vector context has invalid provenance")
	}
	if err := controller.lifecycle.Begin(index); err != nil {
		return err
	}
	if err := controller.writer.begin(index); err != nil {
		return err
	}
	return nil
}

func (controller *incrementalVectorController) Finish(index int) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.terminal != nil {
		return controller.terminal
	}
	if controller.active != index || controller.pending >= 0 {
		return fmt.Errorf("incremental component vector finish index %d is invalid", index)
	}
	output, err := controller.writer.end(index)
	if err != nil {
		return err
	}
	controller.pending = index
	controller.output = output
	return nil
}

func (controller *incrementalVectorController) Commit(index int) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.terminal != nil {
		return controller.terminal
	}
	if controller.active != index || controller.pending != index || controller.writer.active >= 0 {
		return fmt.Errorf("incremental component vector commit index %d is invalid", index)
	}
	if err := controller.lifecycle.End(index, controller.output); err != nil {
		controller.terminal = err
		return err
	}
	controller.active = -1
	controller.pending = -1
	controller.output = ""
	controller.next++
	return nil
}

func (controller *incrementalVectorController) failureIndex() int {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return max(controller.failure, 0)
}

func (controller *incrementalVectorController) complete() error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.terminal != nil {
		return controller.terminal
	}
	if controller.active >= 0 || controller.pending >= 0 || controller.next != len(controller.contexts) ||
		controller.writer.active >= 0 || controller.output != "" {
		return errors.New("incremental component vector lifecycle did not complete")
	}
	return nil
}

func (controller *incrementalVectorController) abort(cause error) {
	controller.mu.Lock()
	if controller.aborted {
		controller.mu.Unlock()
		return
	}
	activeIndex := controller.active
	if controller.vmAborted {
		activeIndex = controller.vmAbort
	}
	controller.aborted = true
	controller.resetLocked()
	controller.mu.Unlock()
	controller.lifecycle.Abort(activeIndex, cause)
}

func (controller *incrementalVectorController) Abort(activeIndex int, cause error) {
	controller.mu.Lock()
	if controller.aborted || controller.vmAborted {
		controller.mu.Unlock()
		return
	}
	controller.vmAborted = true
	controller.vmAbort = activeIndex
	controller.terminal = cause
	controller.resetLocked()
	controller.mu.Unlock()
}

func (controller *incrementalVectorController) resetLocked() {
	controller.active = -1
	controller.pending = -1
	controller.output = ""
	controller.writer.abort()
}

func abortIncrementalVectorInput(
	lifecycle IncrementalComponentVectorLifecycle,
	cause error,
) {
	if !isNilValue(lifecycle) {
		lifecycle.Abort(-1, cause)
	}
}

func newIncrementalVectorSegmentWriter(count int) *incrementalVectorSegmentWriter {
	return &incrementalVectorSegmentWriter{outputs: make([]strings.Builder, count), active: -1}
}

func (writer *incrementalVectorSegmentWriter) begin(index int) error {
	if index < 0 || index >= len(writer.outputs) || writer.active >= 0 {
		return fmt.Errorf("incremental component vector output index %d is invalid", index)
	}
	writer.active = index
	return nil
}

func (writer *incrementalVectorSegmentWriter) end(index int) (string, error) {
	if writer.active != index {
		return "", fmt.Errorf("incremental component vector output index %d is not active", index)
	}
	writer.active = -1
	return writer.outputs[index].String(), nil
}

func (writer *incrementalVectorSegmentWriter) abort() {
	writer.active = -1
}

func (writer *incrementalVectorSegmentWriter) Write(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	if writer.active < 0 {
		return 0, errors.New("incremental component vector output has no active item")
	}
	return writer.outputs[writer.active].Write(value)
}

func (writer *incrementalVectorSegmentWriter) WriteTextFragment(fragment native.TextFragment) error {
	if writer.active < 0 {
		return errors.New("incremental component vector output has no active item")
	}
	_, err := fragment.WriteTo(io.Writer(&writer.outputs[writer.active]))
	return err
}

func makeIncrementalVectorIndices(count int) []int {
	indices := make([]int, count)
	for index := range indices {
		indices[index] = index
	}
	return indices
}

func validIncrementalVectorEntryPoint(
	engine *ScriggoEngine,
	templateName string,
	entryPoint *incrementalVectorEntryPoint,
) bool {
	if engine == nil || entryPoint == nil || entryPoint.seal != entryPoint || entryPoint.template == nil ||
		entryPoint.original == nil || len(entryPoint.bindings) == 0 {
		return false
	}
	if engine.compiledTemplates[templateName] != entryPoint.original ||
		len(entryPoint.bindings) != len(incrementalVectorBaseBindingNames) {
		return false
	}
	for index := range entryPoint.bindings {
		if entryPoint.bindings[index].name != incrementalVectorBaseBindingNames[index] ||
			entryPoint.bindings[index].variableType == nil {
			return false
		}
	}
	return true
}

func observeIncrementalVectorMutation(ctx context.Context, mutation scriggo.Mutation) error {
	if mutation.Path == incrementalVectorTemplatePath || mutation.Path == incrementalVectorCarrierTemplatePath {
		return nil
	}
	return observeIncrementalMutation(ctx, mutation)
}

func observeIncrementalVectorNativeCall(ctx context.Context, call scriggo.NativeCall) error {
	if call.Path == incrementalVectorTemplatePath || call.Path == incrementalVectorCarrierTemplatePath ||
		call.Path == incrementalSourceTransactionTemplatePath {
		if call.Path == incrementalVectorCarrierTemplatePath && call.Receiver.IsValid() &&
			call.Receiver.Type() == reflect.TypeFor[*incrementalVectorWaveController]() &&
			(call.Method == "BeginWave" || call.Method == "EndWave") {
			return nil
		}
		if call.Path == incrementalSourceTransactionTemplatePath && call.Receiver.IsValid() &&
			call.Receiver.Type() == reflect.TypeFor[*incrementalSourceTransactionController]() &&
			(call.Method == "BeginWave" || call.Method == "EndWave") {
			return nil
		}
	}
	return observeIncrementalNativeCall(ctx, call)
}
