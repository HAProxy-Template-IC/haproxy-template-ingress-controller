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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalSourceTransactionTestLifecycle struct {
	*incrementalVectorTestLifecycle
	batch   IncrementalComponentSourceTransactionBatch
	loads   []int
	seals   []int
	abortN  int
	onLoad  func(int)
	onBegin func(int)
}

type incrementalSourceTransactionSelectorTestAuthority struct {
	selector IncrementalSourceTransactionChildSelector
	calls    int
}

type incrementalSourceTransactionSelectorTestValue struct {
	child int
}

type incrementalSourceTransactionSelectorUnauthenticatedLease struct{}

func (*incrementalSourceTransactionSelectorUnauthenticatedLease) ValidateIncrementalResourceInvocation(
	context.Context,
) error {
	return nil
}

func (a *incrementalSourceTransactionSelectorTestAuthority) ValidateIncrementalResourceInvocation(
	context.Context,
) error {
	return nil
}

func (a *incrementalSourceTransactionSelectorTestAuthority) ValidateIncrementalSourceTransactionSelector(
	selector IncrementalSourceTransactionChildSelector,
) error {
	a.calls++
	if selector != a.selector {
		return errors.New("selector has different authority")
	}
	return nil
}

func (s *incrementalSourceTransactionSelectorTestValue) ActiveIncrementalSourceTransactionChild() (int, error) {
	return s.child, nil
}

func (l *incrementalSourceTransactionTestLifecycle) LoadSourceTransactionWave(
	_ context.Context,
	wave int,
) (IncrementalComponentSourceTransactionBatch, error) {
	l.loads = append(l.loads, wave)
	if l.onLoad != nil {
		l.onLoad(wave)
	}
	return l.batch, nil
}

func (l *incrementalSourceTransactionTestLifecycle) SealWave(wave int) error {
	l.seals = append(l.seals, wave)
	return nil
}

func (l *incrementalSourceTransactionTestLifecycle) Begin(index int) error {
	if err := l.incrementalVectorTestLifecycle.Begin(index); err != nil {
		return err
	}
	if l.onBegin != nil {
		l.onBegin(index)
	}
	return nil
}

func (l *incrementalSourceTransactionTestLifecycle) Abort(index int, cause error) {
	l.abortN++
	l.incrementalVectorTestLifecycle.Abort(index, cause)
}

func TestIncrementalComponentSourceTransactionsSharesOneRowAndSealsChildrenIndependently(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	lifecycle := input.Lifecycle.(*incrementalSourceTransactionTestLifecycle)

	require.NoError(t, engine.RenderIncrementalComponentSourceTransactions(t.Context(), input))
	assert.Equal(t, []int{0}, lifecycle.loads)
	assert.Equal(t, []int{0}, lifecycle.seals)
	assert.Equal(t, []int{0, 1}, lifecycle.begins)
	assert.Equal(t, []int{0, 1}, lifecycle.ends)
	assert.Equal(t, []string{"A:test", "B:test"}, lifecycle.outputs)
	assert.Zero(t, lifecycle.abortN)
}

func TestIncrementalSourceTransactionResourceBindingAuthenticatesSelectorWithLease(t *testing.T) {
	engine := &ScriggoEngine{}
	want := &incrementalSourceTransactionSelectorTestValue{child: 0}
	foreign := &incrementalSourceTransactionSelectorTestValue{child: 0}
	authority := &incrementalSourceTransactionSelectorTestAuthority{selector: want}

	_, err := engine.BindIncrementalSourceTransactionResources(
		[]string{"component"}, &struct{}{}, authority, foreign,
	)
	require.ErrorContains(t, err, "selector has different authority")
	assert.Equal(t, 1, authority.calls)

	_, err = engine.BindIncrementalSourceTransactionResources(
		[]string{"component"}, &struct{}{}, &incrementalSourceTransactionSelectorUnauthenticatedLease{}, want,
	)
	require.ErrorContains(t, err, "lease cannot authenticate its child selector")
}

