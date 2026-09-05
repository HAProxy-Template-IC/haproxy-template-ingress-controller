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
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderedResourceCollectorOwnsRegisteredAndReturnedObjects(t *testing.T) {
	labels := map[string]any{"app": "stable"}
	ports := []any{map[string]any{"port": 8080}}
	object := map[string]any{
		"metadata": map[string]any{"labels": labels},
		"spec":     map[string]any{"ports": ports},
	}
	collector := NewRenderedResourceCollector()
	require.NoError(t, collector.Register("v1", "Service", "default", "api", object))

	labels["app"] = "caller-poison"
	ports[0].(map[string]any)["port"] = 9090
	object["spec"] = map[string]any{"ports": []any{}}

	first := collector.Resources()
	require.Len(t, first, 1)
	assert.Equal(t, "stable", first[0].Object["metadata"].(map[string]any)["labels"].(map[string]any)["app"])
	assert.Equal(t, 8080, first[0].Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["port"])

	first[0].Object["metadata"].(map[string]any)["labels"].(map[string]any)["app"] = "result-poison"
	first[0].Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["port"] = 7070
	second := collector.Resources()
	assert.Equal(t, "stable", second[0].Object["metadata"].(map[string]any)["labels"].(map[string]any)["app"])
	assert.Equal(t, 8080, second[0].Object["spec"].(map[string]any)["ports"].([]any)[0].(map[string]any)["port"])
}

func TestRenderedResourceSnapshotReusesOnlyExactPreviousState(t *testing.T) {
	newSnapshot := func(t *testing.T, value string, previous ...*RenderedResourceSnapshot) *RenderedResourceSnapshot {
		t.Helper()
		collector := NewRenderedResourceCollector()
		require.NoError(t, collector.Register("v1", "ConfigMap", "default", "settings", map[string]any{
			"data": map[string]any{"value": value},
		}))
		snapshot, err := collector.Snapshot(previous...)
		require.NoError(t, err)
		return snapshot
	}

	first := newSnapshot(t, "stable")
	reused := newSnapshot(t, "stable", first)
	assert.Same(t, first, reused)

	foreignEqual := newSnapshot(t, "stable")
	equal, err := first.ExactEqual(foreignEqual)
	require.NoError(t, err)
	assert.True(t, equal)
	sameRoot, err := first.SameRoot(foreignEqual)
	require.NoError(t, err)
	assert.False(t, sameRoot)

	changed := newSnapshot(t, "changed", first)
	equal, err = first.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal)
	sameRoot, err = first.SameRoot(changed)
	require.NoError(t, err)
	assert.False(t, sameRoot)
}

func TestRenderedResourceSnapshotReturnsDetachedCompatibilityViews(t *testing.T) {
	collector := NewRenderedResourceCollector()
	require.NoError(t, collector.Register("v1", "ConfigMap", "default", "settings", map[string]any{
		"data": map[string]any{"value": "stable"},
	}))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	first, err := snapshot.Resources()
	require.NoError(t, err)
	first[0].Object["data"].(map[string]any)["value"] = "poison"
	second, err := snapshot.Resources()
	require.NoError(t, err)
	assert.Equal(t, "stable", second[0].Object["data"].(map[string]any)["value"])
}

func TestRenderedResourceSnapshotRejectsCopiedAndSubstitutedState(t *testing.T) {
	newSnapshot := func(t *testing.T, name string) *RenderedResourceSnapshot {
		t.Helper()
		collector := NewRenderedResourceCollector()
		require.NoError(t, collector.Register("v1", "ConfigMap", "default", name, map[string]any{}))
		snapshot, err := collector.Snapshot()
		require.NoError(t, err)
		return snapshot
	}
	first := newSnapshot(t, "first")
	second := newSnapshot(t, "second")

	copied := *first
	require.ErrorContains(t, copied.ValidateAuthentication(), "invalid provenance")

	substituted := *first
	substituted.seal = &substituted
	substituted.storage = second.storage
	require.ErrorContains(t, substituted.ValidateAuthentication(), "invalid provenance")

	first.auth.count++
	require.ErrorContains(t, first.ValidateAuthentication(), "invalid provenance")
}

func TestRenderedResourceSnapshotSealsCollector(t *testing.T) {
	collector := NewRenderedResourceCollector()
	require.NoError(t, collector.Register("v1", "ConfigMap", "default", "first", map[string]any{}))
	first, err := collector.Snapshot()
	require.NoError(t, err)
	second, err := collector.Snapshot()
	require.NoError(t, err)
	assert.Same(t, first, second)
	require.ErrorContains(t,
		collector.Register("v1", "ConfigMap", "default", "later", map[string]any{}),
		"sealed",
	)
}

func TestRenderedResourceCollectorRejectsNonImmutableValues(t *testing.T) {
	collector := NewRenderedResourceCollector()
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	require.ErrorContains(t,
		collector.Register("v1", "ConfigMap", "default", "cycle", cyclic),
		"reference cycle",
	)
	require.ErrorContains(t,
		collector.Register("v1", "ConfigMap", "default", "nan", map[string]any{"value": math.NaN()}),
		"not finite",
	)
}

func TestRenderedResourceSnapshotConcurrentMaterializationIsDetached(t *testing.T) {
	collector := NewRenderedResourceCollector()
	require.NoError(t, collector.Register("v1", "ConfigMap", "default", "settings", map[string]any{
		"data": map[string]any{"value": "stable"},
	}))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	const workers = 32
	errorsByWorker := make(chan error, workers)
	var workersDone sync.WaitGroup
	for range workers {
		workersDone.Add(1)
		go func() {
			defer workersDone.Done()
			resources, materializeErr := snapshot.Resources()
			if materializeErr == nil {
				resources[0].Object["data"].(map[string]any)["value"] = "caller-local"
			}
			errorsByWorker <- materializeErr
		}()
	}
	workersDone.Wait()
	close(errorsByWorker)
	for materializeErr := range errorsByWorker {
		require.NoError(t, materializeErr)
	}
	resources, err := snapshot.Resources()
	require.NoError(t, err)
	assert.Equal(t, "stable", resources[0].Object["data"].(map[string]any)["value"])
}
