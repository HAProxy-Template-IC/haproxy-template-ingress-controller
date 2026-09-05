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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"
)

type incrementalVectorCarrierWavesTestLifecycle struct {
	*incrementalVectorTestLifecycle
	waves   [][]IncrementalComponentVectorCarrierLane
	loads   []int
	seals   []int
	loadErr map[int]error
	sealErr map[int]error
	onBegin func(int)
	abortN  int
}

func (lifecycle *incrementalVectorCarrierWavesTestLifecycle) Begin(index int) error {
	if err := lifecycle.incrementalVectorTestLifecycle.Begin(index); err != nil {
		return err
	}
	if lifecycle.onBegin != nil {
		lifecycle.onBegin(index)
	}
	return nil
}

func (lifecycle *incrementalVectorCarrierWavesTestLifecycle) LoadWave(
	_ context.Context,
	wave int,
) ([]IncrementalComponentVectorCarrierLane, error) {
	lifecycle.loads = append(lifecycle.loads, wave)
	if err := lifecycle.loadErr[wave]; err != nil {
		return nil, err
	}
	return slices.Clone(lifecycle.waves[wave]), nil
}

func (lifecycle *incrementalVectorCarrierWavesTestLifecycle) SealWave(wave int) error {
	lifecycle.seals = append(lifecycle.seals, wave)
	return lifecycle.sealErr[wave]
}

func (lifecycle *incrementalVectorCarrierWavesTestLifecycle) Abort(index int, cause error) {
	lifecycle.abortN++
	lifecycle.incrementalVectorTestLifecycle.Abort(index, cause)
}

func TestIncrementalComponentVectorCarrierWavesLoadsAndSealsEveryWave(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 2, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("a-%d", index)
	})
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("b-%d", index)
	})
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(3),
		waves: [][]IncrementalComponentVectorCarrierLane{
			{laneA},
			{},
			{laneB},
		},
	}
	lifecycle.onBegin = func(index int) {
		if index == 0 {
			laneA.Bindings["source"].([]string)[1] = "poison"
		}
	}
	err := engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "a", Count: 2}}},
				{},
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "b", Count: 1}}},
			},
			Lifecycle: lifecycle,
		},
	)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 2}, lifecycle.loads)
	assert.Equal(t, []int{0, 1, 2}, lifecycle.seals)
	assert.Equal(t, []int{0, 1, 2}, lifecycle.begins)
	assert.Equal(t, []int{0, 1, 2}, lifecycle.ends)
	assert.Equal(t, []string{"A:a-0", "A:a-1", "B:b-0"}, lifecycle.outputs)
	assert.Zero(t, lifecycle.abortN)
}

func TestIncrementalComponentVectorCarrierWavesLoadsOwnedLaneRanges(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 2, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("a-%d", index)
	})
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 2, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("b-%d", index)
	})
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(4),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB}},
	}
	lifecycle.onBegin = func(index int) {
		if index == 0 {
			laneA.Bindings["source"].([]string)[1] = "poison-a"
			laneB.Bindings["source"].([]string)[0] = "poison-b"
		}
	}
	require.NoError(t, engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{{Lanes: []IncrementalComponentVectorCarrierWaveLane{
				{TemplateName: "a", Count: 2},
				{TemplateName: "b", Count: 2},
			}}},
			Lifecycle: lifecycle,
		},
	))
	assert.Equal(t, []int{0}, lifecycle.loads)
	assert.Equal(t, []int{0}, lifecycle.seals)
	assert.Equal(t, []int{0, 1, 2, 3}, lifecycle.begins)
	assert.Equal(t, []int{0, 1, 2, 3}, lifecycle.ends)
	assert.Equal(t, []string{"A:a-0", "A:a-1", "B:b-0", "B:b-1"}, lifecycle.outputs)
	assert.Zero(t, lifecycle.abortN)
}

func TestIncrementalComponentVectorCarrierWavesUsesOwnedExplicitChildOrder(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 3, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("a-%d", index)
	})
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 2, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("b-%d", index)
	})
	entryPoints := []string{"b", "a", "b", "a", "a"}
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(5),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB}},
	}
	lifecycle.onBegin = func(index int) {
		if index == 0 {
			entryPoints[1] = "b"
			laneA.Bindings["source"].([]string)[0] = "poison-a"
			laneB.Bindings["source"].([]string)[1] = "poison-b"
		}
	}
	require.NoError(t, engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{{
				Lanes: []IncrementalComponentVectorCarrierWaveLane{
					{TemplateName: "a", Count: 3},
					{TemplateName: "b", Count: 2},
				},
				EntryPoints: entryPoints,
			}},
			Lifecycle: lifecycle,
		},
	))
	assert.Equal(t, []int{0}, lifecycle.loads)
	assert.Equal(t, []int{0}, lifecycle.seals)
	assert.Equal(t, []int{0, 1, 2, 3, 4}, lifecycle.begins)
	assert.Equal(t, []int{0, 1, 2, 3, 4}, lifecycle.ends)
	assert.Equal(t, []string{"B:b-0", "A:a-0", "B:b-1", "A:a-1", "A:a-2"}, lifecycle.outputs)
	assert.Zero(t, lifecycle.abortN)
}

