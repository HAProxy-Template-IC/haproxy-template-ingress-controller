// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalVectorItemState struct {
	prepared        *preparedIncrementalComponent
	recorder        incrementalRecorder
	http            *incrementalHTTPFetcher
	derived         *rendercontext.DerivedResourceView
	derivedResolver *incrementalQueryDerivedResourceResolver
	token           *incrementalVectorItemToken
	lease           *incrementalVectorItemLease
	ctx             context.Context
	output          string
	outputSet       bool
	completed       bool
	beginCount      uint8
}

type incrementalVectorItemToken struct {
	seal      *incrementalVectorItemToken
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorItemLease struct {
	seal  *incrementalVectorItemLease
	token *incrementalVectorItemToken
}

type incrementalVectorExecution struct {
	seal      *incrementalVectorExecution
	session   *incrementalRenderSession
	component *incrementalComponent
	ctx       context.Context
	items     []incrementalVectorItemState

	mu                       sync.RWMutex
	callGate                 sync.RWMutex
	active                   atomic.Int64
	inflight                 atomic.Int64
	failed                   atomic.Bool
	directInvocationSequence atomic.Uint64
	directInvocations        [64]atomic.Uint64
	aborted                  bool
	finalized                bool
	terminal                 error
}

type incrementalVectorResourceView struct {
	seal      *incrementalVectorResourceView
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorStoreInvocation struct {
	seal   *incrementalVectorStoreInvocation
	view   *incrementalVectorResourceView
	token  *incrementalVectorItemToken
	index  int
	active atomic.Bool
}

type incrementalVectorRecorder struct {
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorSelector struct {
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorDeriver struct {
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorEventRecorder struct {
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorStatusRecorder struct {
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorBackendPlanRecorder struct {
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorHTTPFetcher struct {
	execution *incrementalVectorExecution
	index     int
}

type incrementalVectorExecutionContextKey struct{}
type incrementalVectorStoreInvocationContextKey struct{}

type preparedIncrementalVectorRender struct {
	execution *incrementalVectorExecution
	contexts  []context.Context
	fixed     map[string]any
	columns   map[string]any
}

func validateIncrementalVectorItem(
	session *incrementalRenderSession,
	component *incrementalComponent,
	item *preparedIncrementalComponent,
	index int,
	queryKeys map[incremental.QueryKey]struct{},
) error {
	if item == nil || item.component == nil || item.reader == nil ||
		!incrementalComponentsEqual(item.component, component) ||
		!componentQueryKeyMatches(item.queryKey, component, item.source, item.namespace, item.name) {
		return fmt.Errorf("incremental component vector item %d has invalid provenance", index)
	}
	resolved, source, namespace, name, exists := session.resolveComponentQuery(item.queryKey)
	if !exists || !incrementalComponentsEqual(&resolved, component) || source != item.source ||
		namespace != item.namespace || name != item.name {
		return fmt.Errorf("incremental component vector item %d query has invalid provenance", index)
	}
	if _, duplicate := queryKeys[item.queryKey]; duplicate {
		return fmt.Errorf("incremental component vector item %d duplicates a query", index)
	}
	queryKeys[item.queryKey] = struct{}{}
	if item.itemCertificate == nil || !item.itemCertificate.Guards(item.item) ||
		item.propsCertificate == nil || !item.propsCertificate.Guards(item.props) ||
		item.subjectCertificate == nil || !item.subjectCertificate.Guards(item.renderSubject) {
		return fmt.Errorf("incremental component vector item %d has invalid immutable provenance", index)
	}
	return nil
}

func newIncrementalVectorExecution(
	ctx context.Context,
	session *incrementalRenderSession,
	component *incrementalComponent,
	prepared []*preparedIncrementalComponent,
) (*incrementalVectorExecution, error) {
	if ctx == nil || session == nil || component == nil || len(prepared) == 0 {
		return nil, errors.New("incremental component vector is incomplete")
	}
	execution := &incrementalVectorExecution{
		session: session, component: component,
		items: make([]incrementalVectorItemState, len(prepared)),
	}
	execution.active.Store(-1)
	execution.seal = execution
	queryKeys := make(map[incremental.QueryKey]struct{}, len(prepared))
	for index := range prepared {
		item := prepared[index]
		if err := validateIncrementalVectorItem(session, component, item, index, queryKeys); err != nil {
			return nil, err
		}
		state := &execution.items[index]
		state.prepared = item
		state.recorder.publicationGeneration = session.publicationGeneration
		state.recorder.publicationGroup = component.group
		state.recorder.publicationOwner = incrementalGroupInstanceID{
			component: component.name,
			source:    item.source,
			namespace: item.namespace,
			name:      item.name,
		}
		state.token = &incrementalVectorItemToken{execution: execution, index: index}
		state.token.seal = state.token
		state.lease = &incrementalVectorItemLease{token: state.token}
		state.lease.seal = state.lease
		state.http = &incrementalHTTPFetcher{
			session: session, reader: item.reader, effects: map[uint64]incrementalHTTPEffect{},
		}
		if component.backendPlan {
			state.recorder.plan = newIncrementalBackendPlanRecorder()
		}
		if component.deriveResource {
			deriver, err := newIncrementalResourceDeriver(
				item.source, item.namespace, item.name, item.itemBytes,
			)
			if err != nil {
				return nil, fmt.Errorf("preparing incremental component %q derivation: %w", component.name, err)
			}
			state.recorder.deriver = deriver
			state.derived = deriver.view
		}
	}
	execution.ctx = ctx
	return execution, nil
}

func incrementalVectorContextOptions(
	execution *incrementalVectorExecution,
	component *incrementalComponent,
	state *incrementalVectorItemState,
	index int,
	transitionTime string,
) templating.IncrementalComponentContextOptions {
	options := templating.IncrementalComponentContextOptions{
		ExecutionLease:  state.lease,
		ContextValueKey: incrementalVectorExecutionContextKey{},
		ContextValue:    state.token,
	}
	if component.deriveResource {
		options.ResourceDeriver = &incrementalVectorDeriver{execution: execution, index: index}
	}
	if component.recordEvent {
		options.EventRecorder = &incrementalVectorEventRecorder{execution: execution, index: index}
	}
	if component.statusPatch {
		options.StatusRecorder = &incrementalVectorStatusRecorder{execution: execution, index: index}
		options.TransitionTime = transitionTime
	}
	return options
}

func incrementalVectorRenderMode(
	component *incrementalComponent,
	renderSubject map[string]any,
	index int,
) (string, error) {
	renderMode, ok := renderSubject["mode"].(string)
	if !ok || (renderMode != "reconcile" && renderMode != "admission") {
		return "", fmt.Errorf(
			"incremental component %q vector item %d has invalid renderSubject.mode",
			component.name,
			index,
		)
	}
	return renderMode, nil
}

func (r *incrementalRenderSession) prepareComponentVectorRender(
	ctx context.Context,
	component *incrementalComponent,
	prepared []*preparedIncrementalComponent,
) (*preparedIncrementalVectorRender, error) {
	execution, err := newIncrementalVectorExecution(ctx, r, component, prepared)
	if err != nil {
		return nil, err
	}
	transitionTime := ""
	if component.statusPatch {
		var transitionErr error
		transitionTime, transitionErr = r.incrementalTransitionTime(ctx)
		if transitionErr != nil {
			return nil, fmt.Errorf("sampling incremental transition time: %w", transitionErr)
		}
	}
	count := len(prepared)
	contextTable, err := templating.NewIncrementalComponentContextTable(count)
	if err != nil {
		return nil, fmt.Errorf("preparing incremental component %q vector contexts: %w", component.name, err)
	}
	columns := newIncrementalVectorColumns(count)
	boundResources, resourceCertificate, err := r.bindComponentVectorResources(ctx, component, execution)
	if err != nil {
		return nil, err
	}
	binding := &incrementalVectorItemBinding{
		component:           component,
		execution:           execution,
		prepared:            prepared,
		contextTable:        contextTable,
		columns:             columns,
		transitionTime:      transitionTime,
		boundResources:      boundResources,
		resourceCertificate: resourceCertificate,
	}
	for index := range prepared {
		if err := r.bindVectorItem(ctx, binding, index); err != nil {
			return nil, err
		}
	}
	resourcesColumn, err := incrementalVectorConcreteColumn(
		incrementalResourcesContextName, columns.resourceValues,
	)
	if err != nil {
		return nil, fmt.Errorf("preparing incremental component %q vector: %w", component.name, err)
	}
	return &preparedIncrementalVectorRender{
		execution: execution,
		contexts:  columns.contexts,
		fixed:     map[string]any{},
		columns: map[string]any{
			incrementalSourceContextName:        columns.sources,
			incrementalItemContextName:          columns.items,
			incrementalPropsContextName:         columns.props,
			incrementalRenderSubjectContextName: columns.renderSubjects,
			incrementalRenderModeContextName:    columns.renderModes,
			incrementalResourcesContextName:     resourcesColumn,
			incrementalControllerContextName:    columns.controllers,
			incrementalSharedContextName:        columns.sharedValues,
			incrementalHTTPContextName:          columns.httpValues,
			incrementalPlanRegistryContextName:  columns.planValues,
		},
	}, nil
}

type incrementalVectorColumns struct {
	contexts       []context.Context
	sources        []string
	items          []map[string]any
	props          []map[string]any
	renderSubjects []map[string]any
	renderModes    []string
	resourceValues []any
	controllers    []map[string]templating.ResourceStore
	sharedValues   []templating.SharedContributionContext
	httpValues     []templating.HTTPFetcher
	planValues     []templating.IncrementalBackendPlanRegistrar
}

func newIncrementalVectorColumns(count int) *incrementalVectorColumns {
	return &incrementalVectorColumns{
		contexts:       make([]context.Context, count),
		sources:        make([]string, count),
		items:          make([]map[string]any, count),
		props:          make([]map[string]any, count),
		renderSubjects: make([]map[string]any, count),
		renderModes:    make([]string, count),
		resourceValues: make([]any, count),
		controllers:    make([]map[string]templating.ResourceStore, count),
		sharedValues:   make([]templating.SharedContributionContext, count),
		httpValues:     make([]templating.HTTPFetcher, count),
		planValues:     make([]templating.IncrementalBackendPlanRegistrar, count),
	}
}

type incrementalVectorItemBinding struct {
	component           *incrementalComponent
	execution           *incrementalVectorExecution
	prepared            []*preparedIncrementalComponent
	contextTable        *templating.IncrementalComponentContextTable
	columns             *incrementalVectorColumns
	transitionTime      string
	boundResources      any
	resourceCertificate *templating.IncrementalImmutableCertificate
}

func (r *incrementalRenderSession) bindVectorItem(
	ctx context.Context,
	binding *incrementalVectorItemBinding,
	index int,
) error {
	execution := binding.execution
	component := binding.component
	state := &execution.items[index]
	item := binding.prepared[index]
	options := incrementalVectorContextOptions(execution, component, state, index, binding.transitionTime)
	itemCtx, err := binding.contextTable.Prepare(
		index,
		ctx,
		options,
		item.itemCertificate,
		item.propsCertificate,
		item.subjectCertificate,
		binding.resourceCertificate,
	)
	if err != nil {
		return fmt.Errorf(
			"preparing incremental component %q vector item %d context: %w",
			component.name,
			index,
			err,
		)
	}
	r.bindVectorItemDerivedResources(state, item, itemCtx)
	view := &incrementalVectorResourceView{execution: execution, index: index}
	view.seal = view
	resources := binding.boundResources
	controller := r.incrementalControllerValue(itemCtx, view, false)
	shared := templating.NewLeasedSharedContributionContext(
		itemCtx,
		&incrementalVectorRecorder{execution: execution, index: index},
		&incrementalVectorSelector{execution: execution, index: index},
	)
	httpFetcher := r.vectorItemHTTPFetcher(execution, index)
	planRecorder := vectorItemPlanRecorder(component, execution, index)
	renderMode, err := incrementalVectorRenderMode(component, item.renderSubject, index)
	if err != nil {
		return err
	}
	values := templating.IncrementalComponentContextValues{
		Source: item.source, Item: item.item, Props: item.props,
		RenderSubject: item.renderSubject, RenderMode: renderMode,
		Resources: resources, Controller: controller, Shared: shared,
		HTTP: httpFetcher, PlanRegistry: planRecorder,
	}
	if err := binding.contextTable.SealValues(index, values); err != nil {
		return fmt.Errorf(
			"binding incremental component %q vector item %d immutable inputs: %w",
			component.name,
			index,
			err,
		)
	}
	state.ctx = itemCtx
	columns := binding.columns
	columns.contexts[index] = itemCtx
	columns.sources[index] = item.source
	columns.items[index] = item.item
	columns.props[index] = item.props
	columns.renderSubjects[index] = item.renderSubject
	columns.renderModes[index] = renderMode
	columns.resourceValues[index] = resources
	columns.controllers[index] = controller
	columns.sharedValues[index] = shared
	columns.httpValues[index] = httpFetcher
	columns.planValues[index] = planRecorder
	return nil
}

func (r *incrementalRenderSession) bindComponentVectorResources(
	ctx context.Context,
	component *incrementalComponent,
	execution *incrementalVectorExecution,
) (any, *templating.IncrementalImmutableCertificate, error) {
	sharedResourceView := &incrementalVectorResourceView{execution: execution, index: -1}
	sharedResourceView.seal = sharedResourceView
	sharedResources := r.state.incrementalResourcesValue(
		ctx,
		r.stores,
		r.resourceErrors,
		sharedResourceView,
		nil,
		r.loggerContext,
	)
	var boundResources any
	var err error
	if binder, available := r.state.engine.(templating.IncrementalResourceBinder); available {
		boundResources, err = binder.BindIncrementalResources(component.entryPoint, sharedResources, execution)
	} else {
		boundResources, err = templating.BindAllIncrementalResources(sharedResources, execution)
	}
	if err != nil {
		return nil, nil, fmt.Errorf(
			"binding incremental component %q vector resource capability: %w",
			component.name,
			err,
		)
	}
	resourceCertificate := templating.CertifyIncrementalImmutableInputs(boundResources)
	if resourceCertificate == nil || !resourceCertificate.Guards(boundResources) {
		return nil, nil, fmt.Errorf(
			"certifying incremental component %q vector resource capability",
			component.name,
		)
	}
	return boundResources, resourceCertificate, nil
}

func (r *incrementalRenderSession) bindVectorItemDerivedResources(
	state *incrementalVectorItemState,
	item *preparedIncrementalComponent,
	itemCtx context.Context,
) {
	if state.recorder.deriver == nil {
		state.derivedResolver = &incrementalQueryDerivedResourceResolver{
			ctx: itemCtx, reader: item.reader, session: r,
		}
		return
	}
	state.derived = state.recorder.deriver.view
}

func (r *incrementalRenderSession) vectorItemHTTPFetcher(
	execution *incrementalVectorExecution,
	index int,
) templating.HTTPFetcher {
	if r.httpWrapper == nil {
		return nil
	}
	return &incrementalVectorHTTPFetcher{execution: execution, index: index}
}

func vectorItemPlanRecorder(
	component *incrementalComponent,
	execution *incrementalVectorExecution,
	index int,
) templating.IncrementalBackendPlanRegistrar {
	if !component.backendPlan {
		return nil
	}
	return &incrementalVectorBackendPlanRecorder{execution: execution, index: index}
}

func incrementalVectorConcreteColumn(name string, values []any) (any, error) {
	if name == "" || len(values) == 0 || values[0] == nil {
		return nil, fmt.Errorf("vector binding %q has no concrete value type", name)
	}
	elementType := reflect.TypeOf(values[0])
	column := reflect.MakeSlice(reflect.SliceOf(elementType), len(values), len(values))
	for index, value := range values {
		if value == nil || reflect.TypeOf(value) != elementType {
			return nil, fmt.Errorf("vector binding %q item %d has type %T, want %s", name, index, value, elementType)
		}
		column.Index(index).Set(reflect.ValueOf(value))
	}
	return column.Interface(), nil
}

func (r *incrementalRenderSession) finishComponentVectorRender(
	vector *preparedIncrementalVectorRender,
) ([]string, error) {
	encoded, finalized, err := r.finalizeComponentVectorRender(vector)
	if err != nil {
		return nil, err
	}
	if err := r.installFinalizedComponents(finalized...); err != nil {
		return nil, fmt.Errorf("installing incremental component vector results: %w", err)
	}
	return encoded, nil
}

func (r *incrementalRenderSession) finalizeComponentVectorRender(
	vector *preparedIncrementalVectorRender,
) ([]string, []*finalizedIncrementalComponent, error) {
	if vector == nil || vector.execution == nil {
		return nil, nil, errors.New("incremental component vector result is unavailable")
	}
	if err := vector.execution.finish(); err != nil {
		return nil, nil, err
	}
	encoded := make([]string, len(vector.execution.items))
	finalized := make([]*finalizedIncrementalComponent, len(vector.execution.items))
	for index := range vector.execution.items {
		state := &vector.execution.items[index]
		prepared := state.prepared
		prepared.recorder = &state.recorder
		prepared.httpFetcher = state.http
		value, err := r.finalizePreparedComponent(prepared, state.output)
		if err != nil {
			return nil, nil, fmt.Errorf(
				"finishing incremental component vector item %d: %w",
				index,
				err,
			)
		}
		finalized[index] = value
		encoded[index] = value.encoded
		state.output = ""
	}
	return encoded, finalized, nil
}

func (r *incrementalRenderSession) finalizeComponentVectorRenderIntoArena(
	vector *preparedIncrementalVectorRender,
	arena *incrementalColdResultArena,
	slots []int,
) error {
	if vector == nil || vector.execution == nil || arena == nil ||
		len(slots) != len(vector.execution.items) {
		return errors.New("incremental component vector arena result is unavailable")
	}
	if err := vector.execution.finish(); err != nil {
		return err
	}
	for index := range vector.execution.items {
		state := &vector.execution.items[index]
		prepared := state.prepared
		prepared.recorder = &state.recorder
		prepared.httpFetcher = state.http
		if err := r.finalizePreparedComponentIntoArena(
			prepared,
			state.output,
			arena,
			slots[index],
		); err != nil {
			return fmt.Errorf("finishing incremental component vector item %d: %w", index, err)
		}
		state.output = ""
	}
	return nil
}

func (e *incrementalVectorExecution) valid() bool {
	return e != nil && e.seal == e && e.session != nil && e.component != nil && e.ctx != nil
}

func (e *incrementalVectorExecution) Begin(index int) error {
	if !e.valid() {
		return errors.New("incremental component vector has invalid provenance")
	}
	e.callGate.Lock()
	defer e.callGate.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.aborted || e.finalized || e.failed.Load() || e.terminal != nil {
		return errors.New("incremental component vector is terminal")
	}
	if cause := context.Cause(e.ctx); cause != nil {
		return e.failLocked(cause)
	}
	if active := e.active.Load(); active >= 0 {
		return e.failLocked(fmt.Errorf("incremental component vector item %d is already active", active))
	}
	if index < 0 || index >= len(e.items) {
		return e.failLocked(fmt.Errorf("incremental component vector item %d is invalid", index))
	}
	item := &e.items[index]
	if item.completed || item.beginCount != 0 {
		return e.failLocked(fmt.Errorf("incremental component vector item %d was already executed", index))
	}
	item.beginCount++
	e.active.Store(int64(index))
	return nil
}

func (e *incrementalVectorExecution) End(index int, output string) error {
	if !e.valid() {
		return errors.New("incremental component vector has invalid provenance")
	}
	e.callGate.Lock()
	defer e.callGate.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.aborted || e.finalized || e.failed.Load() || e.terminal != nil {
		return errors.New("incremental component vector is terminal")
	}
	if index < 0 || index >= len(e.items) || e.active.Load() != int64(index) {
		return e.failLocked(fmt.Errorf("incremental component vector item %d is not active", index))
	}
	if cause := context.Cause(e.ctx); cause != nil {
		e.active.Store(-1)
		return e.failLocked(cause)
	}
	e.active.Store(-1)
	if calls := e.inflight.Load(); calls != 0 {
		return e.failLocked(fmt.Errorf(
			"incremental component vector item %d retained %d capability calls",
			index,
			calls,
		))
	}
	e.items[index].output = output
	e.items[index].outputSet = true
	e.items[index].completed = true
	return nil
}

func (e *incrementalVectorExecution) Abort(activeIndex int, cause error) {
	if !e.valid() {
		return
	}
	e.callGate.Lock()
	defer e.callGate.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.aborted {
		return
	}
	e.aborted = true
	if activeIndex >= 0 && e.active.Load() != int64(activeIndex) && cause == nil {
		cause = fmt.Errorf("incremental component vector abort item %d is not active", activeIndex)
	}
	if cause == nil {
		cause = errors.New("incremental component vector aborted")
	}
	_ = e.failLocked(cause)
	e.active.Store(-1)
}

func (e *incrementalVectorExecution) BeginIncrementalExecution(
	ctx context.Context,
	operation string,
) (func(), error) {
	if !e.valid() || ctx == nil {
		return nil, e.recordViolation(fmt.Errorf("%s has an invalid incremental component vector", operation))
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if !token.valid(e) {
		return nil, e.recordViolation(fmt.Errorf("%s has an invalid incremental component vector item", operation))
	}
	return e.enter(ctx, token.index, operation)
}

func (e *incrementalVectorExecution) BeforeIncrementalNativeCall(ctx context.Context) error {
	if !e.valid() || ctx == nil {
		return e.recordViolation(errors.New("native call has an invalid incremental component vector"))
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if !token.valid(e) {
		return e.recordViolation(errors.New("native call has an invalid incremental component vector item"))
	}
	return e.validateActiveContext(ctx, token.index, "native call")
}

func (e *incrementalVectorExecution) ValidateIncrementalResourceInvocation(ctx context.Context) error {
	release, err := e.BeginIncrementalExecution(ctx, "resource capability")
	if err != nil {
		return err
	}
	release()
	return nil
}

func (e *incrementalVectorExecution) ValidateIncrementalSourceTransactionSelector(
	selector templating.IncrementalSourceTransactionChildSelector,
) error {
	if !e.valid() {
		return errors.New("incremental source transaction selector authority has invalid provenance")
	}
	e.callGate.RLock()
	defer e.callGate.RUnlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.aborted || e.finalized || e.failed.Load() || e.terminal != nil {
		return errors.New("incremental source transaction selector authority is terminal")
	}
	selected, ok := selector.(*incrementalVectorExecution)
	if !ok || selected != e {
		return e.failLocked(errors.New("incremental source transaction child selector has different authority"))
	}
	return nil
}

func (e *incrementalVectorExecution) current(index int) (func(), error) {
	if !e.valid() {
		err := errors.New("incremental component vector has invalid provenance")
		return nil, e.recordViolation(err)
	}
	if index < 0 || index >= len(e.items) {
		err := fmt.Errorf("incremental component vector item %d is invalid", index)
		return nil, e.recordViolation(err)
	}
	return e.enter(e.items[index].ctx, index, "component capability")
}

func (e *incrementalVectorExecution) enter(
	ctx context.Context,
	index int,
	operation string,
) (func(), error) {
	e.callGate.RLock()
	if !e.valid() || e.failed.Load() || index < 0 || index >= len(e.items) ||
		e.active.Load() != int64(index) {
		e.callGate.RUnlock()
		return nil, e.recordViolation(fmt.Errorf(
			"%s used inactive incremental component vector item %d",
			operation,
			index,
		))
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if !token.valid(e) || token.index != index {
		e.callGate.RUnlock()
		return nil, e.recordViolation(fmt.Errorf(
			"%s crossed incremental component vector item %d",
			operation,
			index,
		))
	}
	if cause := context.Cause(ctx); cause != nil {
		e.callGate.RUnlock()
		return nil, e.recordViolation(cause)
	}
	e.inflight.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			e.inflight.Add(-1)
			e.callGate.RUnlock()
		})
	}, nil
}

func (e *incrementalVectorExecution) validateActiveContext(
	ctx context.Context,
	index int,
	operation string,
) error {
	if !e.valid() || ctx == nil || e.failed.Load() || index < 0 || index >= len(e.items) ||
		e.active.Load() != int64(index) {
		return e.recordViolation(fmt.Errorf(
			"%s used inactive incremental component vector item %d",
			operation,
			index,
		))
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if !token.valid(e) || token.index != index {
		return e.recordViolation(fmt.Errorf(
			"%s crossed incremental component vector item %d",
			operation,
			index,
		))
	}
	if cause := context.Cause(ctx); cause != nil {
		return e.recordViolation(cause)
	}
	return nil
}

func (e *incrementalVectorExecution) recordViolation(err error) error {
	if err == nil {
		return nil
	}
	if e != nil {
		e.mu.Lock()
		terminal := e.failLocked(err)
		e.mu.Unlock()
		return terminal
	}
	return err
}

func (e *incrementalVectorExecution) finish() error {
	if !e.valid() {
		return errors.New("incremental component vector returned an invalid output set")
	}
	e.callGate.Lock()
	defer e.callGate.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finalized {
		return errors.New("incremental component vector was already finalized")
	}
	if cause := context.Cause(e.ctx); cause != nil {
		return e.failLocked(cause)
	}
	if e.aborted || e.failed.Load() || e.terminal != nil || e.active.Load() >= 0 || e.inflight.Load() != 0 {
		return e.failLocked(errors.New("incremental component vector did not complete"))
	}
	for index := range e.items {
		if !e.items[index].completed || !e.items[index].outputSet || e.items[index].beginCount != 1 {
			return e.failLocked(fmt.Errorf(
				"incremental component vector item %d did not complete exactly once",
				index,
			))
		}
	}
	e.finalized = true
	return nil
}

func (e *incrementalVectorExecution) failLocked(err error) error {
	if err == nil {
		err = errors.New("incremental component vector failed")
	}
	e.failed.Store(true)
	first := e.terminal == nil
	if first {
		e.terminal = err
	}
	if first && e.session != nil && e.session.resourceErrors != nil {
		e.session.resourceErrors.Record(e.terminal)
	}
	return e.terminal
}

func (t *incrementalVectorItemToken) valid(execution *incrementalVectorExecution) bool {
	return t != nil && t.seal == t && t.execution == execution && execution != nil &&
		t.index >= 0 && t.index < len(execution.items) && execution.items[t.index].token == t
}

func (l *incrementalVectorItemLease) valid() bool {
	return l != nil && l.seal == l && l.token != nil && l.token.valid(l.token.execution)
}

func (l *incrementalVectorItemLease) BeginIncrementalExecution(
	ctx context.Context,
	operation string,
) (func(), error) {
	if !l.valid() {
		return nil, errors.New("incremental component vector item lease has invalid provenance")
	}
	if ctx == nil {
		return nil, l.token.execution.recordViolation(fmt.Errorf(
			"%s has a nil incremental component vector context",
			operation,
		))
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if token != l.token {
		return nil, l.token.execution.recordViolation(fmt.Errorf(
			"%s crossed incremental component vector item %d",
			operation,
			l.token.index,
		))
	}
	return l.token.execution.enter(ctx, l.token.index, operation)
}

func (l *incrementalVectorItemLease) BeforeIncrementalNativeCall(ctx context.Context) error {
	if !l.valid() {
		return errors.New("incremental component vector item lease has invalid provenance")
	}
	return l.token.execution.validateActiveContext(ctx, l.token.index, "native call")
}

func (l *incrementalVectorItemLease) ValidateIncrementalResourceInvocation(ctx context.Context) error {
	release, err := l.BeginIncrementalExecution(ctx, "resource capability")
	if err != nil {
		return err
	}
	release()
	return nil
}

func (v *incrementalVectorResourceView) NormalizeLookupKeys(_ string, keys []any) ([]string, error) {
	return templating.CanonicalIncrementalResourceKeys(keys...)
}

func (v *incrementalVectorResourceView) BeginStoreInvocation(
	ctx context.Context,
) (context.Context, func(), error) {
	if v == nil || v.seal != v || v.execution == nil {
		return nil, nil, errors.New("incremental component vector resource view has invalid provenance")
	}
	if ctx == nil {
		return nil, nil, v.execution.recordViolation(errors.New(
			"resource capability has a nil incremental component vector context",
		))
	}
	index, token, err := v.resolveContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	release, err := v.execution.enter(ctx, index, "resource capability")
	if err != nil {
		return nil, nil, err
	}
	invocation := &incrementalVectorStoreInvocation{view: v, token: token, index: index}
	invocation.seal = invocation
	invocation.active.Store(true)
	invocationCtx := context.WithValue(ctx, incrementalVectorStoreInvocationContextKey{}, invocation)
	return invocationCtx, func() {
		if invocation.active.CompareAndSwap(true, false) {
			release()
		}
	}, nil
}

func (v *incrementalVectorResourceView) BeginBoundStoreInvocation(
	ctx context.Context,
	lease templating.IncrementalResourceInvocationLease,
) (context.Context, func(), error) {
	if v == nil || v.seal != v || v.execution == nil {
		return nil, nil, errors.New("incremental component vector resource view has invalid provenance")
	}
	execution, ok := lease.(*incrementalVectorExecution)
	if !ok || execution != v.execution {
		return nil, nil, v.execution.recordViolation(errors.New(
			"incremental component vector resource view has no matching execution lease",
		))
	}
	return v.BeginStoreInvocation(ctx)
}

func (*incrementalVectorResourceView) MemoizeStoreMaterialization() bool {
	return false
}

func (*incrementalVectorResourceView) MemoizeStoreItems() bool {
	return true
}

func (*incrementalVectorResourceView) PreserveStoreValues() bool {
	return true
}

func (v *incrementalVectorResourceView) ResourceItemCache() *rendercontext.ResourceItemCache {
	if v == nil || v.seal != v || v.execution == nil || v.execution.session == nil {
		return nil
	}
	return v.execution.session.resourceItemCache
}

func (v *incrementalVectorResourceView) List(resourceType string, _ stores.Store) ([]any, error) {
	return v.read(&resourceInputSpec{resourceType: resourceType, scope: resourceInputList})
}

func (v *incrementalVectorResourceView) Get(
	resourceType string,
	_ stores.Store,
	keys ...string,
) ([]any, error) {
	return v.read(&resourceInputSpec{
		resourceType: resourceType, scope: resourceInputGet, keys: slices.Clone(keys),
	})
}

func (v *incrementalVectorResourceView) ListContext(
	ctx context.Context,
	resourceType string,
	_ stores.Store,
) ([]any, error) {
	return v.readContext(ctx, &resourceInputSpec{resourceType: resourceType, scope: resourceInputList})
}

func (v *incrementalVectorResourceView) GetContext(
	ctx context.Context,
	resourceType string,
	_ stores.Store,
	keys ...string,
) ([]any, error) {
	return v.readContext(ctx, &resourceInputSpec{
		resourceType: resourceType, scope: resourceInputGet, keys: slices.Clone(keys),
	})
}

func (v *incrementalVectorResourceView) read(spec *resourceInputSpec) ([]any, error) {
	if v == nil || v.seal != v || v.execution == nil {
		return nil, errors.New("incremental component vector resource view has invalid provenance")
	}
	if v.index < 0 {
		return nil, v.execution.recordViolation(errors.New(
			"shared incremental component vector resource view requires an execution context",
		))
	}
	item, err := v.execution.enterDirect(v.index, "resource capability")
	if err != nil {
		return nil, err
	}
	defer v.execution.leaveDirect()
	return v.readActive(item, spec)
}

func (v *incrementalVectorResourceView) readContext(
	ctx context.Context,
	spec *resourceInputSpec,
) ([]any, error) {
	if v == nil || v.seal != v || v.execution == nil {
		return nil, errors.New("incremental component vector resource view has invalid provenance")
	}
	if ctx == nil {
		return nil, v.execution.recordViolation(errors.New(
			"resource capability has a nil incremental component vector context",
		))
	}
	invocation, _ := ctx.Value(incrementalVectorStoreInvocationContextKey{}).(*incrementalVectorStoreInvocation)
	if invocation == nil || invocation.seal != invocation || invocation.view != v || !invocation.active.Load() {
		index, _, err := v.resolveContext(ctx)
		if err != nil {
			return nil, err
		}
		release, err := v.execution.enter(ctx, index, "resource capability")
		if err != nil {
			return nil, err
		}
		defer release()
		return v.readActive(&v.execution.items[index], spec)
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if invocation.token != token || !token.valid(v.execution) || invocation.index != token.index ||
		(v.index >= 0 && v.index != token.index) ||
		v.execution.failed.Load() || v.execution.active.Load() != int64(invocation.index) {
		return nil, v.execution.recordViolation(errors.New(
			"resource capability crossed an incremental component vector boundary",
		))
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, v.execution.recordViolation(cause)
	}
	return v.readActive(&v.execution.items[invocation.index], spec)
}

func (v *incrementalVectorResourceView) resolveContext(
	ctx context.Context,
) (int, *incrementalVectorItemToken, error) {
	if v == nil || v.seal != v || v.execution == nil || ctx == nil {
		return 0, nil, errors.New("incremental component vector resource view has invalid context")
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if !token.valid(v.execution) || (v.index >= 0 && token.index != v.index) {
		return 0, nil, v.execution.recordViolation(errors.New(
			"resource capability crossed an incremental component vector boundary",
		))
	}
	return token.index, token, nil
}

func (v *incrementalVectorResourceView) readActive(
	item *incrementalVectorItemState,
	spec *resourceInputSpec,
) ([]any, error) {
	values, certificate, err := v.execution.session.decodeResourceInput(item.prepared.reader, spec)
	if err != nil {
		return nil, err
	}
	if err := templating.RegisterIncrementalImmutableCertificate(item.ctx, certificate); err != nil {
		return nil, err
	}
	if item.derived != nil {
		return item.derived.Project(spec.resourceType, values)
	}
	if item.derivedResolver == nil {
		return nil, errors.New("incremental component vector has no derived-resource resolver")
	}
	return item.derivedResolver.project(spec.resourceType, values)
}

func (r *incrementalVectorRecorder) current() (*incrementalRecorder, error) {
	item, err := r.execution.enterDirect(r.index, "component capability")
	if err != nil {
		return nil, err
	}
	return &item.recorder, nil
}

func (r *incrementalVectorRecorder) Unique(cell, key, value string) {
	recorder, err := r.current()
	if err != nil {
		return
	}
	defer r.execution.leaveDirect()
	recorder.recordUnique(cell, key, value)
}

func (r *incrementalVectorRecorder) Publish(cell, key string, value any) {
	recorder, err := r.current()
	if err != nil {
		return
	}
	defer r.execution.leaveDirect()
	recorder.publishAfterPreflight(cell, key, "", value, "shared.Publish")
}

func (r *incrementalVectorRecorder) PublishDetached(
	cell, key string,
	value *templating.IncrementalDetachedValue,
) {
	recorder, err := r.current()
	if err != nil {
		return
	}
	defer r.execution.leaveDirect()
	recorder.publishDetachedAfterPreflight(cell, key, "", value, "shared.Publish")
}

func (r *incrementalVectorRecorder) PublishRanked(cell, key, rank string, value any) {
	recorder, err := r.current()
	if err != nil {
		return
	}
	defer r.execution.leaveDirect()
	recorder.publishAfterPreflight(cell, key, rank, value, "shared.PublishRanked")
}

func (r *incrementalVectorRecorder) PublishRankedDetached(
	cell, key, rank string,
	value *templating.IncrementalDetachedValue,
) {
	recorder, err := r.current()
	if err != nil {
		return
	}
	defer r.execution.leaveDirect()
	recorder.publishDetachedAfterPreflight(cell, key, rank, value, "shared.PublishRanked")
}

func (s *incrementalVectorSelector) selector() (*incrementalPublicationSelector, error) {
	item, err := s.execution.enterDirect(s.index, "component capability")
	if err != nil {
		return nil, err
	}
	return &incrementalPublicationSelector{
		ctx: item.ctx, reader: item.prepared.reader,
		session: s.execution.session, component: item.prepared.component,
	}, nil
}

func (s *incrementalVectorSelector) Select(group, cell, key string) (value any, found bool, err error) {
	selector, err := s.selector()
	if err != nil {
		return nil, false, err
	}
	defer s.execution.leaveDirect()
	return selector.selectValue(group, cell, key)
}

func (s *incrementalVectorSelector) SelectValues(group, cell string) ([]any, error) {
	selector, err := s.selector()
	if err != nil {
		return nil, err
	}
	defer s.execution.leaveDirect()
	return selector.selectValues(group, cell)
}

func (s *incrementalVectorSelector) Count(group, cell string) (int, error) {
	selector, err := s.selector()
	if err != nil {
		return 0, err
	}
	defer s.execution.leaveDirect()
	return selector.count(group, cell)
}

func (d *incrementalVectorDeriver) DeriveResource(
	resource string,
	item any,
	path string,
	value any,
) (any, error) {
	state, err := d.execution.enterDirect(d.index, "component capability")
	if err != nil {
		return nil, err
	}
	defer d.execution.leaveDirect()
	if state.recorder.deriver == nil {
		return nil, errors.New("incremental component vector has no resource deriver")
	}
	return state.recorder.deriver.deriveResource(resource, item, path, value)
}

func (r *incrementalVectorEventRecorder) RecordEvent(
	namespace, name, apiVersion, kind, eventType, reason, message string,
) error {
	state, err := r.execution.enterDirect(r.index, "component capability")
	if err != nil {
		return err
	}
	defer r.execution.leaveDirect()
	return state.recorder.recordEvent(namespace, name, apiVersion, kind, eventType, reason, message)
}

func (r *incrementalVectorStatusRecorder) RecordStatusPatch(
	namespace, name, apiVersion, kind, uid, resourceVersion string,
	variants map[string]map[string]any,
	sourceTemplate string,
	sourceLine int,
) error {
	state, err := r.execution.enterDirect(r.index, "component capability")
	if err != nil {
		return err
	}
	defer r.execution.leaveDirect()
	return state.recorder.recordStatusPatch(
		namespace, name, apiVersion, kind, uid, resourceVersion,
		variants, sourceTemplate, sourceLine,
	)
}

func (r *incrementalVectorBackendPlanRecorder) recorder() (*incrementalBackendPlanRecorder, error) {
	state, err := r.execution.enterDirect(r.index, "component capability")
	if err != nil {
		return nil, err
	}
	if state.recorder.plan == nil {
		r.execution.leaveDirect()
		return nil, errors.New("incremental component vector has no backend plan recorder")
	}
	return state.recorder.plan, nil
}

func (r *incrementalVectorBackendPlanRecorder) Profile(record map[string]any) (string, error) {
	recorder, err := r.recorder()
	if err != nil {
		return "", err
	}
	defer r.execution.leaveDirect()
	return recorder.Profile(record)
}

func (r *incrementalVectorBackendPlanRecorder) Backend(record map[string]any, text string) (string, error) {
	recorder, err := r.recorder()
	if err != nil {
		return "", err
	}
	defer r.execution.leaveDirect()
	return recorder.Backend(record, text)
}

func (r *incrementalVectorBackendPlanRecorder) BackendWhenAny(
	record map[string]any,
	text, cell string,
	keys []string,
) (string, error) {
	recorder, err := r.recorder()
	if err != nil {
		return "", err
	}
	defer r.execution.leaveDirect()
	return recorder.BackendWhenAny(record, text, cell, keys)
}

func (f *incrementalVectorHTTPFetcher) Fetch(args ...any) (any, error) {
	state, err := f.execution.enterDirect(f.index, "component capability")
	if err != nil {
		return nil, err
	}
	defer f.execution.leaveDirect()
	return state.http.Fetch(args...)
}
