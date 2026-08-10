// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// endpointSlice builds the shape the shipped chart indexes: several slices of
// one Service share the (namespace, service-name label) bucket and differ only
// in name. Namespace is fixed because it is itself an index key — slices in
// different namespaces land in different buckets and so cannot exercise the
// sibling-eviction path this file is about.
func endpointSlice(name, serviceName string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "discovery.k8s.io/v1",
			"kind":       "EndpointSlice",
			"metadata": map[string]any{
				"name":      name,
				"namespace": "default",
				"labels": map[string]any{
					"kubernetes.io/service-name": serviceName,
				},
			},
		},
	}
}

// storeBackedWatcher wires the pieces processDelete actually touches: an
// indexer over the chart's EndpointSlice index, a real MemoryStore, and a
// debouncer to absorb the change records.
func storeBackedWatcher(t *testing.T) *Watcher {
	t.Helper()

	// The exact expression the chart ships for EndpointSlices
	// (charts/haptic/values.yaml): the dot in the label key is escaped.
	indexBy := []string{"metadata.namespace", `metadata.labels.kubernetes\.io/service-name`}
	idx, err := indexer.New(indexer.Config{IndexBy: indexBy})
	require.NoError(t, err)

	s := store.NewMemoryStore(len(indexBy))

	return &Watcher{
		logger:  slog.Default(),
		indexer: idx,
		store:   s,
		debouncer: NewDebouncer(time.Hour, func(types.Store, types.ChangeStats) {
		}, s, true),
		config: types.WatcherConfig{IndexBy: indexBy},
	}
}

// TestWatcher_ProcessDelete_OnlyEvictsTheDeletedSlice is the watcher-level
// guard for identity-scoped deletion. The store-level test cannot catch a
// swapped (name, namespace) argument order or a wrong identity source here,
// because both still produce a well-formed call.
func TestWatcher_ProcessDelete_OnlyEvictsTheDeletedSlice(t *testing.T) {
	w := storeBackedWatcher(t)

	sliceA := endpointSlice("nginx-abc12", "nginx")
	sliceB := endpointSlice("nginx-xyz89", "nginx")
	otherService := endpointSlice("api-def34", "api")

	w.processAdd(sliceA)
	w.processAdd(sliceB)
	w.processAdd(otherService)

	got, err := w.store.Get("default", "nginx")
	require.NoError(t, err)
	require.Len(t, got, 2, "both slices should share the service's bucket")

	w.processDelete(sliceA)

	got, err = w.store.Get("default", "nginx")
	require.NoError(t, err)
	require.Len(t, got, 1,
		"deleting one EndpointSlice must not evict its siblings — the bucket is "+
			"keyed by (namespace, service-name), so a bucket-wide delete would "+
			"empty the backend and serve 503 until an unrelated event repopulated it")

	survivor, ok := got[0].(map[string]any)
	require.True(t, ok, "stored resource should be a map")
	metadata, ok := survivor["metadata"].(map[string]any)
	require.True(t, ok, "stored resource should carry metadata")
	require.Equal(t, "nginx-xyz89", metadata["name"])

	// A different Service's slice lives in its own bucket and is untouched.
	other, err := w.store.Get("default", "api")
	require.NoError(t, err)
	require.Len(t, other, 1, "an unrelated Service's slices must be unaffected")
}

// TestWatcher_ProcessDelete_LastSliceEmptiesBucket pins that identity-scoped
// deletion still deletes: removing every member clears the bucket.
func TestWatcher_ProcessDelete_LastSliceEmptiesBucket(t *testing.T) {
	w := storeBackedWatcher(t)

	sliceA := endpointSlice("nginx-abc12", "nginx")
	sliceB := endpointSlice("nginx-xyz89", "nginx")

	w.processAdd(sliceA)
	w.processAdd(sliceB)
	w.processDelete(sliceA)
	w.processDelete(sliceB)

	got, err := w.store.Get("default", "nginx")
	require.NoError(t, err)
	require.Empty(t, got)
}
