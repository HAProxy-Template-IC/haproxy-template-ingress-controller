package watcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

// TestNewSingle verifies SingleWatcher creation.
// testWatcherConfig is the ConfigMap watch these tests share; a nil onChange
// installs a no-op callback.
func testWatcherConfig(onChange func(any) error) types.SingleWatcherConfig {
	if onChange == nil {
		onChange = func(any) error { return nil }
	}
	return types.SingleWatcherConfig{
		GVR:       configMapGVR,
		Namespace: "default",
		Name:      "test-config",
		OnChange:  onChange,
	}
}

func TestNewSingle(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	tests := []struct {
		name      string
		config    types.SingleWatcherConfig
		client    *client.Client
		expectErr bool
	}{
		{
			name: "valid config",
			config: types.SingleWatcherConfig{
				GVR:       configMapGVR,
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj any) error {
					return nil
				},
			},
			client:    k8sClient,
			expectErr: false,
		},
		{
			name: "missing GVR resource",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:   "",
					Version: "v1",
				},
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj any) error {
					return nil
				},
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "missing namespace",
			config: types.SingleWatcherConfig{
				GVR:  configMapGVR,
				Name: "test-config",
				OnChange: func(obj any) error {
					return nil
				},
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "missing name",
			config: types.SingleWatcherConfig{
				GVR:       configMapGVR,
				Namespace: "default",
				OnChange: func(obj any) error {
					return nil
				},
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "missing callback",
			config: types.SingleWatcherConfig{
				GVR:       configMapGVR,
				Namespace: "default",
				Name:      "test-config",
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "nil client",
			config: types.SingleWatcherConfig{
				GVR:       configMapGVR,
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj any) error {
					return nil
				},
			},
			client:    nil,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewSingle(&tt.config, tt.client)
			if tt.expectErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestSingleWatcher_WaitForSyncTimeout verifies timeout behavior.
func TestSingleWatcher_WaitForSyncTimeout(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	cfg := testWatcherConfig(nil)

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Don't start watcher - just wait for sync with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err = w.WaitForSync(ctx)
	if err == nil {
		t.Error("expected timeout error but got nil")
	}
}

// TestSingleWatcherConfig_Validate verifies configuration validation.
func TestSingleWatcherConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		config    types.SingleWatcherConfig
		expectErr bool
		errField  string
	}{
		{
			name: "valid config",
			config: types.SingleWatcherConfig{
				GVR:       configMapGVR,
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj any) error {
					return nil
				},
			},
			expectErr: false,
		},
		{
			name: "missing GVR resource",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:   "",
					Version: "v1",
				},
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj any) error {
					return nil
				},
			},
			expectErr: true,
			errField:  "GVR.Resource",
		},
		{
			name: "missing namespace",
			config: types.SingleWatcherConfig{
				GVR:  configMapGVR,
				Name: "test-config",
				OnChange: func(obj any) error {
					return nil
				},
			},
			expectErr: true,
			errField:  "Namespace",
		},
		{
			name: "missing name",
			config: types.SingleWatcherConfig{
				GVR:       configMapGVR,
				Namespace: "default",
				OnChange: func(obj any) error {
					return nil
				},
			},
			expectErr: true,
			errField:  "Name",
		},
		{
			name: "missing callback",
			config: types.SingleWatcherConfig{
				GVR:       configMapGVR,
				Namespace: "default",
				Name:      "test-config",
			},
			expectErr: true,
			errField:  "OnChange",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if tt.expectErr && err != nil {
				if configErr, ok := err.(*types.ConfigError); ok {
					if configErr.Field != tt.errField {
						t.Errorf("expected error field %q, got %q", tt.errField, configErr.Field)
					}
				}
			}
		})
	}
}

// TestSingleWatcherConfig_SetDefaults verifies default value application.
func TestSingleWatcherConfig_SetDefaults(t *testing.T) {
	cfg := testWatcherConfig(nil)
	// Context is nil

	cfg.SetDefaults()

	if cfg.Context == nil {
		t.Error("Context should have been set to default value")
	}
}

