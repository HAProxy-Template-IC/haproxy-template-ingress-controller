// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// newNormalizeTestIndexer builds an indexer over the standard namespace/name
// index with the given IgnoreFields.
func newNormalizeTestIndexer(t *testing.T, ignoreFields []string) *indexer.Indexer {
	t.Helper()
	idx, err := indexer.New(indexer.Config{
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		IgnoreFields: ignoreFields,
	})
	require.NoError(t, err)
	return idx
}

// A MemoryStore (full) watcher must install the NORMALISING transform, not the
// body-stripping projection: for a memory store the stored body is what
// templates read, so a projection here would serve them a husk and every field
// outside metadata would render as missing. ADR-0012 records this as blocker B1.
//
// This has to drive a real informer — the transform runs inside DeltaFIFO, so a
// test that calls handleAdd directly bypasses it entirely and cannot detect a
// projection wrongly installed on a memory store.
func TestNew_MemoryStore_InformerTransformKeepsFullBody(t *testing.T) {
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"namespace": "default", "name": "big-cm"},
		"data":       map[string]any{"blob": "HEAVYVALUE"},
	}}
	k8sClient := newTestClient(t, cm)

	cfg := validWatcherConfig()
	cfg.StoreType = types.StoreTypeMemory

	w, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = w.Start(ctx) }()

	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)

	// The informer's cache keeps the full body — this is the assertion that
	// fails loudly if someone wires the projection transform onto a memory store.
	items := w.informer.GetStore().List()
	require.Len(t, items, 1)
	stored, ok := items[0].(*unstructured.Unstructured)
	require.True(t, ok, "informer cache item should be *unstructured.Unstructured")
	blob, found, _ := unstructured.NestedString(stored.Object, "data", "blob")
	assert.True(t, found, "a memory-store watcher must NOT body-strip: the stored body is what templates read")
	assert.Equal(t, "HEAVYVALUE", blob)

	// And the store serves that same full body to the render.
	results, err := w.Store().Get("default", "big-cm")
	require.NoError(t, err)
	require.Len(t, results, 1)
	gotMap, ok := results[0].(map[string]any)
	require.True(t, ok, "store holds the resource body as map[string]any")
	blob, found, _ = unstructured.NestedString(gotMap, "data", "blob")
	assert.True(t, found, "store read must serve the full body")
	assert.Equal(t, "HEAVYVALUE", blob)
}

// The transform is also where the float→int conversion now happens, so a port
// that arrived as float64 must reach the store as int64 — templates render
// "port 80", never "port 80.0".
func TestNew_MemoryStore_TransformConvertsFloats(t *testing.T) {
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"namespace": "default", "name": "big-cm"},
		"spec":       map[string]any{"port": float64(80)},
	}}
	k8sClient := newTestClient(t, cm)

	cfg := validWatcherConfig()
	cfg.StoreType = types.StoreTypeMemory

	w, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = w.Start(ctx) }()

	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)

	results, err := w.Store().Get("default", "big-cm")
	require.NoError(t, err)
	require.Len(t, results, 1)
	gotMap, ok := results[0].(map[string]any)
	require.True(t, ok)

	spec, ok := gotMap["spec"].(map[string]any)
	require.True(t, ok, "spec should survive the normalising transform")
	assert.Equal(t, int64(80), spec["port"], "float64 ports must reach the store as int64")
}

// The transform must be idempotent: client-go may hand a cached — already
// transformed — object back to it on a Replace.
func TestNormalizeTransform_IsIdempotent(t *testing.T) {
	idx := newNormalizeTestIndexer(t, []string{"metadata.managedFields"})
	transform := newNormalizeTransform(idx)

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"namespace":     "default",
			"name":          "cm",
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
		},
		"spec": map[string]any{"port": float64(8080)},
	}}

	once, err := transform(obj)
	require.NoError(t, err)
	twice, err := transform(once)
	require.NoError(t, err)

	assert.Equal(t, once, twice, "applying the transform twice must equal applying it once")
}

// The transform hands back the SAME pointer rather than a copy. The store
// aliases that memory deliberately — it is what keeps one body in RAM instead
// of two — and the aliasing is only safe because all mutation happens here,
// before the cache or any handler sees the object.
func TestNormalizeTransform_ReturnsSamePointerAndFiltersIgnoredFields(t *testing.T) {
	idx := newNormalizeTestIndexer(t, []string{"metadata.managedFields"})
	transform := newNormalizeTransform(idx)

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"namespace":     "default",
			"name":          "cm",
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
		},
		"data": map[string]any{"blob": "keep-me"},
	}}

	got, err := transform(obj)
	require.NoError(t, err)
	assert.Same(t, obj, got, "transform must return the same pointer, not a copy")

	_, hasManagedFields, _ := unstructured.NestedFieldNoCopy(obj.Object, "metadata", "managedFields")
	assert.False(t, hasManagedFields, "IgnoreFields must be stripped by the transform")

	blob, found, _ := unstructured.NestedString(obj.Object, "data", "blob")
	assert.True(t, found, "the body must survive — normalise never projects")
	assert.Equal(t, "keep-me", blob)
}

// A tombstone (or anything else that is not *unstructured.Unstructured) passes
// through untouched, matching the projection transform's behaviour.
func TestNormalizeTransform_PassesThroughNonUnstructured(t *testing.T) {
	transform := newNormalizeTransform(newNormalizeTestIndexer(t, nil))

	in := "not-an-object"
	got, err := transform(in)
	require.NoError(t, err)
	assert.Equal(t, in, got)
}