func TestIncrementalSourceTransactionResourceBindingKeepsStaticAuthorityPerChild(t *testing.T) {
	rootType := reflect.TypeFor[*incrementalResourceBindingStaticResources]()
	listPlan, err := newIncrementalResourceBindingPlan(
		rootType,
		map[string]uint8{"Routes": incrementalResourceList},
	)
	require.NoError(t, err)
	staticPlan, err := newIncrementalResourceBindingPlan(
		rootType,
		map[string]uint8{"Routes": incrementalResourceStatic},
	)
	require.NoError(t, err)
	engine := &ScriggoEngine{
		incrementalEntryPoints: map[string]struct{}{"list": {}, "static": {}},
		incrementalResourceBindings: map[string]*incrementalResourceBindingPlan{
			"list": listPlan, "static": staticPlan,
		},
	}

	selector := &incrementalSourceTransactionSelectorTestValue{child: 0}
	authority := &incrementalSourceTransactionSelectorTestAuthority{selector: selector}
	boundValue, err := engine.BindIncrementalSourceTransactionResources(
		[]string{"list", "static"},
		&incrementalResourceBindingStaticResources{Routes: &incrementalResourceBindingStaticStore{
			APIVersion: func() string { return "example.test/v1" },
			List:       func(native.Env) []any { return []any{"route"} },
		}},
		authority,
		selector,
	)
	require.NoError(t, err)
	bound := boundValue.(*incrementalResourceBindingStaticResources)

	require.Panics(t, func() { bound.Routes.APIVersion() })
	selector.child = 1
	assert.Equal(t, "example.test/v1", bound.Routes.APIVersion())
}

func TestIncrementalComponentSourceTransactionsReturnsPanickingChildAsBatchError(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	lifecycle := input.Lifecycle.(*incrementalSourceTransactionTestLifecycle)
	lifecycle.onBegin = func(index int) {
		if index == 1 {
			panic("child panic")
		}
	}

	var err error
	require.NotPanics(t, func() {
		err = engine.RenderIncrementalComponentSourceTransactions(t.Context(), input)
	})
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 1, batchErr.Index)
	assert.ErrorContains(t, batchErr.Err, "child panic")
	assert.Equal(t, []int{0, 1}, lifecycle.begins)
	assert.Equal(t, []int{0}, lifecycle.ends)
	assert.Equal(t, 1, lifecycle.abortN)
}