// TestSingleWatcher_NoAddCallbacksDuringSync verifies Add events don't trigger callbacks during sync.
func TestSingleWatcher_NoAddCallbacksDuringSync(t *testing.T) {
	k8sClient := createFakeClientForConfigMapListing()

	callbackCount := 0
	cfg := testWatcherConfig(func(any) error { callbackCount++; return nil })

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Simulate Add event before sync completes
	mockResource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "test-config",
				"namespace": "default",
			},
		},
	}
	w.handleAdd(mockResource)

	// Callback should not have been called (not synced yet)
	if callbackCount != 0 {
		t.Errorf("expected 0 callbacks during sync, got %d", callbackCount)
	}

	// Mark as synced
	w.synced.Store(true)

	// Now Add should trigger callback
	w.handleAdd(mockResource)
	if callbackCount != 1 {
		t.Errorf("expected 1 callback after sync, got %d", callbackCount)
	}
}

// TestSingleWatcher_NoUpdateCallbacksDuringSync verifies Update events don't trigger callbacks during sync.
func TestSingleWatcher_NoUpdateCallbacksDuringSync(t *testing.T) {
	k8sClient := createFakeClientForConfigMapListing()

	callbackCount := 0
	cfg := testWatcherConfig(func(any) error { callbackCount++; return nil })

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Simulate Update event before sync completes
	// Use different resourceVersion and generation to simulate a real spec update
	oldResource := createUnstructuredWithGeneration("100", 1)
	newResource := createUnstructuredWithGeneration("101", 2)
	w.handleUpdate(oldResource, newResource)

	// Callback should not have been called (not synced yet)
	if callbackCount != 0 {
		t.Errorf("expected 0 callbacks during sync, got %d", callbackCount)
	}

	// Mark as synced
	w.synced.Store(true)

	// Now Update with spec change (different generation) should trigger callback
	oldResource2 := createUnstructuredWithGeneration("101", 2)
	newResource2 := createUnstructuredWithGeneration("102", 3)
	w.handleUpdate(oldResource2, newResource2)
	if callbackCount != 1 {
		t.Errorf("expected 1 callback after sync, got %d", callbackCount)
	}
}

// TestSingleWatcher_NoDeleteCallbacksDuringSync verifies Delete events don't trigger callbacks during sync.
func TestSingleWatcher_NoDeleteCallbacksDuringSync(t *testing.T) {
	k8sClient := createFakeClientForConfigMapListing()

	callbackCount := 0
	cfg := testWatcherConfig(func(any) error { callbackCount++; return nil })

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Simulate Delete event before sync completes
	mockResource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "test-config",
				"namespace": "default",
			},
		},
	}
	w.handleDelete(mockResource)

	// Callback should not have been called (not synced yet)
	if callbackCount != 0 {
		t.Errorf("expected 0 callbacks during sync, got %d", callbackCount)
	}

	// Mark as synced
	w.synced.Store(true)

	// Now Delete should trigger callback
	w.handleDelete(mockResource)
	if callbackCount != 1 {
		t.Errorf("expected 1 callback after sync, got %d", callbackCount)
	}
}

// TestSingleWatcher_StopIdempotency verifies Stop() can be called multiple times safely.
func TestSingleWatcher_StopIdempotency(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	cfg := testWatcherConfig(nil)

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Call Stop() multiple times - should not panic
	err1 := w.Stop()
	err2 := w.Stop()
	err3 := w.Stop()

	if err1 != nil {
		t.Errorf("first Stop() returned error: %v", err1)
	}
	if err2 != nil {
		t.Errorf("second Stop() returned error: %v", err2)
	}
	if err3 != nil {
		t.Errorf("third Stop() returned error: %v", err3)
	}
}

