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
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalSourceTransactionRender struct {
	execution    *incrementalVectorExecution
	batch        templating.IncrementalComponentSourceTransactionBatch
	children     []*preparedIncrementalComponent
	batchIndexes []int
	arenaSlots   []int
}

type incrementalSourceTransactionInvocationContext struct {
	parent       context.Context
	execution    *incrementalVectorExecution
	source       int
	sourceByItem []int
}

type incrementalSourceTransactionRecorder struct {
	execution *incrementalVectorExecution
}

type incrementalSourceTransactionSelector struct {
	execution *incrementalVectorExecution
}

type incrementalSourceTransactionHTTPFetcher struct {
	execution *incrementalVectorExecution
}

type incrementalSourceTransactionBackendPlanRecorder struct {
	execution *incrementalVectorExecution
}

func newIncrementalSourceTransactionExecution(
	ctx context.Context,
	session *incrementalRenderSession,
	prepared []*preparedIncrementalComponent,
	sourceByItem []int,
	sourceCount int,
) (*incrementalVectorExecution, error) {
	if ctx == nil || session == nil || len(prepared) == 0 || len(sourceByItem) != len(prepared) || sourceCount <= 0 {
		return nil, errors.New("incremental source transaction execution is incomplete")
	}
	execution := &incrementalVectorExecution{
		session: session, component: prepared[0].component, ctx: ctx,
		items: make([]incrementalVectorItemState, len(prepared)),
	}
	execution.active.Store(-1)
	execution.seal = execution
	queryKeys := make(map[incremental.QueryKey]struct{}, len(prepared))
	for index, item := range prepared {
		if err := validateSourceTransactionChild(
			session, item, index, sourceByItem, sourceCount, queryKeys,
		); err != nil {
			return nil, err
		}
		if err := initSourceTransactionItemState(execution, session, item, index); err != nil {
			return nil, err
		}
	}
	return execution, nil
}

func validateSourceTransactionChild(
	session *incrementalRenderSession,
	item *preparedIncrementalComponent,
	index int,
	sourceByItem []int,
	sourceCount int,
	queryKeys map[incremental.QueryKey]struct{},
) error {
	if item == nil || item.component == nil || item.reader == nil ||
		sourceByItem[index] < 0 || sourceByItem[index] >= sourceCount ||
		!componentQueryKeyMatches(item.queryKey, item.component, item.source, item.namespace, item.name) {
		return fmt.Errorf("incremental source transaction child %d has invalid provenance", index)
	}
	resolved, source, namespace, name, exists := session.resolveComponentQuery(item.queryKey)
	if !exists || !incrementalComponentsEqual(&resolved, item.component) ||
		source != item.source || namespace != item.namespace || name != item.name {
		return fmt.Errorf("incremental source transaction child %d query has invalid provenance", index)
	}
	if _, duplicate := queryKeys[item.queryKey]; duplicate {
		return fmt.Errorf("incremental source transaction child %d duplicates a query", index)
	}
	queryKeys[item.queryKey] = struct{}{}
	if item.itemCertificate == nil || !item.itemCertificate.Guards(item.item) ||
		item.propsCertificate == nil || !item.propsCertificate.Guards(item.props) ||
		item.subjectCertificate == nil || !item.subjectCertificate.Guards(item.renderSubject) {
		return fmt.Errorf("incremental source transaction child %d has invalid immutable provenance", index)
	}
	return nil
}

func initSourceTransactionItemState(
	execution *incrementalVectorExecution,
	session *incrementalRenderSession,
	item *preparedIncrementalComponent,
	index int,
) error {
	component := item.component
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
		deriver, err := newIncrementalResourceDeriver(item.source, item.namespace, item.name, item.itemBytes)
		if err != nil {
			return fmt.Errorf(
				"preparing incremental source transaction child %q derivation: %w", component.name, err,
			)
		}
		state.recorder.deriver = deriver
		state.derived = deriver.view
	}
	return nil
}

