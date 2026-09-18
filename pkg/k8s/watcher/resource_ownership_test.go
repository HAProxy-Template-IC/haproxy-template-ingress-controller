package watcher

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestImmutableInformerResourceLifecycle(t *testing.T) {
	resource := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"namespace": "default", "name": "target", "resourceVersion": "1"},
		"spec":     map[string]any{"group": "A", "enabled": true, "payload": []any{"original"}},
	}}
	k8sClient := newTestClient(t, resource)
	cfg := validWatcherConfig()
	cfg.IndexBy = []string{"spec.group"}
	cfg.FieldSelector = "spec.enabled=true"
	w, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	stopped := make(chan error, 1)
	go func() { stopped <- w.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("watcher did not stop")
		}
	})
	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)
	pinned, err := w.Store().(stores.SnapshotProvider).Pin()
	require.NoError(t, err)
	api := k8sClient.DynamicClient().Resource(configMapGVR).Namespace("default")
	for index, change := range []struct {
		group   string
		enabled bool
	}{
		{group: "B", enabled: true},
		{group: "B", enabled: false},
		{group: "C", enabled: true},
	} {
		updated := resource.DeepCopy()
		updated.SetResourceVersion([]string{"2", "3", "4"}[index])
		updated.Object["spec"] = map[string]any{
			"group": change.group, "enabled": change.enabled, "payload": []any{"changed"},
		}
		_, err = api.Update(ctx, updated, metav1.UpdateOptions{})
		require.NoError(t, err)
		require.EventuallyWithT(t, func(collect *assert.CollectT) {
			items, listErr := w.Store().List()
			if !assert.NoError(collect, listErr) {
				return
			}
			if !change.enabled {
				assert.Empty(collect, items)
				return
			}
			if assert.Len(collect, items, 1) {
				assert.Equal(collect, updated.Object, items[0])
			}
		}, 5*time.Second, 10*time.Millisecond)
		for _, group := range []string{"A", "B", "C"} {
			items, getErr := w.Store().Get(group)
			require.NoError(t, getErr)
			if change.enabled && group == change.group {
				require.Len(t, items, 1)
			} else {
				assert.Empty(t, items)
			}
		}
	}
	require.NoError(t, api.Delete(ctx, "target", metav1.DeleteOptions{}))
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		items, listErr := w.Store().List()
		assert.NoError(collect, listErr)
		assert.Empty(collect, items)
	}, 5*time.Second, 10*time.Millisecond)
	original, err := pinned.List()
	require.NoError(t, err)
	require.Equal(t, []any{resource.Object}, original)
}

func TestImmutableInformerTombstone(t *testing.T) {
	w, err := New(validWatcherConfig(), newTestClient(t), slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, w.Stop()) })
	resource, err := store.NewImmutableResource(map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "target"},
	})
	require.NoError(t, err)
	w.handleAdd(resource)
	items, err := w.Store().List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	w.handleDelete(cache.DeletedFinalStateUnknown{Key: "default/target", Obj: resource})
	items, err = w.Store().List()
	require.NoError(t, err)
	assert.Empty(t, items)
}
