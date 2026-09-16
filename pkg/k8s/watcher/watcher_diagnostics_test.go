package watcher

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

func TestWatcherDiagnosticsKeepResourceContentsPrivate(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := validWatcherConfig()
	config.IndexBy = []string{"metadata.namespace", "spec.credential"}
	watcher, err := New(config, newTestClient(t), logger)
	require.NoError(t, err)
	old := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "example.com/v1", "kind": "PrivateRecord",
		"metadata": map[string]any{"name": "private-record", "namespace": "default", "resourceVersion": "10"},
		"spec":     map[string]any{"credential": "old-private-value"},
	}}
	updated := old.DeepCopy()
	updated.SetResourceVersion("11")
	require.NoError(t, unstructured.SetNestedField(updated.Object, "new-private-value", "spec", "credential"))
	watcher.handleAdd(old)
	watcher.handleUpdate(old, updated)
	watcher.handleDelete(updated)
	assert.NotContains(t, output.String(), "old-private-value")
	assert.NotContains(t, output.String(), "new-private-value")
	assert.Contains(t, output.String(), "name=private-record")
	assert.Contains(t, output.String(), "resource_version=11")
	assert.Contains(t, output.String(), "previous_resource_version=10")
	assert.NotPanics(t, func() { watcher.handleUpdate(nil, old) })

	output.Reset()
	watcher.store = store.NewMemoryStore(1)
	watcher.handleAdd(old)
	watcher.handleUpdate(old, updated)
	watcher.handleDelete(updated)
	assert.Contains(t, output.String(), "wrong number of keys")
	assert.NotContains(t, output.String(), "old-private-value")
	assert.NotContains(t, output.String(), "new-private-value")

	output.Reset()
	watcher.indexer, err = indexer.New(indexer.Config{IndexBy: []string{"spec.credential[?(@.active==true)]"}})
	require.NoError(t, err)
	watcher.handleAdd(old)
	assert.Contains(t, output.String(), "spec.credential")
	assert.NotContains(t, output.String(), "old-private-value")
}