func TestIncrementalComponentVectorCarrierWavesRejectsMalformedExplicitChildOrder(t *testing.T) {
	for _, test := range []struct {
		name        string
		entryPoints []string
		wantError   string
	}{
		{name: "empty", entryPoints: []string{}, wantError: "has 0 entrypoints for 2 items"},
		{name: "wrong length", entryPoints: []string{"a"}, wantError: "has 1 entrypoints for 2 items"},
		{name: "unknown entrypoint", entryPoints: []string{"a", "c"}, wantError: `entrypoint "c" is not eligible`},
		{name: "wrong lane count", entryPoints: []string{"a", "a"}, wantError: "does not match lane counts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
				"a": `A:{{ source }}`,
				"b": `B:{{ source }}`,
			})
			laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, nil)
			laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, nil)
			lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
				incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(2),
				waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB}},
			}
			err := engine.RenderIncrementalComponentVectorCarrierWaves(
				t.Context(),
				IncrementalComponentVectorCarrierWavesInput{
					Waves: []IncrementalComponentVectorCarrierWave{{
						Lanes: []IncrementalComponentVectorCarrierWaveLane{
							{TemplateName: "a", Count: 1},
							{TemplateName: "b", Count: 1},
						},
						EntryPoints: test.entryPoints,
					}},
					Lifecycle: lifecycle,
				},
			)
			require.ErrorContains(t, err, test.wantError)
			assert.Empty(t, lifecycle.loads)
			assert.Empty(t, lifecycle.seals)
			assert.Empty(t, lifecycle.begins)
			assert.Equal(t, 1, lifecycle.abortN)
			assert.Equal(t, -1, lifecycle.abortIndex)
		})
	}
}

func TestIncrementalComponentVectorCarrierWavesPrevalidatesEveryLane(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, nil)
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, nil)
	laneB.Bindings["source"] = []int{42}
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(2),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB}},
	}
	err := engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{{Lanes: []IncrementalComponentVectorCarrierWaveLane{
				{TemplateName: "a", Count: 1},
				{TemplateName: "b", Count: 1},
			}}},
			Lifecycle: lifecycle,
		},
	)
	require.ErrorContains(t, err, `binding "source"`)
	assert.Equal(t, []int{0}, lifecycle.loads)
	assert.Empty(t, lifecycle.seals)
	assert.Empty(t, lifecycle.begins)
	assert.Empty(t, lifecycle.ends)
	assert.Equal(t, []string{"", ""}, lifecycle.outputs)
	assert.Equal(t, 1, lifecycle.abortN)
}

func TestIncrementalComponentVectorCarrierWavesStopsAfterLateLoadFailure(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `{{ source }}`,
		"b": `{{ source }}`,
		"c": `{{ source }}`,
	})
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, nil)
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, nil)
	laneC := newIncrementalVectorCarrierTestLane(t, engine, "c", 1, nil)
	lateFailure := errors.New("late load failed")
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(3),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA}, {laneB}, {laneC}},
		loadErr:                        map[int]error{1: lateFailure},
	}
	err := engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "a", Count: 1}}},
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "b", Count: 1}}},
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "c", Count: 1}}},
			},
			Lifecycle: lifecycle,
		},
	)
	require.ErrorIs(t, err, lateFailure)
	assert.Equal(t, []int{0, 1}, lifecycle.loads)
	assert.Equal(t, []int{0}, lifecycle.seals)
	assert.Equal(t, []int{0}, lifecycle.begins)
	assert.Equal(t, []int{0}, lifecycle.ends)
	assert.Equal(t, 1, lifecycle.abortN)
	assert.Equal(t, -1, lifecycle.abortIndex)
}

func TestIncrementalComponentVectorCarrierWavesReportsFinalEmptyWaveFailure(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{"a": `{{ source }}`})
	lane := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, nil)
	finalFailure := errors.New("empty final wave failed")
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(1),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{lane}, {}},
		sealErr:                        map[int]error{1: finalFailure},
	}
	err := engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "a", Count: 1}}},
				{},
			},
			Lifecycle: lifecycle,
		},
	)
	require.ErrorIs(t, err, finalFailure)
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 1, batchErr.Index)
	assert.Equal(t, []int{0, 1}, lifecycle.loads)
	assert.Equal(t, []int{0, 1}, lifecycle.seals)
	assert.Equal(t, 1, lifecycle.abortN)
	assert.Equal(t, -1, lifecycle.abortIndex)
}

