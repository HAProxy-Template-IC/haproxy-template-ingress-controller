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
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalVectorCarrierWaveShape struct {
	lanes []IncrementalComponentVectorCarrierWaveLane
	order []int
	start int
	end   int
}

type preparedIncrementalVectorCarrierWavesInput struct {
	authority     *native.VectorAuthority
	orders        [][]int
	starts        []int
	ends          []int
	shapes        []incrementalVectorCarrierWaveShape
	contextSeals  []*incrementalVectorContextSeal
	templateNames []string
}

type preparedIncrementalVectorCarrierWave struct {
	start                     int
	bindings                  map[string]any
	contexts                  []context.Context
	nativeFunctionTrampolines []*native.FunctionTrampoline
}

type preparedIncrementalVectorCarrierWaveLane struct {
	bindings map[string]reflect.Value
	contexts []context.Context
}

type incrementalVectorWaveController struct {
	items     *incrementalVectorController
	lifecycle IncrementalComponentVectorCarrierWaveLifecycle
	shapes    []incrementalVectorCarrierWaveShape
	ctx       context.Context

	mu         sync.Mutex
	nextWave   int
	activeWave int
	waveLoaded bool
	waveSealed bool
	vectorEnv  native.VectorEnv
	carrier    *incrementalVectorCarrier
}

var incrementalVectorWaveControllerTrampolines = []*native.FunctionTrampoline{
	native.MakeMethodTrampolineWithFrame(
		reflect.TypeFor[*incrementalVectorWaveController](),
		"BeginWave",
		func(args []reflect.Value) []reflect.Value {
			args[0].Interface().(*incrementalVectorWaveController).BeginWave(
				args[1].Interface().(native.Env),
				int(args[2].Int()),
			)
			return nil
		},
		func(frame native.FunctionCallFrame) {
			frame.ArgValue(0).Interface().(*incrementalVectorWaveController).BeginWave(
				frame.ArgEnv(1),
				int(frame.ArgInt(2)),
			)
		},
	),
	native.MakeMethodTrampolineWithFrame(
		reflect.TypeFor[*incrementalVectorWaveController](),
		"EndWave",
		func(args []reflect.Value) []reflect.Value {
			args[0].Interface().(*incrementalVectorWaveController).EndWave(
				args[1].Interface().(native.Env),
				int(args[2].Int()),
			)
			return nil
		},
		func(frame native.FunctionCallFrame) {
			frame.ArgValue(0).Interface().(*incrementalVectorWaveController).EndWave(
				frame.ArgEnv(1),
				int(frame.ArgInt(2)),
			)
		},
	),
}

