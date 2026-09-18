package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestWatcher_FilteredUpdateBurstWithZeroDebounce(t *testing.T) {
	tests := []struct {
		name            string
		ignoreFields    []string
		expectedUpdates int32
	}{
		{
			name:            "ignored timestamps with resource version retained",
			ignoreFields:    []string{"metadata.managedFields", "status.conditions[*].lastTransitionTime"},
			expectedUpdates: 1,
		},
		{
			name:            "ignored timestamps with resource version filtered",
			ignoreFields:    []string{"metadata.managedFields", "metadata.resourceVersion", "status.conditions[*].lastTransitionTime"},
			expectedUpdates: 1,
		},
		{
			name:            "retained timestamps remain observable changes",
			ignoreFields:    []string{"metadata.managedFields"},
			expectedUpdates: 101,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFilteredUpdateBurst(t, tt.ignoreFields, tt.expectedUpdates)
		})
	}
}

func testFilteredUpdateBurst(t *testing.T, ignoreFields []string, expectedUpdates int32) {
	t.Helper()
	initial := makeTimestampedResource(1, "initial")
	k8sClient := newTestClient(t, initial)
	var created, modified atomic.Int32
	visibleChange := make(chan struct{}, 1)
	cfg := validWatcherConfig()
	cfg.DebounceInterval = 0
	cfg.IgnoreFields = ignoreFields
	cfg.OnChange = func(resourceStore types.Store, stats types.ChangeStats) {
		created.Add(int32(stats.Created))
		updates := modified.Add(int32(stats.Modified))
		items, err := resourceStore.Get("default", "cm")
		if err != nil || len(items) != 1 {
			return
		}
		value, _, _ := unstructured.NestedString(items[0].(map[string]any), "data", "k")
		if value == "changed" && updates == expectedUpdates {
			select {
			case visibleChange <- struct{}{}:
			default:
			}
		}
	}
	w, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	t.Cleanup(func() { require.NoError(t, w.Stop()) })
	go func() { _ = w.Start(ctx) }()
	_, err = w.WaitForSync(ctx)
	require.NoError(t, err)

	resources := k8sClient.DynamicClient().Resource(cfg.GVR).Namespace("default")
	for version := 2; version <= 101; version++ {
		_, err = resources.Update(ctx, makeTimestampedResource(version, "initial"), metav1.UpdateOptions{})
		require.NoError(t, err)
	}
	_, err = resources.Update(ctx, makeTimestampedResource(102, "changed"), metav1.UpdateOptions{})
	require.NoError(t, err)

	select {
	case <-visibleChange:
	case <-ctx.Done():
		t.Fatal("visible update did not trigger a change callback")
	}
	w.debouncer.callbacks.Wait()
	require.Equal(t, int32(1), created.Load())
	require.Equal(t, expectedUpdates, modified.Load())
	journal := w.Store().(stores.RevisionJournal)
	sequence, changes, complete := journal.ChangesSince(0)
	require.True(t, complete)
	require.Equal(t, uint64(expectedUpdates+1), sequence)
	require.Len(t, changes, int(expectedUpdates+1))
}

func makeTimestampedResource(version int, value string) *unstructured.Unstructured {
	stamp := fmt.Sprint(version)
	resource := makeConfigMap(stamp, value)
	resource.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: stamp}})
	resource.Object["status"] = map[string]any{
		"conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastTransitionTime": stamp}},
	}
	return resource
}