func TestIncrementalComponentVectorCarrierWavesRevokesRetainedCallbackAcrossWaves(t *testing.T) {
	const source = `{% var _, _ = http.Fetch(func() string { var value, _ = http.Fetch("body"); return tostring(value) }) %}`
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{"a": source, "b": source})
	fetcher := &incrementalVectorCarrierRetainingFetcher{}
	setFetcher := func(_ int, values map[string]any) { values["http"] = fetcher }
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, setFetcher)
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, setFetcher)
	var crossWavePanic any
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(2),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA}, {laneB}},
	}
	lifecycle.onBegin = func(index int) {
		if index == 1 {
			func() {
				defer func() { crossWavePanic = recover() }()
				fetcher.callback()()
			}()
		}
	}
	require.NoError(t, engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "a", Count: 1}}},
				{Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "b", Count: 1}}},
			},
			Lifecycle: lifecycle,
		},
	))
	require.NotNil(t, crossWavePanic)
	assert.ErrorContains(t, fmt.Errorf("%v", crossWavePanic), scriggo.ErrVectorGenerationRevoked.Error())
	assert.Equal(t, int64(2), fetcher.bodyCalls.Load())
	assert.Equal(t, []int{0, 1}, lifecycle.loads)
	assert.Equal(t, []int{0, 1}, lifecycle.seals)
}

func TestIncrementalComponentVectorCarrierWavesRevokesRetainedCallbackAcrossExplicitChildren(t *testing.T) {
	const source = `{% var _, _ = http.Fetch(func() string { var value, _ = http.Fetch("body"); return tostring(value) }) %}`
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{"a": source, "b": source})
	fetcher := &incrementalVectorCarrierRetainingFetcher{}
	setFetcher := func(_ int, values map[string]any) { values["http"] = fetcher }
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, setFetcher)
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, setFetcher)
	var crossChildPanic any
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(2),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB}},
	}
	lifecycle.onBegin = func(index int) {
		if index == 1 {
			func() {
				defer func() { crossChildPanic = recover() }()
				fetcher.callback()()
			}()
		}
	}
	require.NoError(t, engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{{
				Lanes: []IncrementalComponentVectorCarrierWaveLane{
					{TemplateName: "a", Count: 1},
					{TemplateName: "b", Count: 1},
				},
				EntryPoints: []string{"b", "a"},
			}},
			Lifecycle: lifecycle,
		},
	))
	require.NotNil(t, crossChildPanic)
	assert.ErrorContains(t, fmt.Errorf("%v", crossChildPanic), scriggo.ErrVectorGenerationRevoked.Error())
	assert.Equal(t, int64(2), fetcher.bodyCalls.Load())
	assert.Equal(t, []int{0}, lifecycle.loads)
	assert.Equal(t, []int{0}, lifecycle.seals)
}

var preparedIncrementalVectorCarrierWaveSink *preparedIncrementalVectorCarrierWave

func BenchmarkPrepareIncrementalVectorCarrierWave(b *testing.B) {
	engine := newIncrementalVectorCarrierTestEngine(b, map[string]string{
		"a": `A:{{ source }}`,
		"b": `B:{{ source }}`,
	})
	const count = 256
	laneA := newIncrementalVectorCarrierTestLane(b, engine, "a", count, nil)
	laneB := newIncrementalVectorCarrierTestLane(b, engine, "b", count, nil)
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(count * 2),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB}},
	}
	input := IncrementalComponentVectorCarrierWavesInput{
		Waves: []IncrementalComponentVectorCarrierWave{{Lanes: []IncrementalComponentVectorCarrierWaveLane{
			{TemplateName: "a", Count: count},
			{TemplateName: "b", Count: count},
		}}},
		Lifecycle: lifecycle,
	}
	prepared, err := prepareIncrementalVectorCarrierWavesInput(b.Context(), engine.incrementalVectorCarrier, input)
	require.NoError(b, err)
	items := newIncrementalVectorController(
		prepared.authority,
		lifecycle,
		prepared.contextSeals,
		len(prepared.contextSeals),
	)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		preparedIncrementalVectorCarrierWaveSink, err = prepareIncrementalVectorCarrierWave(
			engine.incrementalVectorCarrier,
			prepared.shapes[0],
			lifecycle.waves[0],
			items,
		)
		if err != nil {
			b.Fatal(err)
		}
	}
}