func (e *ScriggoEngine) RenderIncrementalComponentVectorCarrierWaves(
	ctx context.Context,
	input IncrementalComponentVectorCarrierWavesInput,
) (err error) {
	carrier := e.incrementalVectorCarrier
	if !validIncrementalVectorCarrier(e, carrier) {
		return errors.New("incremental component vector carrier is unavailable")
	}
	prepared, err := prepareIncrementalVectorCarrierWavesInput(ctx, carrier, input)
	if err != nil {
		abortIncrementalVectorInput(input.Lifecycle, err)
		return err
	}
	items := newIncrementalVectorController(
		prepared.authority,
		input.Lifecycle,
		prepared.contextSeals,
		len(prepared.contextSeals),
	)
	controller := newIncrementalVectorWaveController(items, input.Lifecycle, prepared.shapes, ctx)
	controller.carrier = carrier
	defer func() {
		if recovered := recover(); recovered != nil {
			items.abort(fmt.Errorf("incremental vector carrier waves panic: %v", recovered))
			panic(recovered)
		}
		if err != nil {
			items.abort(err)
		}
	}()
	values := maps.Clone(input.SharedContext)
	if values == nil {
		values = make(map[string]any, 4)
	}
	values[incrementalVectorCarrierOrderName] = prepared.orders
	values[incrementalVectorCarrierStartsName] = prepared.starts
	values[incrementalVectorCarrierEndsName] = prepared.ends
	values[incrementalVectorRuntimeName] = controller
	boundary := native.NewVectorBoundary()
	values[incrementalVectorBoundaryName] = boundary
	bindingNames := make([]string, len(carrier.bindings))
	for index, binding := range carrier.bindings {
		bindingNames[index] = binding.name
	}
	runOptions := &scriggo.RunOptions{
		Context:                   ctx,
		Deterministic:             true,
		ObserveMutationContext:    observeIncrementalVectorMutation,
		ObserveNativeCallContext:  observeIncrementalVectorNativeCall,
		BeforeNativeCallContext:   beforeIncrementalNativeCall,
		NativeFunctionTrampolines: incrementalVectorCarrierNativeFunctionTrampolines(nil),
		Vector: &scriggo.VectorRunOptions{
			Authority:        prepared.authority,
			Count:            len(prepared.contextSeals),
			DeferredBindings: bindingNames,
			VMNative:         true,
			Boundary:         boundary,
			Lifecycle:        items,
		},
	}
	if err = runScriggoTemplate(
		ctx,
		incrementalVectorCarrierTemplatePath,
		carrier.template,
		items.writer,
		values,
		runOptions,
	); err != nil {
		failure := items.failureIndex()
		cause := err
		if failure >= 0 && failure < len(prepared.templateNames) {
			cause = remapIncrementalVectorCarrierError(prepared.templateNames[failure], err)
		}
		return &IncrementalComponentBatchError{
			Index: failure,
			Err:   cause,
		}
	}
	if err = controller.complete(); err != nil {
		return &IncrementalComponentBatchError{Index: items.failureIndex(), Err: err}
	}
	return nil
}

func prepareIncrementalVectorCarrierWavesInput(
	ctx context.Context,
	carrier *incrementalVectorCarrier,
	input IncrementalComponentVectorCarrierWavesInput,
) (*preparedIncrementalVectorCarrierWavesInput, error) {
	if ctx == nil {
		return nil, errors.New("incremental component vector carrier waves context is nil")
	}
	if isNilValue(input.Lifecycle) {
		return nil, errors.New("incremental component vector carrier waves lifecycle is nil")
	}
	if len(input.Waves) == 0 {
		return nil, errors.New("incremental component vector carrier has no waves")
	}
	bindingNames := make(map[string]struct{}, len(carrier.bindings))
	for _, binding := range carrier.bindings {
		bindingNames[binding.name] = struct{}{}
	}
	for name := range input.SharedContext {
		if _, dynamic := bindingNames[name]; dynamic {
			return nil, fmt.Errorf("incremental component vector binding %q is also fixed", name)
		}
		if strings.HasPrefix(name, incrementalVectorIdentifierPrefix) {
			return nil, fmt.Errorf("incremental component vector fixed name %q is reserved", name)
		}
	}
	prepared := &preparedIncrementalVectorCarrierWavesInput{
		authority: native.NewVectorAuthority(),
		orders:    make([][]int, len(input.Waves)),
		starts:    make([]int, len(input.Waves)),
		ends:      make([]int, len(input.Waves)),
		shapes:    make([]incrementalVectorCarrierWaveShape, len(input.Waves)),
	}
	total := 0
	for waveIndex, wave := range input.Waves {
		shape, templateNames, shapeErr := prepareIncrementalVectorCarrierWaveShape(
			carrier,
			waveIndex,
			wave,
			total,
		)
		if shapeErr != nil {
			return nil, shapeErr
		}
		prepared.orders[waveIndex] = slices.Clone(shape.order)
		prepared.starts[waveIndex] = shape.start
		prepared.ends[waveIndex] = shape.end
		prepared.templateNames = append(prepared.templateNames, templateNames...)
		prepared.shapes[waveIndex] = shape
		total = shape.end
	}
	if total == 0 {
		return nil, errors.New("incremental component vector carrier waves have no items")
	}
	prepared.contextSeals = make([]*incrementalVectorContextSeal, total)
	for index := range total {
		seal := &incrementalVectorContextSeal{index: index}
		seal.seal = seal
		prepared.contextSeals[index] = seal
	}
	return prepared, nil
}

