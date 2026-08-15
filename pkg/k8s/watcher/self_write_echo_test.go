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

// A watch event that echoes this controller's own status write must refresh
// the store (the next render reads the status it just wrote) but must not
// count as a change: before this filter every status write cost a full
// render, three to four per route change under sequential churn.
func TestWatcher_HandleUpdate_SelfWriteEcho_RefreshesStoreWithoutChange(t *testing.T) {
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

	old := makeConfigMap("cm", "10", "a")
	w.handleAdd(old)

	// Our own write comes back with the resourceVersion the API server returned.
	registry.Record(cfg.GVR.GroupResource(), "default", "cm", "11")
	echo := makeConfigMap("cm", "11", "b")
	w.handleUpdate(old, echo)
	time.Sleep(50 * time.Millisecond)

	got, err := w.Store().Get("default", "cm")
	require.NoError(t, err)
	require.Len(t, got, 1)
	obj := got[0].(map[string]any)
	assert.Equal(t, "11", obj["metadata"].(map[string]any)["resourceVersion"], "store must hold the echoed object")
	assert.Equal(t, int32(0), modified.Load(), "an echoed self-write is not a change")

	// Somebody else's write to the same object is a change.
	external := makeConfigMap("cm", "12", "c")
	w.handleUpdate(echo, external)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), modified.Load())
}

func makeConfigMap(name, version, value string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":            name,
				"namespace":       "default",
				"resourceVersion": version,
			},
			"data": map[string]any{"k": value},
		},
	}
}