type incrementalSourceTransactionColumns struct {
	contexts           []context.Context
	childContextValues []context.Context
	sources            []string
	items              []map[string]any
	props              []map[string]any
	renderSubjects     []map[string]any
	renderModes        []string
	controllers        []map[string]templating.ResourceStore
	sharedValues       []templating.SharedContributionContext
	httpValues         []templating.HTTPFetcher
	planValues         []templating.IncrementalBackendPlanRegistrar
}

func newIncrementalSourceTransactionColumns(rows, children int) *incrementalSourceTransactionColumns {
	return &incrementalSourceTransactionColumns{
		contexts:           make([]context.Context, rows),
		childContextValues: make([]context.Context, children),
		sources:            make([]string, rows),
		items:              make([]map[string]any, rows),
		props:              make([]map[string]any, rows),
		renderSubjects:     make([]map[string]any, rows),
		renderModes:        make([]string, rows),
		controllers:        make([]map[string]templating.ResourceStore, rows),
		sharedValues:       make([]templating.SharedContributionContext, rows),
		httpValues:         make([]templating.HTTPFetcher, rows),
		planValues:         make([]templating.IncrementalBackendPlanRegistrar, rows),
	}
}

type incrementalSourceTransactionRowPlan struct {
	ctx                 context.Context
	execution           *incrementalVectorExecution
	children            []*preparedIncrementalComponent
	groups              []incrementalColdSourceTransactionGroup
	sourceByItem        []int
	boundResources      any
	resourceCertificate *templating.IncrementalImmutableCertificate
	rowContexts         *templating.IncrementalComponentContextTable
	childContexts       *templating.IncrementalComponentContextTable
	transitionTime      string
	columns             *incrementalSourceTransactionColumns
}