func prepareIncrementalVectorCarrierWaveShape(
	carrier *incrementalVectorCarrier,
	waveIndex int,
	wave IncrementalComponentVectorCarrierWave,
	start int,
) (incrementalVectorCarrierWaveShape, []string, error) {
	shape := incrementalVectorCarrierWaveShape{
		lanes: slices.Clone(wave.Lanes),
		start: start,
		end:   start,
	}
	laneIDs, counts, err := incrementalVectorCarrierWaveLanes(carrier, waveIndex, wave, &shape)
	if err != nil {
		return incrementalVectorCarrierWaveShape{}, nil, err
	}
	count := shape.end - shape.start
	if wave.EntryPoints == nil {
		shape.order = make([]int, 0, count)
		for laneIndex, id := range laneIDs {
			for range wave.Lanes[laneIndex].Count {
				shape.order = append(shape.order, id)
			}
		}
	} else {
		order, orderErr := incrementalVectorCarrierWaveOrder(carrier, waveIndex, wave, counts, count)
		if orderErr != nil {
			return incrementalVectorCarrierWaveShape{}, nil, orderErr
		}
		shape.order = order
	}
	templateNames := make([]string, len(shape.order))
	for index, id := range shape.order {
		templateNames[index] = carrier.entryPoints[id]
	}
	return shape, templateNames, nil
}

func prepareIncrementalVectorCarrierWaveLanes(
	carrier *incrementalVectorCarrier,
	shape incrementalVectorCarrierWaveShape,
	lanes []IncrementalComponentVectorCarrierLane,
	bindingByName map[string]incrementalVectorBinding,
) ([]preparedIncrementalVectorCarrierWaveLane, map[int]int, error) {
	preparedLanes := make([]preparedIncrementalVectorCarrierWaveLane, len(lanes))
	laneByID := make(map[int]int, len(lanes))
	for laneIndex, lane := range lanes {
		expected := shape.lanes[laneIndex]
		if lane.TemplateName != expected.TemplateName || lane.Count != expected.Count ||
			len(lane.Contexts) != lane.Count || len(lane.Bindings) != len(bindingByName) {
			return nil, nil, fmt.Errorf("incremental component vector carrier loaded lane %d does not match its shape", laneIndex)
		}
		id, eligible := carrier.laneByName[lane.TemplateName]
		if !eligible || carrier.entryPoints[id] != lane.TemplateName {
			return nil, nil, fmt.Errorf("incremental component vector carrier loaded lane %d is not eligible", laneIndex)
		}
		if _, duplicate := laneByID[id]; duplicate {
			return nil, nil, fmt.Errorf("incremental component vector carrier loaded lane %d is duplicated", laneIndex)
		}
		laneByID[id] = laneIndex
		originalColumns, err := incrementalVectorCarrierWaveLaneColumns(lane, bindingByName)
		if err != nil {
			return nil, nil, err
		}
		preparedLanes[laneIndex] = preparedIncrementalVectorCarrierWaveLane{
			bindings: originalColumns,
			contexts: slices.Clone(lane.Contexts),
		}
	}
	return preparedLanes, laneByID, nil
}

