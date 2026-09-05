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
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type incrementalVectorCarrierRetainingFetcher struct {
	mu        sync.Mutex
	callbacks []func() string
	bodyCalls atomic.Int64
}

func (fetcher *incrementalVectorCarrierRetainingFetcher) Fetch(arguments ...any) (any, error) {
	fetcher.bodyCalls.Add(1)
	if len(arguments) == 1 {
		if callback, ok := arguments[0].(func() string); ok {
			fetcher.mu.Lock()
			fetcher.callbacks = append(fetcher.callbacks, callback)
			fetcher.mu.Unlock()
			return "retained", nil
		}
	}
	return "body", nil
}

func (fetcher *incrementalVectorCarrierRetainingFetcher) callback() func() string {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	return fetcher.callbacks[0]
}

type incrementalVectorCarrierDrainingFetcher struct {
	started chan struct{}
	release chan struct{}
	done    chan any
	once    sync.Once
}

func (fetcher *incrementalVectorCarrierDrainingFetcher) Fetch(arguments ...any) (any, error) {
	if len(arguments) == 1 {
		if callback, ok := arguments[0].(func() string); ok {
			go func() {
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					callback()
				}()
				fetcher.done <- recovered
			}()
			<-fetcher.started
			return "retained", nil
		}
		if value, ok := arguments[0].(string); ok && value == "block" {
			fetcher.once.Do(func() { close(fetcher.started) })
			<-fetcher.release
			return "body", nil
		}
	}
	return "body", nil
}

func TestIncrementalComponentVectorCarrierReinitializesSharedImportedState(t *testing.T) {
	engine, err := New(map[string]string{
		"a":       `{% import "library" for Next %}{{ Next() }}`,
		"b":       `{% import "library" for Next %}{{ Next() }}`,
		"library": `{% var Counter int %}{% macro Next %}{% Counter++ %}{{ source }}{{ Counter }}{% end %}`,
	}, &Options{
		EntryPoints:            []string{"a", "b"},
		IncrementalEntryPoints: []string{"a", "b"},
		Declarations: map[string]any{
			"resources": incrementalBatchResourcesDeclaration(),
		},
	})
	require.NoError(t, err)
	_, carrierAvailable := engine.IncrementalComponentVectorCarrierEligibility()
	require.Truef(t, carrierAvailable, "carrier rejection: %v", engine.IncrementalComponentVectorCarrierDiagnostic())
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 2, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("a%d", index)
	})
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 2, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("b%d", index)
	})
	want := make([]string, 0, 4)
	for _, lane := range []IncrementalComponentVectorCarrierLane{laneA, laneB} {
		for _, itemCtx := range lane.Contexts {
			values := maps.Clone(itemCtx.Value(RenderContextContextKey).(map[string]any))
			delete(values, "renderMode")
			output, renderErr := engine.RenderIncrementalComponent(itemCtx, lane.TemplateName, values)
			require.NoError(t, renderErr)
			want = append(want, output)
		}
	}
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(4),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB}},
	}

	require.NoError(t, engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{{
				Lanes: []IncrementalComponentVectorCarrierWaveLane{
					{TemplateName: "a", Count: 2},
					{TemplateName: "b", Count: 2},
				},
			}},
			Lifecycle: lifecycle,
		},
	))
	assert.Equal(t, want, lifecycle.outputs)
}

func TestIncrementalComponentVectorCarrierExcludesIneligibleEntrypoint(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"eligible":   `{{ source }}`,
		"ineligible": `{% var pointer = &item %}{% _ = pointer %}{{ source }}`,
	})
	eligibility, ok := engine.IncrementalComponentVectorCarrierEligibility()
	require.True(t, ok)
	assert.Equal(t, []string{"eligible"}, eligibility.TemplateNames)
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(1),
	}
	err := engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{{
				Lanes: []IncrementalComponentVectorCarrierWaveLane{{TemplateName: "ineligible", Count: 1}},
			}},
			Lifecycle: lifecycle,
		},
	)
	require.ErrorContains(t, err, "not eligible")
	assert.Empty(t, lifecycle.begins)
	assert.True(t, lifecycle.abortCalled)
}