func TestIncrementalComponentSourceTransactionsReturnsFailingChildAsBatchError(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `{% fail("component failed") %}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	lifecycle := input.Lifecycle.(*incrementalSourceTransactionTestLifecycle)

	err := engine.RenderIncrementalComponentSourceTransactions(t.Context(), input)
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 1, batchErr.Index)
	var renderErr *RenderError
	require.ErrorAs(t, batchErr.Err, &renderErr)
	assert.Equal(t, "b", renderErr.TemplateName)
	assert.ErrorContains(t, renderErr, "component failed")
	assert.Equal(t, []int{0, 1}, lifecycle.begins)
	assert.Equal(t, []int{0}, lifecycle.ends)
	assert.Equal(t, "A:test", lifecycle.outputs[0])
	assert.Empty(t, lifecycle.outputs[1])
	assert.Equal(t, 1, lifecycle.abortN)
	assert.Equal(t, 1, lifecycle.abortIndex)
}

func TestIncrementalComponentSourceTransactionNativeControlExemptionIsExact(t *testing.T) {
	controller := &incrementalSourceTransactionController{}
	ctx := WithIncrementalImmutableInputs(t.Context(), controller)
	receiver := reflect.ValueOf(controller)

	for _, method := range []string{"BeginWave", "EndWave"} {
		require.NoError(t, observeIncrementalVectorNativeCall(ctx, scriggo.NativeCall{
			Receiver: receiver,
			Method:   method,
			Path:     incrementalSourceTransactionTemplatePath,
		}))
	}

	assert.Error(t, observeIncrementalVectorNativeCall(ctx, scriggo.NativeCall{
		Receiver: receiver,
		Method:   "Complete",
		Path:     incrementalSourceTransactionTemplatePath,
	}))
	assert.Error(t, observeIncrementalVectorNativeCall(ctx, scriggo.NativeCall{
		Receiver: receiver,
		Method:   "BeginWave",
		Path:     "component.txt",
	}))
	foreign := &struct {
		controller *incrementalSourceTransactionController
	}{controller: controller}
	assert.Error(t, observeIncrementalVectorNativeCall(ctx, scriggo.NativeCall{
		Receiver: reflect.ValueOf(foreign),
		Method:   "BeginWave",
		Path:     incrementalSourceTransactionTemplatePath,
	}))
}

func TestIncrementalComponentSourceTransactionsRejectsReusedChildContextBeforeExecution(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	lifecycle := input.Lifecycle.(*incrementalSourceTransactionTestLifecycle)
	childCtx := newIncrementalSourceTransactionSealedChildContext(t, lifecycle.batch.ChildContexts[0])
	lifecycle.batch.ChildContexts[0] = childCtx
	lifecycle.batch.ChildContexts[1] = childCtx

	err := engine.RenderIncrementalComponentSourceTransactions(t.Context(), input)
	require.Error(t, err)
	assert.Empty(t, lifecycle.begins)
	assert.Empty(t, lifecycle.ends)
	assert.Equal(t, 1, lifecycle.abortN)
}

func newIncrementalSourceTransactionSealedChildContext(
	tb testing.TB,
	ctx context.Context,
) context.Context {
	tb.Helper()
	renderValues := ctx.Value(RenderContextContextKey).(map[string]any)
	values := IncrementalComponentContextValues{
		Source:        renderValues["source"].(string),
		Item:          renderValues["item"].(map[string]any),
		Props:         renderValues["props"].(map[string]any),
		RenderSubject: renderValues["renderSubject"].(map[string]any),
		RenderMode:    renderValues["renderMode"].(string),
		Resources:     renderValues["resources"],
		Controller:    renderValues["controller"].(map[string]ResourceStore),
		Shared:        renderValues["shared"].(SharedContributionContext),
	}
	table, err := NewIncrementalComponentContextTable(1)
	require.NoError(tb, err)
	sealed, err := table.Prepare(
		0,
		context.Background(),
		IncrementalComponentContextOptions{ExecutionLease: &incrementalComponentContextTestLease{}},
		CertifyIncrementalImmutableInputs(values.Item),
		CertifyIncrementalImmutableInputs(values.Props),
		CertifyIncrementalImmutableInputs(values.RenderSubject),
		CertifyIncrementalImmutableInputs(values.Resources),
	)
	require.NoError(tb, err)
	require.NoError(tb, table.SealValues(0, values))
	return sealed
}

func TestIncrementalComponentSourceTransactionsCancellationPublishesNothing(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	lifecycle := input.Lifecycle.(*incrementalSourceTransactionTestLifecycle)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := engine.RenderIncrementalComponentSourceTransactions(ctx, input)
	require.Error(t, err)
	assert.Empty(t, lifecycle.begins)
	assert.Empty(t, lifecycle.ends)
	assert.Equal(t, 1, lifecycle.abortN)
}

func TestIncrementalComponentSourceTransactionsDeepOwnsNestedTopology(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	lifecycle := input.Lifecycle.(*incrementalSourceTransactionTestLifecycle)
	lifecycle.onLoad = func(int) {
		input.Waves[0].Transactions[0].Children[0] = IncrementalComponentSourceTransactionChild{
			TemplateName: "b",
			Index:        1,
		}
		input.Waves[0].Transactions = append(
			input.Waves[0].Transactions,
			IncrementalComponentSourceTransaction{Children: []IncrementalComponentSourceTransactionChild{{
				TemplateName: "a",
				Index:        0,
			}}},
		)
	}

	require.NoError(t, engine.RenderIncrementalComponentSourceTransactions(t.Context(), input))
	assert.Equal(t, []int{0, 1}, lifecycle.begins)
	assert.Equal(t, []string{"A:test", "B:test"}, lifecycle.outputs)
}

func TestIncrementalSourceTransactionTopologyRejectsMutationAndABA(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	prepared, err := prepareIncrementalSourceTransactionsInput(
		t.Context(),
		engine.incrementalVectorCarrier,
		input,
	)
	require.NoError(t, err)
	require.NoError(t, prepared.authenticateTopology())

	original := prepared.shapes[0].Transactions[0].Children[0]
	prepared.shapes[0].Transactions[0].Children[0].Index = 1
	require.ErrorContains(t, prepared.authenticateTopology(), "invalid provenance")
	prepared.shapes[0].Transactions[0].Children[0] = original
	require.ErrorContains(t, prepared.authenticateTopology(), "revoked")
}

func TestIncrementalSourceTransactionTopologyRejectsMalformedIndexes(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		index int
	}{
		{name: "negative", index: -1},
		{name: "duplicate", index: 0},
		{name: "sparse", index: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
				"a": `A:{{ source }}`,
				"b": `B:{{ source }}`,
			})
			input := newIncrementalSourceTransactionTestInput(t, engine)
			input.Waves[0].Transactions[0].Children[1].Index = testCase.index

			_, err := prepareIncrementalSourceTransactionsInput(
				t.Context(),
				engine.incrementalVectorCarrier,
				input,
			)
			require.Error(t, err)
		})
	}
}

func TestIncrementalSourceTransactionTopologyHasNoCallerAliases(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	input := newIncrementalSourceTransactionTestInput(t, engine)
	prepared, err := prepareIncrementalSourceTransactionsInput(
		t.Context(),
		engine.incrementalVectorCarrier,
		input,
	)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for iteration := range 1000 {
			input.Waves[0].Transactions[0].Children[0].Index = iteration & 1
			input.Waves[0].Transactions[0].Children[0].TemplateName = []string{"a", "b"}[iteration&1]
		}
	}()
	for range 1000 {
		require.NoError(t, prepared.authenticateTopology())
	}
	<-done
	assert.Equal(t, "a", prepared.shapes[0].Transactions[0].Children[0].TemplateName)
	assert.Zero(t, prepared.shapes[0].Transactions[0].Children[0].Index)
}

func newIncrementalSourceTransactionTestInput(
	tb testing.TB,
	engine *ScriggoEngine,
) IncrementalComponentSourceTransactionsInput {
	tb.Helper()
	laneA := newIncrementalVectorCarrierTestLane(tb, engine, "a", 1, nil)
	laneB := newIncrementalVectorCarrierTestLane(tb, engine, "b", 1, nil)
	columns := make(map[string]any, len(laneA.Bindings))
	for name, laneColumn := range laneA.Bindings {
		columnValue := reflect.ValueOf(laneColumn)
		column := reflect.MakeSlice(columnValue.Type(), 1, 1)
		column.Index(0).Set(columnValue.Index(0))
		columns[name] = column.Interface()
	}
	lifecycle := &incrementalSourceTransactionTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(2),
		batch: IncrementalComponentSourceTransactionBatch{
			Bindings: columns,
			Contexts: []context.Context{laneA.Contexts[0]},
			ChildContexts: []context.Context{
				laneA.Contexts[0],
				laneB.Contexts[0],
			},
		},
	}
	return IncrementalComponentSourceTransactionsInput{
		Waves: []IncrementalComponentSourceTransactionWave{{
			Transactions: []IncrementalComponentSourceTransaction{{
				Children: []IncrementalComponentSourceTransactionChild{
					{TemplateName: "a", Index: 0},
					{TemplateName: "b", Index: 1},
				},
			}},
		}},
		Lifecycle: lifecycle,
	}
}