func incrementalVectorCarrierWaveLaneColumns(
	lane IncrementalComponentVectorCarrierLane,
	bindingByName map[string]incrementalVectorBinding,
) (map[string]reflect.Value, error) {
	originalColumns := make(map[string]reflect.Value, len(bindingByName))
	for name, binding := range bindingByName {
		column, exists := lane.Bindings[name]
		if !exists {
			return nil, fmt.Errorf("incremental component vector carrier lane %q binding %q is missing", lane.TemplateName, name)
		}
		value := reflect.ValueOf(column)
		if !value.IsValid() || value.Kind() != reflect.Slice || value.Len() != lane.Count {
			return nil, fmt.Errorf("incremental component vector carrier lane %q binding %q must be a concrete slice of length %d", lane.TemplateName, name, lane.Count)
		}
		if err := validateIncrementalVectorColumn(name, value, binding.variableType); err != nil {
			return nil, fmt.Errorf("incremental component vector carrier lane %q: %w", lane.TemplateName, err)
		}
		originalColumns[name] = value
	}
	for name := range lane.Bindings {
		if _, exists := bindingByName[name]; !exists {
			return nil, fmt.Errorf("incremental component vector carrier lane %q binding %q is not eligible", lane.TemplateName, name)
		}
	}
	return originalColumns, nil
}

func flattenIncrementalVectorCarrierWaveItem(
	carrier *incrementalVectorCarrier,
	lane preparedIncrementalVectorCarrierWaveLane,
	laneIndexPosition, itemIndex, globalIndex int,
	flattened map[string]reflect.Value,
	items *incrementalVectorController,
	nativeFunctions *incrementalResourceNativeFunctionCollector,
) (context.Context, error) {
	itemCtx := lane.contexts[laneIndexPosition]
	if itemCtx == nil {
		return nil, &IncrementalComponentBatchError{Index: globalIndex, Err: errors.New("incremental component vector item context is nil")}
	}
	if err := validateIncrementalVectorItemContext(itemCtx, lane.bindings, laneIndexPosition); err != nil {
		return nil, &IncrementalComponentBatchError{Index: globalIndex, Err: err}
	}
	for _, binding := range carrier.bindings {
		value, valueErr := normalizeIncrementalVectorValue(
			lane.bindings[binding.name].Index(laneIndexPosition),
			binding.variableType,
		)
		if valueErr != nil {
			return nil, &IncrementalComponentBatchError{
				Index: globalIndex,
				Err: fmt.Errorf(
					"incremental component vector binding %q item %d: %w",
					binding.name,
					laneIndexPosition,
					valueErr,
				),
			}
		}
		flattened[binding.name].Index(itemIndex).Set(value)
	}
	boundContext, bindErr := bindIncrementalVectorContext(itemCtx, items.contexts[globalIndex])
	if bindErr != nil {
		return nil, &IncrementalComponentBatchError{Index: globalIndex, Err: bindErr}
	}
	nativeFunctions.add(lane.bindings[declResources].Index(laneIndexPosition).Interface())
	return boundContext, nil
}

func incrementalVectorCarrierWaveLanes(
	carrier *incrementalVectorCarrier,
	waveIndex int,
	wave IncrementalComponentVectorCarrierWave,
	shape *incrementalVectorCarrierWaveShape,
) (laneIDs []int, counts map[int]int, err error) {
	laneIDs = make([]int, len(wave.Lanes))
	counts = make(map[int]int, len(wave.Lanes))
	for laneIndex, lane := range wave.Lanes {
		id, eligible := carrier.laneByName[lane.TemplateName]
		if !eligible || carrier.entryPoints[id] != lane.TemplateName {
			return nil, nil, fmt.Errorf(
				"incremental component vector carrier lane %q is not eligible",
				lane.TemplateName,
			)
		}
		if _, duplicate := counts[id]; duplicate {
			return nil, nil, fmt.Errorf(
				"incremental component vector carrier wave %d lane %q is duplicated",
				waveIndex,
				lane.TemplateName,
			)
		}
		if lane.Count <= 0 {
			return nil, nil, fmt.Errorf(
				"incremental component vector carrier lane %q count must be positive",
				lane.TemplateName,
			)
		}
		if lane.Count > int(^uint(0)>>1)-shape.end {
			return nil, nil, errors.New(
				"incremental component vector carrier item count overflows",
			)
		}
		laneIDs[laneIndex] = id
		counts[id] = lane.Count
		shape.end += lane.Count
	}
	return laneIDs, counts, nil
}

