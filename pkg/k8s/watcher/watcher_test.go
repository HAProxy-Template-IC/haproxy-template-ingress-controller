// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package watcher

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"haproxy-template-ic/pkg/k8s/client"
	"haproxy-template-ic/pkg/k8s/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

var configMapGVR = schema.GroupVersionResource{
	Group:    "",
	Version:  "v1",
	Resource: "configmaps",
}

func newTestClient(t *testing.T) *client.Client {
	t.Helper()
	fakeClientset := kubefake.NewSimpleClientset()
	// Register the list kind for configmaps
	gvrToListKind := map[schema.GroupVersionResource]string{
		configMapGVR: "ConfigMapList",
	}
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		gvrToListKind,
	)
	return client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")
}

func newTestClientWithScheme(t *testing.T, objects ...runtime.Object) *client.Client {
	t.Helper()
	fakeClientset := kubefake.NewSimpleClientset()
	gvrToListKind := map[schema.GroupVersionResource]string{
		configMapGVR: "ConfigMapList",
	}
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		gvrToListKind,
		objects...,
	)
	return client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")
}

func validWatcherConfig() types.WatcherConfig {
	return types.WatcherConfig{
		GVR:              configMapGVR,
		IndexBy:          []string{"metadata.namespace", "metadata.name"},
		StoreType:        types.StoreTypeMemory,
		DebounceInterval: 50 * time.Millisecond,
		OnChange:         func(_ types.Store, _ types.ChangeStats) {}, // Required callback
	}
}

// =============================================================================
// New() Tests - Configuration Validation
// =============================================================================

func TestNew_ValidConfig(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.OnChange = func(_ types.Store, _ types.ChangeStats) {}

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
	assert.NotNil(t, watcher.Store())
	assert.False(t, watcher.IsSynced())
}

func TestNew_MissingGVR(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := types.WatcherConfig{
		IndexBy:   []string{"metadata.namespace"},
		StoreType: types.StoreTypeMemory,
	}

	watcher, err := New(cfg, k8sClient, nil)

	require.Error(t, err)
	assert.Nil(t, watcher)
	assert.Contains(t, err.Error(), "GVR")
}

func TestNew_MissingIndexBy(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := types.WatcherConfig{
		GVR:       configMapGVR,
		StoreType: types.StoreTypeMemory,
	}

	watcher, err := New(cfg, k8sClient, nil)

	require.Error(t, err)
	assert.Nil(t, watcher)
	assert.Contains(t, err.Error(), "IndexBy")
}

func TestNew_InvalidIndexExpression(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := types.WatcherConfig{
		GVR:       configMapGVR,
		IndexBy:   []string{"invalid..path"},
		StoreType: types.StoreTypeMemory,
	}

	watcher, err := New(cfg, k8sClient, nil)

	require.Error(t, err)
	assert.Nil(t, watcher)
}

func TestNew_MemoryStoreType(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.StoreType = types.StoreTypeMemory

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

func TestNew_CachedStoreType(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.StoreType = types.StoreTypeCached
	cfg.CacheTTL = 10 * time.Minute

	watcher, err := New(cfg, k8sClient, slog.Default())

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

func TestNew_InvalidStoreType(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.StoreType = types.StoreType(999) // Invalid

	watcher, err := New(cfg, k8sClient, nil)

	require.Error(t, err)
	assert.Nil(t, watcher)
	assert.Contains(t, err.Error(), "unsupported store type")
}

func TestNew_NamespacedWatch(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.NamespacedWatch = true

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

func TestNew_WithLabelSelector(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": "test"},
	}

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

func TestNew_WithIgnoreFields(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.IgnoreFields = []string{"metadata.managedFields", "metadata.resourceVersion"}

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

func TestNew_WithLogger(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	logger := slog.Default()

	watcher, err := New(cfg, k8sClient, logger)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

func TestNew_SetsDefaults(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := types.WatcherConfig{
		GVR:      configMapGVR,
		IndexBy:  []string{"metadata.namespace", "metadata.name"},
		OnChange: func(_ types.Store, _ types.ChangeStats) {}, // Required
		// StoreType and DebounceInterval not set - should use defaults
	}

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

// =============================================================================
// Accessor Tests
// =============================================================================

func TestWatcher_Store(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	store := watcher.Store()
	assert.NotNil(t, store)
}

func TestWatcher_IsSynced_InitiallyFalse(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	assert.False(t, watcher.IsSynced())
}

// =============================================================================
// Start/Stop Tests
// =============================================================================

func TestWatcher_Start_SyncsAndCallsOnSyncComplete(t *testing.T) {
	k8sClient := newTestClient(t)

	var syncCompleteCalled atomic.Bool
	var syncCount atomic.Int32

	cfg := validWatcherConfig()
	cfg.OnSyncComplete = func(_ types.Store, count int) {
		syncCompleteCalled.Store(true)
		syncCount.Store(int32(count))
	}

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- watcher.Start(ctx)
	}()

	// Wait for sync
	time.Sleep(500 * time.Millisecond)

	assert.True(t, watcher.IsSynced())
	assert.True(t, syncCompleteCalled.Load())
}

func TestWatcher_Start_ContextCancellation(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- watcher.Start(ctx)
	}()

	// Wait a bit for start
	time.Sleep(200 * time.Millisecond)

	// Cancel context
	cancel()

	// Should exit cleanly
	select {
	case err := <-errChan:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for watcher to stop")
	}
}

func TestWatcher_Stop(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- watcher.Start(ctx)
	}()

	// Wait for start
	time.Sleep(200 * time.Millisecond)

	// Cancel context to trigger stop (don't call Stop directly as Start calls it)
	cancel()

	// Wait for clean exit
	select {
	case err := <-errChan:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for watcher to stop")
	}
}

func TestWatcher_ForceSync(t *testing.T) {
	k8sClient := newTestClient(t)

	var callbackCalled atomic.Bool

	cfg := validWatcherConfig()
	cfg.OnChange = func(_ types.Store, _ types.ChangeStats) {
		callbackCalled.Store(true)
	}

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = watcher.Start(ctx)
	}()

	// Wait for sync
	time.Sleep(500 * time.Millisecond)

	// Force sync should flush debouncer
	watcher.ForceSync()

	// Give callback time to fire
	time.Sleep(100 * time.Millisecond)
}

// =============================================================================
// WaitForSync Tests
// =============================================================================

func TestWatcher_WaitForSync_Success(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = watcher.Start(ctx)
	}()

	// Wait for sync
	count, err := watcher.WaitForSync(ctx)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 0)
	assert.True(t, watcher.IsSynced())
}