func TestIncrementalComponentVectorCarrierRejectsLanePoisonBeforeExecution(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `{{ source }}`,
		"b": `{{ source }}`,
	})
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, nil)
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, nil)
	laneB.Contexts[0] = laneA.Contexts[0]
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
			}},
			Lifecycle: lifecycle,
		},
	)
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 1, batchErr.Index)
	assert.Empty(t, lifecycle.begins)
	assert.True(t, lifecycle.abortCalled)
}

func TestIncrementalComponentVectorCarrierDrainsBeforeAbort(t *testing.T) {
	engine := newIncrementalVectorCarrierTestEngine(t, map[string]string{
		"a": `done`,
		"b": `{% var _, _ = http.Fetch(func() string { var value, _ = http.Fetch("block"); return tostring(value) }) %}{{ fail("failed") }}`,
		"c": `unused`,
	})
	fetcher := &incrementalVectorCarrierDrainingFetcher{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan any, 1),
	}
	laneA := newIncrementalVectorCarrierTestLane(t, engine, "a", 1, nil)
	laneB := newIncrementalVectorCarrierTestLane(t, engine, "b", 1, func(_ int, values map[string]any) {
		values["http"] = fetcher
	})
	laneC := newIncrementalVectorCarrierTestLane(t, engine, "c", 1, nil)
	lifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(3),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{laneA, laneB, laneC}},
	}
	result := make(chan error, 1)
	go func() {
		result <- engine.RenderIncrementalComponentVectorCarrierWaves(
			t.Context(),
			IncrementalComponentVectorCarrierWavesInput{
				Waves: []IncrementalComponentVectorCarrierWave{{
					Lanes: []IncrementalComponentVectorCarrierWaveLane{
						{TemplateName: "a", Count: 1},
						{TemplateName: "b", Count: 1},
						{TemplateName: "c", Count: 1},
					},
				}},
				Lifecycle: lifecycle,
			},
		)
	}()
	<-fetcher.started
	select {
	case err := <-result:
		t.Fatalf("carrier returned before retained invocation drained: %v", err)
	default:
	}
	close(fetcher.release)
	require.NoError(t, errorFromRecoveredInvocation(<-fetcher.done))
	err := <-result
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 1, batchErr.Index)
	assert.Equal(t, []int{0, 1}, lifecycle.begins)
	assert.Equal(t, []int{0}, lifecycle.ends)
	assert.Equal(t, "done", lifecycle.outputs[0])
	assert.True(t, lifecycle.abortCalled)
	assert.Equal(t, 1, lifecycle.abortIndex)
}

func newIncrementalVectorCarrierTestEngine(
	tb testing.TB,
	templates map[string]string,
) *ScriggoEngine {
	tb.Helper()
	entryPoints := make([]string, 0, len(templates))
	for name := range templates {
		entryPoints = append(entryPoints, name)
	}
	slices.Sort(entryPoints)
	engine, err := New(templates, &Options{
		EntryPoints:            entryPoints,
		IncrementalEntryPoints: entryPoints,
		Declarations: map[string]any{
			"resources": incrementalBatchResourcesDeclaration(),
		},
	})
	require.NoError(tb, err)
	return engine
}

func newIncrementalVectorCarrierTestLane(
	tb testing.TB,
	engine *ScriggoEngine,
	templateName string,
	count int,
	mutate func(int, map[string]any),
) IncrementalComponentVectorCarrierLane {
	tb.Helper()
	input := newIncrementalVectorTestInputForTemplate(tb, engine, templateName, count, mutate)
	return IncrementalComponentVectorCarrierLane{
		TemplateName: templateName,
		Count:        count,
		Bindings:     input.Bindings,
		Contexts:     input.Contexts,
	}
}

func errorFromRecoveredInvocation(recovered any) error {
	if recovered == nil {
		return nil
	}
	if err, ok := recovered.(error); ok {
		return err
	}
	return errors.New(fmt.Sprint(recovered))
}