func (r *incrementalRenderSession) prepareSourceTransactionRender(
	ctx context.Context,
	children []*preparedIncrementalComponent,
	batchIndexes []int,
	arenaSlots []int,
	groups []incrementalColdSourceTransactionGroup,
) (*incrementalSourceTransactionRender, error) {
	if ctx == nil || len(children) == 0 || len(batchIndexes) != len(children) ||
		len(arenaSlots) != len(children) || len(groups) == 0 {
		return nil, errors.New("incremental source transaction render is incomplete")
	}
	sourceByItem, err := mapIncrementalSourceTransactionChildren(children, groups)
	if err != nil {
		return nil, err
	}
	execution, err := newIncrementalSourceTransactionExecution(ctx, r, children, sourceByItem, len(groups))
	if err != nil {
		return nil, err
	}
	transitionTime, err := r.sourceTransactionTransitionTime(ctx, children)
	if err != nil {
		return nil, err
	}
	rowContexts, err := templating.NewIncrementalComponentContextTable(len(groups))
	if err != nil {
		return nil, err
	}
	childContexts, err := templating.NewIncrementalComponentContextTable(len(children))
	if err != nil {
		return nil, err
	}
	boundResources, resourceCertificate, err := r.bindSourceTransactionResources(ctx, execution, children)
	if err != nil {
		return nil, err
	}
	plan := &incrementalSourceTransactionRowPlan{
		ctx: ctx, execution: execution, children: children, groups: groups,
		sourceByItem: sourceByItem, boundResources: boundResources,
		resourceCertificate: resourceCertificate, rowContexts: rowContexts,
		childContexts: childContexts, transitionTime: transitionTime,
		columns: newIncrementalSourceTransactionColumns(len(groups), len(children)),
	}
	for source := range groups {
		if err := r.prepareSourceTransactionRow(plan, source); err != nil {
			return nil, err
		}
	}
	resourcesColumn, err := incrementalSourceTransactionRepeatedColumn(
		incrementalResourcesContextName, boundResources, len(groups),
	)
	if err != nil {
		return nil, err
	}
	columns := plan.columns
	orderedChildContexts := make([]context.Context, 0, len(columns.childContextValues))
	for source := range groups {
		for _, childIndex := range groups[source].children {
			orderedChildContexts = append(orderedChildContexts, columns.childContextValues[childIndex])
		}
	}
	if len(orderedChildContexts) != len(columns.childContextValues) {
		return nil, errors.New("incremental source transaction child context order is incomplete")
	}
	return &incrementalSourceTransactionRender{
		execution: execution,
		batch: templating.IncrementalComponentSourceTransactionBatch{
			Bindings: map[string]any{
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
			Contexts:      columns.contexts,
			ChildContexts: orderedChildContexts,
		},
		children: children, batchIndexes: slicesCloneInts(batchIndexes), arenaSlots: slicesCloneInts(arenaSlots),
	}, nil
}

func mapIncrementalSourceTransactionChildren(
	children []*preparedIncrementalComponent,
	groups []incrementalColdSourceTransactionGroup,
) ([]int, error) {
	sourceByItem := make([]int, len(children))
	seen := make([]bool, len(children))
	for source := range groups {
		if len(groups[source].children) == 0 {
			return nil, fmt.Errorf("incremental source transaction row %d is empty", source)
		}
		for _, child := range groups[source].children {
			if child < 0 || child >= len(children) || seen[child] {
				return nil, fmt.Errorf("incremental source transaction row %d has invalid child %d", source, child)
			}
			seen[child] = true
			sourceByItem[child] = source
		}
	}
	for child, childSeen := range seen {
		if !childSeen {
			return nil, fmt.Errorf("incremental source transaction omitted child %d", child)
		}
	}
	return sourceByItem, nil
}

func (r *incrementalRenderSession) sourceTransactionTransitionTime(
	ctx context.Context,
	children []*preparedIncrementalComponent,
) (string, error) {
	for _, child := range children {
		if child.component.statusPatch {
			transitionTime, err := r.incrementalTransitionTime(ctx)
			if err != nil {
				return "", fmt.Errorf("sampling incremental source transaction transition time: %w", err)
			}
			return transitionTime, nil
		}
	}
	return "", nil
}

func (r *incrementalRenderSession) bindSourceTransactionResources(
	ctx context.Context,
	execution *incrementalVectorExecution,
	children []*preparedIncrementalComponent,
) (any, *templating.IncrementalImmutableCertificate, error) {
	sharedResourceView := &incrementalVectorResourceView{execution: execution, index: -1}
	sharedResourceView.seal = sharedResourceView
	sharedResources := r.state.incrementalResourcesValue(
		ctx, r.stores, r.resourceErrors, sharedResourceView, nil, r.loggerContext,
	)
	templateNames := make([]string, len(children))
	for child := range children {
		templateNames[child] = children[child].component.entryPoint
	}
	binder, available := r.state.engine.(templating.IncrementalSourceTransactionResourceBinder)
	if !available {
		return nil, nil, errors.New("incremental source transaction resource binder is unavailable")
	}
	boundResources, err := binder.BindIncrementalSourceTransactionResources(
		templateNames, sharedResources, execution, execution,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("binding incremental source transaction resource capability: %w", err)
	}
	resourceCertificate := templating.CertifyIncrementalImmutableInputs(boundResources)
	if resourceCertificate == nil || !resourceCertificate.Guards(boundResources) {
		return nil, nil, errors.New("certifying incremental source transaction resource capability")
	}
	return boundResources, resourceCertificate, nil
}

func (r *incrementalRenderSession) prepareSourceTransactionRow(
	plan *incrementalSourceTransactionRowPlan,
	source int,
) error {
	group := &plan.groups[source]
	representative := plan.children[group.children[0]]
	for _, childIndex := range group.children[1:] {
		if !sameIncrementalSourceTransactionGlobals(representative, plan.children[childIndex]) {
			return fmt.Errorf(
				"%w: row %d children do not share authenticated globals",
				errIncrementalColdSourceTransactionInvariant, source,
			)
		}
	}
	invocationCtx := &incrementalSourceTransactionInvocationContext{
		parent: plan.ctx, execution: plan.execution, source: source, sourceByItem: plan.sourceByItem,
	}
	view := &incrementalVectorResourceView{execution: plan.execution, index: -1}
	view.seal = view
	controller := r.incrementalControllerValue(invocationCtx, view, false)
	shared := templating.NewLeasedSharedContributionContext(
		invocationCtx,
		&incrementalSourceTransactionRecorder{execution: plan.execution},
		&incrementalSourceTransactionSelector{execution: plan.execution},
	)
	var httpFetcher templating.HTTPFetcher
	if r.httpWrapper != nil {
		httpFetcher = &incrementalSourceTransactionHTTPFetcher{execution: plan.execution}
	}
	var planRecorder templating.IncrementalBackendPlanRegistrar
	for _, childIndex := range group.children {
		if plan.children[childIndex].component.backendPlan {
			planRecorder = &incrementalSourceTransactionBackendPlanRecorder{execution: plan.execution}
			break
		}
	}
	renderMode, ok := representative.renderSubject["mode"].(string)
	if !ok || (renderMode != "reconcile" && renderMode != "admission") {
		return fmt.Errorf("incremental source transaction row %d has invalid renderSubject.mode", source)
	}
	values := templating.IncrementalComponentContextValues{
		Source: representative.source, Item: representative.item, Props: representative.props,
		RenderSubject: representative.renderSubject, RenderMode: renderMode,
		Resources: plan.boundResources, Controller: controller, Shared: shared,
		HTTP: httpFetcher, PlanRegistry: planRecorder,
	}
	rowCtx, err := plan.rowContexts.Prepare(
		source, plan.ctx,
		templating.IncrementalComponentContextOptions{ExecutionLease: plan.execution},
		representative.itemCertificate,
		representative.propsCertificate,
		representative.subjectCertificate,
		plan.resourceCertificate,
	)
	if err != nil {
		return fmt.Errorf("preparing incremental source transaction row %d context: %w", source, err)
	}
	if err := plan.rowContexts.SealValues(source, values); err != nil {
		return fmt.Errorf("sealing incremental source transaction row %d: %w", source, err)
	}
	columns := plan.columns
	columns.contexts[source] = rowCtx
	for _, childIndex := range group.children {
		if err := r.prepareSourceTransactionChildContext(plan, childIndex, &values); err != nil {
			return err
		}
	}
	columns.sources[source] = representative.source
	columns.items[source] = representative.item
	columns.props[source] = representative.props
	columns.renderSubjects[source] = representative.renderSubject
	columns.renderModes[source] = renderMode
	columns.controllers[source] = controller
	columns.sharedValues[source] = shared
	columns.httpValues[source] = httpFetcher
	columns.planValues[source] = planRecorder
	return nil
}

func (r *incrementalRenderSession) prepareSourceTransactionChildContext(
	plan *incrementalSourceTransactionRowPlan,
	childIndex int,
	values *templating.IncrementalComponentContextValues,
) error {
	child := plan.children[childIndex]
	state := &plan.execution.items[childIndex]
	options := templating.IncrementalComponentContextOptions{
		ExecutionLease:  state.lease,
		ContextValueKey: incrementalVectorExecutionContextKey{},
		ContextValue:    state.token,
	}
	if child.component.deriveResource {
		options.ResourceDeriver = &incrementalVectorDeriver{execution: plan.execution, index: childIndex}
	}
	if child.component.recordEvent {
		options.EventRecorder = &incrementalVectorEventRecorder{execution: plan.execution, index: childIndex}
	}
	if child.component.statusPatch {
		options.StatusRecorder = &incrementalVectorStatusRecorder{execution: plan.execution, index: childIndex}
		options.TransitionTime = plan.transitionTime
	}
	childCtx, err := plan.childContexts.Prepare(
		childIndex, plan.ctx, options,
		child.itemCertificate,
		child.propsCertificate,
		child.subjectCertificate,
		plan.resourceCertificate,
	)
	if err != nil {
		return fmt.Errorf("preparing incremental source transaction child %d context: %w", childIndex, err)
	}
	if err := plan.childContexts.SealValues(childIndex, *values); err != nil {
		return fmt.Errorf("sealing incremental source transaction child %d: %w", childIndex, err)
	}
	state.ctx = childCtx
	if state.recorder.deriver == nil {
		state.derivedResolver = &incrementalQueryDerivedResourceResolver{
			ctx: childCtx, reader: state.prepared.reader, session: r,
		}
	} else {
		state.derived = state.recorder.deriver.view
	}
	plan.columns.childContextValues[childIndex] = childCtx
	return nil
}

func incrementalSourceTransactionRepeatedColumn(name string, value any, count int) (any, error) {
	if name == "" || value == nil || count <= 0 {
		return nil, fmt.Errorf("source transaction binding %q has no concrete value", name)
	}
	elementType := reflect.TypeOf(value)
	column := reflect.MakeSlice(reflect.SliceOf(elementType), count, count)
	item := reflect.ValueOf(value)
	for index := range count {
		column.Index(index).Set(item)
	}
	return column.Interface(), nil
}

func sameIncrementalSourceTransactionGlobals(left, right *preparedIncrementalComponent) bool {
	if left == nil || right == nil || left.source != right.source || left.namespace != right.namespace ||
		left.name != right.name || left.itemCertificate != right.itemCertificate ||
		left.propsCertificate != right.propsCertificate || left.subjectCertificate != right.subjectCertificate {
		return false
	}
	return sameMapIdentity(left.item, right.item) && sameMapIdentity(left.props, right.props) &&
		sameMapIdentity(left.renderSubject, right.renderSubject) &&
		left.itemCertificate.Guards(left.item) && right.itemCertificate.Guards(right.item) &&
		left.propsCertificate.Guards(left.props) && right.propsCertificate.Guards(right.props) &&
		left.subjectCertificate.Guards(left.renderSubject) && right.subjectCertificate.Guards(right.renderSubject)
}

func sameMapIdentity(left, right map[string]any) bool {
	return left != nil && right != nil && reflect.ValueOf(left).Pointer() == reflect.ValueOf(right).Pointer()
}

func slicesCloneInts(values []int) []int {
	return append([]int(nil), values...)
}

func (r *incrementalRenderSession) finalizeSourceTransactionRenderIntoArena(
	render *incrementalSourceTransactionRender,
	arena *incrementalColdResultArena,
) error {
	if r == nil || render == nil || render.execution == nil || render.execution.session != r ||
		arena == nil || arena.session != r || len(render.children) != len(render.execution.items) ||
		len(render.batchIndexes) != len(render.children) || len(render.arenaSlots) != len(render.children) {
		return errors.New("incremental source transaction result is unavailable")
	}
	if err := render.execution.finish(); err != nil {
		return err
	}
	for child := range render.children {
		state := &render.execution.items[child]
		prepared := state.prepared
		prepared.recorder = &state.recorder
		prepared.httpFetcher = state.http
		if err := stagePreparedComponentResultIntoArena(
			prepared, state.output, arena, render.arenaSlots[child],
		); err != nil {
			return fmt.Errorf("finishing incremental source transaction child %d: %w", child, err)
		}
		state.output = ""
	}
	return nil
}

func (e *incrementalVectorExecution) ActiveIncrementalSourceTransactionChild() (int, error) {
	if !e.valid() || e.failed.Load() {
		return -1, e.recordViolation(errors.New("incremental source transaction has no active child"))
	}
	child := int(e.active.Load())
	if child < 0 || child >= len(e.items) {
		return -1, e.recordViolation(errors.New("incremental source transaction has no active child"))
	}
	return child, nil
}

func (c *incrementalSourceTransactionInvocationContext) Deadline() (time.Time, bool) {
	return c.parent.Deadline()
}

func (c *incrementalSourceTransactionInvocationContext) Done() <-chan struct{} {
	return c.parent.Done()
}
func (c *incrementalSourceTransactionInvocationContext) Err() error { return c.parent.Err() }

func (c *incrementalSourceTransactionInvocationContext) Value(key any) any {
	if c == nil || c.parent == nil || c.execution == nil || c.source < 0 {
		return nil
	}
	child := int(c.execution.active.Load())
	if child >= 0 && child < len(c.execution.items) && child < len(c.sourceByItem) &&
		c.sourceByItem[child] == c.source && c.execution.items[child].ctx != nil {
		return c.execution.items[child].ctx.Value(key)
	}
	return c.parent.Value(key)
}

func (r *incrementalSourceTransactionRecorder) active() (*incrementalVectorRecorder, error) {
	child, err := r.execution.ActiveIncrementalSourceTransactionChild()
	if err != nil {
		return nil, err
	}
	return &incrementalVectorRecorder{execution: r.execution, index: child}, nil
}

func (r *incrementalSourceTransactionRecorder) Unique(cell, key, value string) {
	if recorder, err := r.active(); err == nil {
		recorder.Unique(cell, key, value)
	}
}

func (r *incrementalSourceTransactionRecorder) Publish(cell, key string, value any) {
	if recorder, err := r.active(); err == nil {
		recorder.Publish(cell, key, value)
	}
}

func (r *incrementalSourceTransactionRecorder) PublishDetached(
	cell, key string,
	value *templating.IncrementalDetachedValue,
) {
	if recorder, err := r.active(); err == nil {
		recorder.PublishDetached(cell, key, value)
	}
}

func (r *incrementalSourceTransactionRecorder) PublishRanked(cell, key, rank string, value any) {
	if recorder, err := r.active(); err == nil {
		recorder.PublishRanked(cell, key, rank, value)
	}
}

func (r *incrementalSourceTransactionRecorder) PublishRankedDetached(
	cell, key, rank string,
	value *templating.IncrementalDetachedValue,
) {
	if recorder, err := r.active(); err == nil {
		recorder.PublishRankedDetached(cell, key, rank, value)
	}
}

func (s *incrementalSourceTransactionSelector) selector() (*incrementalVectorSelector, error) {
	child, err := s.execution.ActiveIncrementalSourceTransactionChild()
	if err != nil {
		return nil, err
	}
	return &incrementalVectorSelector{execution: s.execution, index: child}, nil
}

func (s *incrementalSourceTransactionSelector) Select(group, cell, key string) (value any, found bool, err error) {
	selector, err := s.selector()
	if err != nil {
		return nil, false, err
	}
	return selector.Select(group, cell, key)
}

func (s *incrementalSourceTransactionSelector) SelectValues(group, cell string) ([]any, error) {
	selector, err := s.selector()
	if err != nil {
		return nil, err
	}
	return selector.SelectValues(group, cell)
}

func (s *incrementalSourceTransactionSelector) Count(group, cell string) (int, error) {
	selector, err := s.selector()
	if err != nil {
		return 0, err
	}
	return selector.Count(group, cell)
}

func (f *incrementalSourceTransactionHTTPFetcher) Fetch(args ...any) (any, error) {
	child, err := f.execution.ActiveIncrementalSourceTransactionChild()
	if err != nil {
		return nil, err
	}
	return (&incrementalVectorHTTPFetcher{execution: f.execution, index: child}).Fetch(args...)
}

func (r *incrementalSourceTransactionBackendPlanRecorder) recorder() (*incrementalVectorBackendPlanRecorder, error) {
	child, err := r.execution.ActiveIncrementalSourceTransactionChild()
	if err != nil {
		return nil, err
	}
	return &incrementalVectorBackendPlanRecorder{execution: r.execution, index: child}, nil
}

func (r *incrementalSourceTransactionBackendPlanRecorder) Profile(record map[string]any) (string, error) {
	recorder, err := r.recorder()
	if err != nil {
		return "", err
	}
	return recorder.Profile(record)
}

func (r *incrementalSourceTransactionBackendPlanRecorder) Backend(
	record map[string]any,
	text string,
) (string, error) {
	recorder, err := r.recorder()
	if err != nil {
		return "", err
	}
	return recorder.Backend(record, text)
}

func (r *incrementalSourceTransactionBackendPlanRecorder) BackendWhenAny(
	record map[string]any,
	text, cell string,
	keys []string,
) (string, error) {
	recorder, err := r.recorder()
	if err != nil {
		return "", err
	}
	return recorder.BackendWhenAny(record, text, cell, keys)
}

var _ templating.IncrementalSourceTransactionChildSelector = (*incrementalVectorExecution)(nil)
var _ templating.IncrementalResourceInvocationLease = (*incrementalVectorExecution)(nil)
var _ templating.IncrementalSourceTransactionSelectorAuthenticator = (*incrementalVectorExecution)(nil)
var _ rendercontext.StoreSnapshotView = (*incrementalVectorResourceView)(nil)