// TestSingleWatcher_ConcurrentCallbacks verifies thread-safe callback invocation after sync.
func TestSingleWatcher_ConcurrentCallbacks(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	callbackCount := 0
	var mu sync.Mutex

	cfg := types.SingleWatcherConfig{
		GVR:       configMapGVR,
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj any) error {
			mu.Lock()
			callbackCount++
			mu.Unlock()
			time.Sleep(1 * time.Millisecond) // Simulate work
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Mark as synced
	w.synced.Store(true)

	// Trigger callbacks concurrently
	mockResource := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "test-config",
				"namespace": "default",
			},
		},
	}

	var wg sync.WaitGroup
	numGoroutines := 10

	for range numGoroutines {
		wg.Go(func() {
			w.handleAdd(mockResource)
		})
	}

	wg.Wait()

	mu.Lock()
	finalCount := callbackCount
	mu.Unlock()

	if finalCount != numGoroutines {
		t.Errorf("expected %d callbacks, got %d", numGoroutines, finalCount)
	}
}

// TestSingleWatcher_StartIdempotency verifies Start() can be called multiple times safely.
func TestSingleWatcher_StartIdempotency(t *testing.T) {
	k8sClient := createFakeClientForConfigMapListing()

	cfg := testWatcherConfig(nil)

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Create a context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start watcher multiple times concurrently - should not panic
	var wg sync.WaitGroup
	numStarts := 3
	errs := make([]error, numStarts)

	for i := range numStarts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = w.Start(ctx)
		}(i)
	}

	// Wait for all Start() calls to complete
	wg.Wait()

	// All should return nil or context cancelled error
	for i, err := range errs {
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Start() call %d returned unexpected error: %v", i, err)
		}
	}

	// Verify sync completed
	if !w.synced.Load() {
		t.Error("expected watcher to be synced after Start() completes")
	}
}

// Helper functions for tests

// createFakeClientForSingleWatcher creates a fake Kubernetes client suitable for SingleWatcher tests.
func createFakeClientForSingleWatcher() *client.Client {
	fakeClientset := kubefake.NewClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	return client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")
}

// createFakeClientForConfigMapListing creates a fake Kubernetes client that knows how
// to list ConfigMaps (required by tests that actually run the informer). Mirrors the
// upstream SingleWatcher use case where informer list calls need the GVK-to-ListKind
// mapping registered on the dynamic fake client.
func createFakeClientForConfigMapListing() *client.Client {
	scheme := runtime.NewScheme()
	fakeClientset := kubefake.NewClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "configmaps"}: "ConfigMapList",
		},
	)
	return client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")
}

// createUnstructuredConfigMap creates an unstructured ConfigMap for testing.
func createUnstructuredConfigMap(name, namespace, resourceVersion string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
	if resourceVersion != "" {
		obj.SetResourceVersion(resourceVersion)
	}
	return obj
}

// createUnstructuredWithGeneration creates an unstructured object with generation for testing.
// Uses fixed name "test-config" and namespace "default" to match common test patterns.
func createUnstructuredWithGeneration(resourceVersion string, generation int64) *unstructured.Unstructured {
	obj := createUnstructuredConfigMap("test-config", "default", resourceVersion)
	obj.SetGeneration(generation)
	return obj
}

// TestSingleWatcher_SkipsStatusOnlyUpdates verifies that Update events with unchanged
// generation (status-only updates) don't trigger callbacks.
// This is the canonical Kubernetes pattern for resources with status subresources.
func TestSingleWatcher_SkipsStatusOnlyUpdates(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	callbackCount := 0
	cfg := testWatcherConfig(func(any) error { callbackCount++; return nil })

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Mark as synced
	w.synced.Store(true)

	// Simulate spec change: generation 1 -> 2 (should trigger callback)
	oldResource := createUnstructuredWithGeneration("100", 1)
	newResource := createUnstructuredWithGeneration("101", 2)
	w.handleUpdate(oldResource, newResource)

	// Callback should have been called for spec change
	if callbackCount != 1 {
		t.Errorf("expected 1 callback for spec change, got %d", callbackCount)
	}

	// Simulate status-only update: same generation, different resourceVersion
	// This is what happens when StatusUpdater updates the CRD status
	statusUpdateOld := createUnstructuredWithGeneration("101", 2)
	statusUpdateNew := createUnstructuredWithGeneration("102", 2) // Same generation!
	w.handleUpdate(statusUpdateOld, statusUpdateNew)

	// Callback should NOT have been called for status-only update
	if callbackCount != 1 {
		t.Errorf("expected still 1 callback (status-only update should be skipped), got %d", callbackCount)
	}

	// Simulate another spec change: generation 2 -> 3 (should trigger callback)
	specUpdateOld := createUnstructuredWithGeneration("102", 2)
	specUpdateNew := createUnstructuredWithGeneration("103", 3)
	w.handleUpdate(specUpdateOld, specUpdateNew)

	// Callback should have been called for the second spec change
	if callbackCount != 2 {
		t.Errorf("expected 2 callbacks after second spec change, got %d", callbackCount)
	}
}