func incrementalVectorCarrierWaveOrder(
	carrier *incrementalVectorCarrier,
	waveIndex int,
	wave IncrementalComponentVectorCarrierWave,
	counts map[int]int,
	count int,
) ([]int, error) {
	if len(wave.EntryPoints) != count {
		return nil, fmt.Errorf(
			"incremental component vector carrier wave %d has %d entrypoints for %d items",
			waveIndex,
			len(wave.EntryPoints),
			count,
		)
	}
	order := make([]int, 0, count)
	remaining := maps.Clone(counts)
	for itemIndex, name := range wave.EntryPoints {
		id, eligible := carrier.laneByName[name]
		if !eligible || carrier.entryPoints[id] != name {
			return nil, fmt.Errorf(
				"incremental component vector carrier wave %d entrypoint %q is not eligible",
				waveIndex,
				name,
			)
		}
		if remaining[id] <= 0 {
			return nil, fmt.Errorf(
				"incremental component vector carrier wave %d entrypoint %q at item %d does not match lane counts",
				waveIndex,
				name,
				itemIndex,
			)
		}
		remaining[id]--
		order = append(order, id)
	}
	return order, nil
}

func newIncrementalVectorWaveController(
	items *incrementalVectorController,
	lifecycle IncrementalComponentVectorCarrierWaveLifecycle,
	shapes []incrementalVectorCarrierWaveShape,
	ctx context.Context,
) *incrementalVectorWaveController {
	return &incrementalVectorWaveController{
		items: items, lifecycle: lifecycle, shapes: shapes, ctx: ctx, activeWave: -1,
	}
}

func (controller *incrementalVectorWaveController) BeginWave(env native.Env, wave int) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if wave != controller.nextWave || controller.activeWave >= 0 || wave < 0 || wave >= len(controller.shapes) {
		env.Stop(fmt.Errorf("incremental component vector carrier wave %d is invalid", wave))
		return
	}
	shape := controller.shapes[wave]
	controller.items.mu.Lock()
	controller.items.failure = shape.start
	validItems := controller.items.active < 0 && controller.items.pending < 0 &&
		controller.items.next == shape.start && controller.items.writer.active < 0
	controller.items.mu.Unlock()
	if !validItems {
		env.Stop(fmt.Errorf("incremental component vector carrier wave %d starts with an invalid lifecycle", wave))
		return
	}
	controller.activeWave = wave
	controller.waveLoaded = false
	controller.waveSealed = false
	if controller.lifecycle == nil {
		controller.waveLoaded = true
		return
	}
	lanes, err := controller.lifecycle.LoadWave(controller.ctx, wave)
	if err != nil {
		env.Stop(err)
		return
	}
	prepared, err := prepareIncrementalVectorCarrierWave(controller.carrier, shape, lanes, controller.items)
	if err != nil {
		var batchErr *IncrementalComponentBatchError
		if errors.As(err, &batchErr) {
			controller.items.mu.Lock()
			controller.items.failure = batchErr.Index
			controller.items.mu.Unlock()
		}
		env.Stop(err)
		return
	}
	vectorEnv, ok := env.(native.VectorEnv)
	if !ok {
		env.Stop(errors.New("incremental component vector environment is unavailable"))
		return
	}
	if len(prepared.contexts) > 0 {
		if err := vectorEnv.LoadVectorRangeOwned(
			controller.items.authority,
			prepared.start,
			prepared.bindings,
			prepared.contexts,
			prepared.nativeFunctionTrampolines,
		); err != nil {
			env.Stop(err)
			return
		}
	}
	prepared.bindings = nil
	prepared.contexts = nil
	controller.vectorEnv = vectorEnv
	controller.waveLoaded = true
}

