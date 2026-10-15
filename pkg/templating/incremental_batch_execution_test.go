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
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type incrementalBatchParallelProbe struct {
	started chan int
	release chan struct{}
	active  atomic.Int32
	maximum atomic.Int32
	once    sync.Once
}

func (p *incrementalBatchParallelProbe) unblock() {
	p.once.Do(func() { close(p.release) })
}

type incrementalBatchProbeFetcher struct {
	probe     *incrementalBatchParallelProbe
	index     int
	lifecycle *atomic.Bool
}

func (*incrementalBatchProbeFetcher) Pending(string) bool { return false }

func (f *incrementalBatchProbeFetcher) Fetch(...any) (any, error) {
	if !f.lifecycle.Load() {
		return nil, errors.New("incremental batch item executed outside its lifecycle")
	}
	active := f.probe.active.Add(1)
	defer f.probe.active.Add(-1)
	for {
		maximum := f.probe.maximum.Load()
		if active <= maximum || f.probe.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	f.probe.started <- f.index
	<-f.probe.release
	return fmt.Sprintf("item-%03d", f.index), nil
}

func TestRenderIncrementalComponentsExecutesIndependentItemsInParallel(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	engine, err := New(map[string]string{
		"component": `{% var value, fetchErr = http.Fetch() %}` +
			`{% if fetchErr != nil %}{{ fail(tostring(fetchErr)) }}{% end %}{{ value }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	const itemCount = incrementalBatchRunsPerWorker * 4
	probe := &incrementalBatchParallelProbe{
		started: make(chan int, itemCount),
		release: make(chan struct{}),
	}
	items := make([]IncrementalComponentBatchItem, itemCount)
	active := make([]atomic.Bool, itemCount)
	for index := range items {
		items[index] = IncrementalComponentBatchItem{
			Context: t.Context(),
			TemplateContext: incrementalComponentContext(map[string]any{
				"http": &incrementalBatchProbeFetcher{
					probe: probe, index: index, lifecycle: &active[index],
				},
			}),
			Activate: func() error {
				if !active[index].CompareAndSwap(false, true) {
					return errors.New("incremental batch item activated twice")
				}
				return nil
			},
			Deactivate: func() {
				active[index].Store(false)
			},
		}
	}

	type batchResult struct {
		outputs []string
		err     error
	}
	done := make(chan batchResult, 1)
	go func() {
		outputs, runErr := engine.RenderIncrementalComponents(t.Context(), "component", items)
		done <- batchResult{outputs: outputs, err: runErr}
	}()
	finished := false
	t.Cleanup(func() {
		probe.unblock()
		if !finished {
			<-done
		}
	})

	for range 2 {
		select {
		case <-probe.started:
		case <-time.After(5 * time.Second):
			probe.unblock()
			t.Fatal("incremental batch did not execute two items concurrently")
		}
	}
	probe.unblock()
	result := <-done
	finished = true
	require.NoError(t, result.err)
	require.Len(t, result.outputs, itemCount)
	for index := range result.outputs {
		assert.Equal(t, fmt.Sprintf("item-%03d", index), result.outputs[index])
		assert.False(t, active[index].Load())
	}
	assert.GreaterOrEqual(t, probe.maximum.Load(), int32(2))
}

func TestRenderIncrementalComponentsReportsLowestParallelErrorIndex(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	engine, err := New(map[string]string{
		"component": `{% if item["fail"] == true %}{{ fail(tostring(item["id"])) }}{% end %}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	const itemCount = incrementalBatchRunsPerWorker * 4
	items := make([]IncrementalComponentBatchItem, itemCount)
	for index := range items {
		items[index] = IncrementalComponentBatchItem{
			Context: t.Context(),
			TemplateContext: incrementalComponentContext(map[string]any{
				"item": map[string]any{
					"id":   index,
					"fail": index == 5 || index == 35,
				},
			}),
		}
	}

	_, err = engine.RenderIncrementalComponents(t.Context(), "component", items)
	var batchErr *IncrementalComponentBatchError
	require.ErrorAs(t, err, &batchErr)
	assert.Equal(t, 5, batchErr.Index)
	assert.ErrorContains(t, batchErr, "5")
}

type countingWorkerGate struct {
	admit             bool
	acquired, release atomic.Int32
}

func (g *countingWorkerGate) TryAcquire() bool {
	if !g.admit {
		return false
	}
	g.acquired.Add(1)
	return true
}

func (g *countingWorkerGate) Release() { g.release.Add(1) }

func TestRenderIncrementalComponentsRefusedGateLeavesOneWorker(t *testing.T) {
	engine, probe, items := newGatedBatchFixture(t)
	probe.unblock()
	gate := &countingWorkerGate{}

	outputs, err := engine.RenderIncrementalComponents(WithWorkerGate(t.Context(), gate), "component", items)
	require.NoError(t, err)
	assertGatedBatchOutputs(t, outputs, gate)
	assert.Equal(t, int32(1), probe.maximum.Load(), "a refusing gate leaves only the first worker")
}

func TestRenderIncrementalComponentsAdmittedGateRunsWorkersInParallel(t *testing.T) {
	engine, probe, items := newGatedBatchFixture(t)
	gate := &countingWorkerGate{admit: true}

	type batchResult struct {
		outputs []string
		err     error
	}
	done := make(chan batchResult, 1)
	go func() {
		outputs, err := engine.RenderIncrementalComponents(WithWorkerGate(t.Context(), gate), "component", items)
		done <- batchResult{outputs: outputs, err: err}
	}()
	for range 2 {
		select {
		case <-probe.started:
		case <-time.After(5 * time.Second):
			probe.unblock()
			<-done
			t.Fatal("an admitting gate did not let two items run concurrently")
		}
	}
	probe.unblock()
	result := <-done
	require.NoError(t, result.err)
	assertGatedBatchOutputs(t, result.outputs, gate)
}

func newGatedBatchFixture(t *testing.T) (*ScriggoEngine, *incrementalBatchParallelProbe, []IncrementalComponentBatchItem) {
	t.Helper()
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	engine, err := New(map[string]string{
		"component": `{% var value, fetchErr = http.Fetch() %}` +
			`{% if fetchErr != nil %}{{ fail(tostring(fetchErr)) }}{% end %}{{ value }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
	})
	require.NoError(t, err)

	const itemCount = incrementalBatchRunsPerWorker * 4
	probe := &incrementalBatchParallelProbe{
		started: make(chan int, itemCount),
		release: make(chan struct{}),
	}
	t.Cleanup(probe.unblock)
	lifecycle := &atomic.Bool{}
	lifecycle.Store(true)
	items := make([]IncrementalComponentBatchItem, itemCount)
	for index := range items {
		items[index] = IncrementalComponentBatchItem{
			Context: t.Context(),
			TemplateContext: incrementalComponentContext(map[string]any{
				"http": &incrementalBatchProbeFetcher{probe: probe, index: index, lifecycle: lifecycle},
			}),
		}
	}
	return engine, probe, items
}

func assertGatedBatchOutputs(t *testing.T, outputs []string, gate *countingWorkerGate) {
	t.Helper()
	require.Len(t, outputs, incrementalBatchRunsPerWorker*4)
	for index := range outputs {
		assert.Equal(t, fmt.Sprintf("item-%03d", index), outputs[index])
	}
	assert.Equal(t, gate.acquired.Load(), gate.release.Load(), "every admitted chunk is released")
}