// TestSingleWatcher_DoesNotSkipUpdatesWhenGenerationStaysZero verifies that the
// "status-only update" guard above doesn't accidentally drop updates to
// resources that simply don't use .metadata.generation. Secrets, ConfigMaps,
// and most core/v1 resources keep generation pinned at 0, so old/new gen
// equality is the always-true degenerate case for them — the watcher must
// still deliver the callback when the resourceVersion advances.
//
// Regression guard for the webhook-cert Secret rotation bug: before this
// fix, every Secret rotation observed by a SingleWatcher was silently
// dropped, so cert-manager renewals and the e2e CA-rotation test harness
// never reached the controller's cert pipeline.
func TestSingleWatcher_DoesNotSkipUpdatesWhenGenerationStaysZero(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	callbackCount := 0
	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "secrets",
		},
		Namespace: "default",
		Name:      "test-cert",
		OnChange: func(obj any) error {
			callbackCount++
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}
	w.synced.Store(true)

	// Both old and new have generation=0 (the Secret default), but the
	// resourceVersion advances — the canonical "Secret content rotated"
	// shape. The callback must fire.
	old := createUnstructuredWithGeneration("100", 0)
	updated := createUnstructuredWithGeneration("101", 0)
	w.handleUpdate(old, updated)

	if callbackCount != 1 {
		t.Errorf("expected 1 callback for Secret-content rotation (gen 0→0, rv 100→101), got %d", callbackCount)
	}
}

// TestSingleWatcher_SkipsResyncCallback verifies that Update events with unchanged
// resource version (resync events) don't trigger callbacks.
func TestSingleWatcher_SkipsResyncCallback(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	callbackCount := 0
	cfg := testWatcherConfig(func(any) error { callbackCount++; return nil })

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Mark as synced
	w.synced.Store(true)

	// Simulate real update: different resourceVersion AND generation (spec change)
	oldResource := createUnstructuredWithGeneration("100", 1)
	newResource := createUnstructuredWithGeneration("101", 2)
	w.handleUpdate(oldResource, newResource)

	// Callback should have been called for real update
	if callbackCount != 1 {
		t.Errorf("expected 1 callback for real update, got %d", callbackCount)
	}

	// Simulate resync: same resourceVersion "101" -> "101" (also same generation)
	w.handleUpdate(newResource, newResource)

	// Callback should NOT have been called for resync
	if callbackCount != 1 {
		t.Errorf("expected still 1 callback (resync should be skipped), got %d", callbackCount)
	}

	// Simulate another real update: different resourceVersion AND generation (spec change)
	newerResource := createUnstructuredWithGeneration("102", 3)
	w.handleUpdate(newResource, newerResource)

	// Callback should have been called again for the second real update
	if callbackCount != 2 {
		t.Errorf("expected 2 callbacks after second real update, got %d", callbackCount)
	}
}