func (controller *incrementalVectorWaveController) EndWave(env native.Env, wave int) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if wave != controller.activeWave || wave != controller.nextWave || !controller.waveLoaded || controller.waveSealed {
		env.Stop(fmt.Errorf("incremental component vector carrier wave %d cannot end", wave))
		return
	}
	shape := controller.shapes[wave]
	controller.items.mu.Lock()
	controller.items.failure = shape.start
	terminal := controller.items.terminal
	validItems := terminal == nil && controller.items.active < 0 && controller.items.pending < 0 &&
		controller.items.next == shape.end && controller.items.writer.active < 0
	controller.items.mu.Unlock()
	if terminal != nil {
		env.Stop(terminal)
		return
	}
	if !validItems {
		env.Stop(fmt.Errorf("incremental component vector carrier wave %d ends with an invalid lifecycle", wave))
		return
	}
	if controller.lifecycle != nil {
		if err := controller.lifecycle.SealWave(wave); err != nil {
			env.Stop(err)
			return
		}
	}
	controller.waveSealed = true
	controller.waveLoaded = false
	controller.vectorEnv = nil
	controller.activeWave = -1
	controller.nextWave++
}

func (controller *incrementalVectorWaveController) complete() error {
	controller.mu.Lock()
	complete := controller.nextWave == len(controller.shapes) && controller.activeWave < 0 &&
		!controller.waveLoaded && controller.waveSealed
	controller.mu.Unlock()
	if !complete {
		return errors.New("incremental component vector carrier wave lifecycle did not complete")
	}
	return controller.items.complete()
}

func prepareIncrementalVectorCarrierWave(
	carrier *incrementalVectorCarrier,
	shape incrementalVectorCarrierWaveShape,
	lanes []IncrementalComponentVectorCarrierLane,
	items *incrementalVectorController,
) (*preparedIncrementalVectorCarrierWave, error) {
	if carrier == nil || len(lanes) != len(shape.lanes) {
		return nil, errors.New("incremental component vector carrier loaded wave does not match its shape")
	}
	bindingByName := make(map[string]incrementalVectorBinding, len(carrier.bindings))
	for _, binding := range carrier.bindings {
		bindingByName[binding.name] = binding
	}
	nativeFunctions := newIncrementalResourceNativeFunctionCollector(nil)
	preparedLanes, laneByID, err := prepareIncrementalVectorCarrierWaveLanes(carrier, shape, lanes, bindingByName)
	if err != nil {
		return nil, err
	}
	count := shape.end - shape.start
	if len(shape.order) != count {
		return nil, errors.New("incremental component vector carrier loaded wave has an invalid item count")
	}
	prepared := &preparedIncrementalVectorCarrierWave{
		start:    shape.start,
		bindings: make(map[string]any, len(carrier.bindings)),
		contexts: make([]context.Context, count),
	}
	flattened := make(map[string]reflect.Value, len(carrier.bindings))
	for _, binding := range carrier.bindings {
		flattened[binding.name] = reflect.MakeSlice(reflect.SliceOf(binding.variableType), count, count)
	}
	positions := make([]int, len(lanes))
	for itemIndex, id := range shape.order {
		laneIndex, exists := laneByID[id]
		if !exists {
			return nil, errors.New("incremental component vector carrier loaded wave has an invalid entrypoint order")
		}
		lane := preparedLanes[laneIndex]
		laneIndexPosition := positions[laneIndex]
		if laneIndexPosition >= len(lane.contexts) {
			return nil, errors.New("incremental component vector carrier loaded wave exceeds a lane count")
		}
		boundContext, itemErr := flattenIncrementalVectorCarrierWaveItem(
			carrier, lane, laneIndexPosition, itemIndex, shape.start+itemIndex,
			flattened, items, nativeFunctions,
		)
		if itemErr != nil {
			return nil, itemErr
		}
		prepared.contexts[itemIndex] = boundContext
		positions[laneIndex]++
	}
	for laneIndex, position := range positions {
		if position != len(preparedLanes[laneIndex].contexts) {
			return nil, errors.New("incremental component vector carrier loaded wave does not consume a lane")
		}
	}
	for _, binding := range carrier.bindings {
		prepared.bindings[binding.name] = flattened[binding.name].Interface()
	}
	prepared.nativeFunctionTrampolines = nativeFunctions.trampolines
	return prepared, nil
}
