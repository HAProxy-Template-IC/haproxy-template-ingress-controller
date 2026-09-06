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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

func TestWatcher_HandleUpdate_SelfWriteEchoTriggersWhenStoredInputChanges(t *testing.T) {
	registry := types.NewSelfWriteRegistry(0)
	var modified atomic.Int32

	cfg := validWatcherConfig()
	cfg.CallOnChangeDuringSync = true
	cfg.DebounceInterval = 5 * time.Millisecond
	cfg.SelfWrites = registry
	cfg.OnChange = func(_ types.Store, stats types.ChangeStats) {
		modified.Add(int32(stats.Modified))
	}
	w, err := New(cfg, newTestClient(t), slog.Default())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	go func() { _ = w.Start(ctx) }()
	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)

	old := makeConfigMap("10", "a")
	w.handleAdd(old)

	// Our own write comes back with the resourceVersion the API server returned.
	registry.Record(cfg.GVR.GroupResource(), "default", "cm", "11")
	echo := makeConfigMap("11", "b")
	w.handleUpdate(old, echo)
	time.Sleep(50 * time.Millisecond)

	got, err := w.Store().Get("default", "cm")
	require.NoError(t, err)
	require.Len(t, got, 1)
	obj := got[0].(map[string]any)
	assert.Equal(t, "11", obj["metadata"].(map[string]any)["resourceVersion"], "store must hold the echoed object")
	assert.Equal(t, int32(1), modified.Load(), "an observable self-write must trigger reconciliation")

	registry.Record(cfg.GVR.GroupResource(), "default", "cm", "11")
	w.processUpdate(echo, echo.DeepCopy())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), modified.Load(), "a semantic no-op must not trigger reconciliation")

	// Somebody else's write to the same object is a change.
	external := makeConfigMap("12", "c")
	w.handleUpdate(echo, external)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(2), modified.Load())

	w.processUpdate(external, external.DeepCopy())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(2), modified.Load(), "an external semantic no-op must not trigger reconciliation")
}

func TestWatcher_DuplicateAddAndDeleteDoNotTriggerChange(t *testing.T) {
	var changes atomic.Int32

	cfg := validWatcherConfig()
	cfg.CallOnChangeDuringSync = true
	cfg.DebounceInterval = 5 * time.Millisecond
	cfg.OnChange = func(_ types.Store, stats types.ChangeStats) {
		changes.Add(int32(stats.Total()))
	}
	w, err := New(cfg, newTestClient(t), slog.Default())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	go func() { _ = w.Start(ctx) }()
	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)

	resource := makeConfigMap("10", "a")
	w.handleAdd(resource)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(1), changes.Load())

	w.handleAdd(resource.DeepCopy())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), changes.Load())

	w.handleDelete(resource)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(2), changes.Load())

	w.handleDelete(resource.DeepCopy())
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(2), changes.Load())
}

func makeConfigMap(version, value string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":            "cm",
				"namespace":       "default",
				"resourceVersion": version,
			},
			"data": map[string]any{"k": value},
		},
	}
}

// The controller's own status write comes back as an update that differs in
// the written field and the resourceVersion. With that field ignored, the
// echo is not a change and no reconciliation follows it.
func TestWatcher_HandleUpdate_IgnoredFieldEchoDoesNotTriggerChange(t *testing.T) {
	var modified atomic.Int32

	cfg := validWatcherConfig()
	cfg.CallOnChangeDuringSync = true
	cfg.DebounceInterval = 5 * time.Millisecond
	cfg.IgnoreFields = []string{"status.listeners[*].attachedRoutes"}
	cfg.OnChange = func(_ types.Store, stats types.ChangeStats) {
		modified.Add(int32(stats.Modified))
	}
	w, err := New(cfg, newTestClient(t), slog.Default())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	go func() { _ = w.Start(ctx) }()
	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)

	// The informer's transform applies IgnoreFields before a handler runs;
	// calling the handlers directly means applying it here.
	withCount := func(version string, attached int64, value string) *unstructured.Unstructured {
		object := makeConfigMap(version, value)
		object.Object["status"] = map[string]any{
			"listeners": []any{map[string]any{"name": "http", "attachedRoutes": attached}},
		}
		require.NoError(t, w.indexer.FilterFields(object))
		return object
	}
	first := withCount("10", 0, "a")
	w.handleAdd(first)
	time.Sleep(50 * time.Millisecond)

	w.handleUpdate(first, withCount("11", 1, "a"))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), modified.Load(), "an echo changing only an ignored field is not a change")

	w.handleUpdate(withCount("11", 1, "a"), withCount("12", 1, "b"))
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), modified.Load(), "a visible change still triggers")
}