// TestSingleWatcher_OnSyncComplete_CalledAfterSync verifies that OnSyncComplete is called
// after initial sync completes, delivering the current resource from the cache.
func TestSingleWatcher_OnSyncComplete_CalledAfterSync(t *testing.T) {
	k8sClient := createFakeClientForConfigMapListing()

	var onChangeCount int
	var onSyncCompleteCount int
	var syncCompleteResource *unstructured.Unstructured
	var mu sync.Mutex

	cfg := types.SingleWatcherConfig{
		GVR:       configMapGVR,
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj any) error {
			mu.Lock()
			onChangeCount++
			mu.Unlock()
			return nil
		},
		OnSyncComplete: func(obj any) error {
			mu.Lock()
			onSyncCompleteCount++
			if u, ok := obj.(*unstructured.Unstructured); ok {
				syncCompleteResource = u
			}
			mu.Unlock()
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Start watcher
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = w.Start(ctx)
	}()

	err = w.WaitForSync(ctx)
	if err != nil {
		t.Fatalf("WaitForSync failed: %v", err)
	}

	// Give a small buffer for the callback to complete
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	changeCount := onChangeCount
	syncCount := onSyncCompleteCount
	mu.Unlock()

	// OnSyncComplete should have been called once
	if syncCount != 1 {
		t.Errorf("expected OnSyncComplete to be called once, got %d", syncCount)
	}

	// OnChange should NOT have been called during sync (no resource exists in fake client)
	if changeCount != 0 {
		t.Errorf("expected OnChange to not be called (suppressed during sync), got %d", changeCount)
	}

	// In this test, the resource doesn't exist, so syncCompleteResource should be nil
	// (getCurrentResourceFromCache returns nil when no resource is in cache)
	mu.Lock()
	res := syncCompleteResource
	mu.Unlock()
	if res != nil {
		t.Errorf("expected syncCompleteResource to be nil (no resource in cache), got %v", res)
	}
}

// TestSingleWatcher_OnSyncComplete_ReceivesCurrentResource verifies that OnSyncComplete
// receives the current resource state from the informer cache.
func TestSingleWatcher_OnSyncComplete_ReceivesCurrentResource(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	var syncCompleteResource *unstructured.Unstructured
	var mu sync.Mutex

	cfg := types.SingleWatcherConfig{
		GVR:       configMapGVR,
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj any) error {
			return nil
		},
		OnSyncComplete: func(obj any) error {
			mu.Lock()
			if u, ok := obj.(*unstructured.Unstructured); ok {
				syncCompleteResource = u
			}
			mu.Unlock()
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Manually populate the informer cache by simulating what happens during sync.
	// Add a mock resource directly to the informer's store.
	mockResource := createUnstructuredConfigMap("test-config", "default", "12345")
	err = w.informer.GetStore().Add(mockResource)
	if err != nil {
		t.Fatalf("adding mock resource to store: %v", err)
	}

	// Mark sync as complete (simulating what Start() does after cache sync)
	w.synced.Store(true)

	// Invoke OnSyncComplete manually to test the behavior
	if w.config.OnSyncComplete != nil {
		resource := w.getCurrentResourceFromCache()
		if resource != nil {
			_ = w.config.OnSyncComplete(resource)
		}
	}

	// Verify OnSyncComplete received the resource
	mu.Lock()
	res := syncCompleteResource
	mu.Unlock()

	if res == nil {
		t.Fatal("expected syncCompleteResource to be non-nil")
	}

	if res.GetName() != "test-config" {
		t.Errorf("expected resource name 'test-config', got '%s'", res.GetName())
	}

	if res.GetResourceVersion() != "12345" {
		t.Errorf("expected resource version '12345', got '%s'", res.GetResourceVersion())
	}
}

// TestSingleWatcher_OnSyncComplete_Optional verifies that OnSyncComplete is optional
// and watcher works correctly when it's not provided.
func TestSingleWatcher_OnSyncComplete_Optional(t *testing.T) {
	k8sClient := createFakeClientForConfigMapListing()

	cfg := testWatcherConfig(nil)
	// OnSyncComplete is nil - should be optional

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("creating watcher: %v", err)
	}

	// Start watcher - should not panic even with nil OnSyncComplete
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = w.Start(ctx)
	}()

	// Wait for sync - should succeed without OnSyncComplete callback
	err = w.WaitForSync(ctx)
	if err != nil {
		t.Fatalf("WaitForSync failed: %v", err)
	}

	// Verify watcher is synced
	if !w.synced.Load() {
		t.Error("expected watcher to be synced")
	}
}