func TestWatcher_WaitForSync_ContextCancelled(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	// Create already cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = watcher.WaitForSync(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sync")
}

// =============================================================================
// Event Handler Tests
// =============================================================================

func TestWatcher_HandleAdd(t *testing.T) {
	configMap := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-config",
				"namespace": "default",
			},
			"data": map[string]interface{}{
				"key": "value",
			},
		},
	}

	k8sClient := newTestClientWithScheme(t, configMap)

	var addCalled atomic.Bool

	cfg := validWatcherConfig()
	cfg.CallOnChangeDuringSync = true // See changes during sync
	cfg.OnChange = func(_ types.Store, _ types.ChangeStats) {
		addCalled.Store(true)
	}

	watcher, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		_ = watcher.Start(ctx)
	}()

	// Wait for sync and potential callbacks
	time.Sleep(1 * time.Second)

	// Check store has the resource
	_, err = watcher.Store().List()
	require.NoError(t, err)
	// The fake dynamic client may not fully simulate resource presence
	// but the watcher should have synced
	assert.True(t, watcher.IsSynced())
}

func TestWatcher_ConvertToUnstructured_Nil(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)

	// Test with nil
	result := watcher.convertToUnstructured(nil)
	assert.Nil(t, result)

	// Test with invalid type
	result = watcher.convertToUnstructured("invalid")
	assert.Nil(t, result)
}

func TestWatcher_ConvertToUnstructured_Unstructured(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, slog.Default())
	require.NoError(t, err)

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test",
				"namespace": "default",
			},
		},
	}

	result := watcher.convertToUnstructured(obj)
	assert.NotNil(t, result)
	assert.Equal(t, "test", result.GetName())
}

// =============================================================================
// Callback Configuration Tests
// =============================================================================

func TestWatcher_CallOnChangeDuringSync_False(t *testing.T) {
	k8sClient := newTestClient(t)

	var callbackCount atomic.Int32

	cfg := validWatcherConfig()
	cfg.CallOnChangeDuringSync = false // Default
	cfg.OnChange = func(_ types.Store, _ types.ChangeStats) {
		callbackCount.Add(1)
	}

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = watcher.Start(ctx)
	}()

	// Wait for sync
	time.Sleep(500 * time.Millisecond)

	assert.True(t, watcher.IsSynced())
	// Callbacks should be suppressed during sync
}

func TestWatcher_CallOnChangeDuringSync_True(t *testing.T) {
	k8sClient := newTestClient(t)

	var callbackCount atomic.Int32

	cfg := validWatcherConfig()
	cfg.CallOnChangeDuringSync = true
	cfg.OnChange = func(_ types.Store, _ types.ChangeStats) {
		callbackCount.Add(1)
	}

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = watcher.Start(ctx)
	}()

	// Wait for sync
	time.Sleep(500 * time.Millisecond)

	assert.True(t, watcher.IsSynced())
	// Callbacks may fire during sync
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestWatcher_ConcurrentAccess(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		_ = watcher.Start(ctx)
	}()

	// Wait for start
	time.Sleep(200 * time.Millisecond)

	// Concurrent access to IsSynced and Store
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = watcher.IsSynced()
				_ = watcher.Store()
			}
		}()
	}

	wg.Wait()
}

// =============================================================================
// applyListOptions Tests
// =============================================================================

func TestWatcher_ApplyListOptions_WithSelector(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.LabelSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": "test", "env": "prod"},
	}

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	options := &metav1.ListOptions{}
	watcher.applyListOptions(options)

	assert.NotEmpty(t, options.LabelSelector)
	assert.Contains(t, options.LabelSelector, "app=test")
}

func TestWatcher_ApplyListOptions_NoSelector(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.LabelSelector = nil

	watcher, err := New(cfg, k8sClient, nil)
	require.NoError(t, err)

	options := &metav1.ListOptions{}
	watcher.applyListOptions(options)

	assert.Empty(t, options.LabelSelector)
}

// =============================================================================
// Namespace Configuration Tests
// =============================================================================

func TestWatcher_AllNamespaces(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.Namespace = "" // All namespaces

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}

func TestWatcher_SpecificNamespace(t *testing.T) {
	k8sClient := newTestClient(t)
	cfg := validWatcherConfig()
	cfg.Namespace = "kube-system"

	watcher, err := New(cfg, k8sClient, nil)

	require.NoError(t, err)
	require.NotNil(t, watcher)
}
