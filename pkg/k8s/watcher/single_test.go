package watcher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"haproxy-template-ic/pkg/k8s/client"
	"haproxy-template-ic/pkg/k8s/types"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

// TestNewSingle verifies SingleWatcher creation.
func TestNewSingle(t *testing.T) {
	// Create fake clients
	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	tests := []struct {
		name      string
		config    types.SingleWatcherConfig
		client    *client.Client
		expectErr bool
	}{
		{
			name: "valid config",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj interface{}) error {
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
				OnChange: func(obj interface{}) error {
					return nil
				},
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "missing namespace",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Name: "test-config",
				OnChange: func(obj interface{}) error {
					return nil
				},
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "missing name",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				OnChange: func(obj interface{}) error {
					return nil
				},
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "missing callback",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				Name:      "test-config",
			},
			client:    k8sClient,
			expectErr: true,
		},
		{
			name: "nil client",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj interface{}) error {
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

// TestSingleWatcher_IsSynced verifies sync status tracking.
func TestSingleWatcher_IsSynced(t *testing.T) {
	// Create scheme and register ConfigMap
	scheme := runtime.NewScheme()
	//nolint:govet // unusedwrite: Group field intentionally set to "" for Kubernetes core types
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"}

	// Create fake clients with registered GVK
	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			{Group: "", Version: "v1", Resource: "configmaps"}: gvk.Kind,
		},
	)
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Initially not synced
	if w.IsSynced() {
		t.Error("watcher should not be synced initially")
	}

	// Start watcher in background
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		_ = w.Start(ctx)
	}()

	// Wait for sync
	err = w.WaitForSync(ctx)
	if err != nil {
		t.Fatalf("WaitForSync failed: %v", err)
	}

	// Should be synced now
	if !w.IsSynced() {
		t.Error("watcher should be synced after WaitForSync")
	}
}

// TestSingleWatcher_WaitForSyncTimeout verifies timeout behavior.
func TestSingleWatcher_WaitForSyncTimeout(t *testing.T) {
	// Create fake clients
	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
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
//
//nolint:revive // cognitive-complexity: Table-driven test with multiple test cases
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
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				Name:      "test-config",
				OnChange: func(obj interface{}) error {
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
				OnChange: func(obj interface{}) error {
					return nil
				},
			},
			expectErr: true,
			errField:  "GVR.Resource",
		},
		{
			name: "missing namespace",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Name: "test-config",
				OnChange: func(obj interface{}) error {
					return nil
				},
			},
			expectErr: true,
			errField:  "Namespace",
		},
		{
			name: "missing name",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				OnChange: func(obj interface{}) error {
					return nil
				},
			},
			expectErr: true,
			errField:  "Name",
		},
		{
			name: "missing callback",
			config: types.SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
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
	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			return nil
		},
		// Context is nil
	}

	cfg.SetDefaults()

	if cfg.Context == nil {
		t.Error("Context should have been set to default value")
	}
}

// TestSingleWatcher_NoAddCallbacksDuringSync verifies Add events don't trigger callbacks during sync.
func TestSingleWatcher_NoAddCallbacksDuringSync(t *testing.T) {
	scheme := runtime.NewScheme()
	//nolint:govet // unusedwrite: Group field intentionally set to "" for Kubernetes core types
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"}

	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			{Group: "", Version: "v1", Resource: "configmaps"}: gvk.Kind,
		},
	)
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	callbackCount := 0
	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			callbackCount++
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Simulate Add event before sync completes
	mockResource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
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
	scheme := runtime.NewScheme()
	//nolint:govet // unusedwrite: Group field intentionally set to "" for Kubernetes core types
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"}

	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			{Group: "", Version: "v1", Resource: "configmaps"}: gvk.Kind,
		},
	)
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	callbackCount := 0
	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			callbackCount++
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Simulate Update event before sync completes
	mockResource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-config",
				"namespace": "default",
			},
		},
	}
	w.handleUpdate(mockResource, mockResource)

	// Callback should not have been called (not synced yet)
	if callbackCount != 0 {
		t.Errorf("expected 0 callbacks during sync, got %d", callbackCount)
	}

	// Mark as synced
	w.synced.Store(true)

	// Now Update should trigger callback
	w.handleUpdate(mockResource, mockResource)
	if callbackCount != 1 {
		t.Errorf("expected 1 callback after sync, got %d", callbackCount)
	}
}

// TestSingleWatcher_NoDeleteCallbacksDuringSync verifies Delete events don't trigger callbacks during sync.
func TestSingleWatcher_NoDeleteCallbacksDuringSync(t *testing.T) {
	scheme := runtime.NewScheme()
	//nolint:govet // unusedwrite: Group field intentionally set to "" for Kubernetes core types
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"}

	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			{Group: "", Version: "v1", Resource: "configmaps"}: gvk.Kind,
		},
	)
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	callbackCount := 0
	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			callbackCount++
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Simulate Delete event before sync completes
	mockResource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
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
	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
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
	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	callbackCount := 0
	var mu sync.Mutex

	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			mu.Lock()
			callbackCount++
			mu.Unlock()
			time.Sleep(1 * time.Millisecond) // Simulate work
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Mark as synced
	w.synced.Store(true)

	// Trigger callbacks concurrently
	mockResource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name":      "test-config",
				"namespace": "default",
			},
		},
	}

	var wg sync.WaitGroup
	numGoroutines := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.handleAdd(mockResource)
		}()
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
	scheme := runtime.NewScheme()
	//nolint:govet // unusedwrite: Group field intentionally set to "" for Kubernetes core types
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMapList"}

	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			{Group: "", Version: "v1", Resource: "configmaps"}: gvk.Kind,
		},
	)
	k8sClient := client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")

	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Verify not started initially
	if w.IsStarted() {
		t.Error("watcher should not be started initially")
	}

	// Create a context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Start watcher multiple times concurrently - should not panic
	var wg sync.WaitGroup
	numStarts := 3
	errs := make([]error, numStarts)

	for i := 0; i < numStarts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = w.Start(ctx)
		}(i)
	}

	// Wait for all Start() calls to complete
	wg.Wait()

	// Verify IsStarted is true
	if !w.IsStarted() {
		t.Error("expected IsStarted() to be true after Start() calls")
	}

	// All should return nil or context cancelled error
	for i, err := range errs {
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Start() call %d returned unexpected error: %v", i, err)
		}
	}

	// Verify IsSynced is true (sync should have completed)
	if !w.IsSynced() {
		t.Error("expected IsSynced() to be true after Start() completes")
	}
}

// TestSingleWatcher_LastEventTimeUpdatesOnEvent verifies that LastEventTime is updated
// when events are received.
func TestSingleWatcher_LastEventTimeUpdatesOnEvent(t *testing.T) {
	fakeClient := createFakeClientForSingleWatcher()
	cfg := validSingleWatcherConfig()

	w, err := NewSingle(cfg, fakeClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Initially, LastEventTime should be zero
	if !w.LastEventTime().IsZero() {
		t.Error("expected LastEventTime() to be zero initially")
	}

	// Mark as synced to enable callbacks
	w.synced.Store(true)

	// Simulate an event
	beforeEvent := time.Now()
	w.handleAdd(createUnstructuredConfigMap("test-config", "default", ""))

	// Verify LastEventTime was updated
	lastEventTime := w.LastEventTime()
	if lastEventTime.IsZero() {
		t.Error("expected LastEventTime() to be non-zero after event")
	}

	// Verify it's within a reasonable range (allow 1 second tolerance due to Unix timestamp precision)
	// LastEventTime stores Unix seconds, so we truncate beforeEvent to second precision
	beforeEventTruncated := beforeEvent.Truncate(time.Second)
	if lastEventTime.Before(beforeEventTruncated) {
		t.Errorf("LastEventTime() %v is before %v", lastEventTime, beforeEventTruncated)
	}
	if lastEventTime.After(beforeEvent.Add(time.Second)) {
		t.Errorf("LastEventTime() %v is more than 1 second after %v", lastEventTime, beforeEvent)
	}
}

// TestSingleWatcher_LastEventTimeUpdatesOnAllEventTypes verifies that LastEventTime
// is updated for Add, Update, and Delete events.
func TestSingleWatcher_LastEventTimeUpdatesOnAllEventTypes(t *testing.T) {
	fakeClient := createFakeClientForSingleWatcher()
	cfg := validSingleWatcherConfig()

	w, err := NewSingle(cfg, fakeClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	testCases := []struct {
		name    string
		handler func()
	}{
		{
			name: "Add",
			handler: func() {
				w.handleAdd(createUnstructuredConfigMap("add-config", "ns-add", ""))
			},
		},
		{
			name: "Update",
			handler: func() {
				old := createUnstructuredConfigMap("update-config", "ns-update", "1")
				new := createUnstructuredConfigMap("update-config", "ns-update", "2")
				w.handleUpdate(old, new)
			},
		},
		{
			name: "Delete",
			handler: func() {
				w.handleDelete(createUnstructuredConfigMap("delete-config", "ns-delete", ""))
			},
		},
	}

	// Mark as synced
	w.synced.Store(true)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset timestamp
			w.lastEventTime.Store(0)

			// Verify zero before event
			if !w.LastEventTime().IsZero() {
				t.Error("expected LastEventTime() to be zero before event")
			}

			// Trigger event
			tc.handler()

			// Verify non-zero after event
			if w.LastEventTime().IsZero() {
				t.Errorf("expected LastEventTime() to be non-zero after %s event", tc.name)
			}
		})
	}
}

// TestSingleWatcher_LastWatchErrorInitiallyZero verifies that LastWatchError returns
// zero time when no watch errors have occurred.
func TestSingleWatcher_LastWatchErrorInitiallyZero(t *testing.T) {
	fakeClient := createFakeClientForSingleWatcher()
	cfg := validSingleWatcherConfig()

	w, err := NewSingle(cfg, fakeClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Initially, LastWatchError should be zero
	if !w.LastWatchError().IsZero() {
		t.Error("expected LastWatchError() to be zero initially")
	}
}

// TestSingleWatcher_LastWatchErrorUpdatesOnError verifies that LastWatchError is updated
// when a watch error occurs.
func TestSingleWatcher_LastWatchErrorUpdatesOnError(t *testing.T) {
	fakeClient := createFakeClientForSingleWatcher()
	cfg := validSingleWatcherConfig()

	w, err := NewSingle(cfg, fakeClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Initially zero
	if !w.LastWatchError().IsZero() {
		t.Error("expected LastWatchError() to be zero initially")
	}

	// Simulate a watch error
	beforeError := time.Now()
	w.handleWatchError(nil, errors.New("simulated watch error"))

	// Verify LastWatchError was updated
	lastWatchError := w.LastWatchError()
	if lastWatchError.IsZero() {
		t.Error("expected LastWatchError() to be non-zero after error")
	}

	// Verify it's within a reasonable range (allow 1 second tolerance due to Unix timestamp precision)
	// LastWatchError stores Unix seconds, so we truncate beforeError to second precision
	beforeErrorTruncated := beforeError.Truncate(time.Second)
	if lastWatchError.Before(beforeErrorTruncated) {
		t.Errorf("LastWatchError() %v is before %v", lastWatchError, beforeErrorTruncated)
	}
	if lastWatchError.After(beforeError.Add(time.Second)) {
		t.Errorf("LastWatchError() %v is more than 1 second after %v", lastWatchError, beforeError)
	}
}

// Helper functions for tests

// createFakeClientForSingleWatcher creates a fake Kubernetes client suitable for SingleWatcher tests.
func createFakeClientForSingleWatcher() *client.Client {
	fakeClientset := kubefake.NewSimpleClientset()
	fakeDynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	return client.NewFromClientset(fakeClientset, fakeDynamicClient, "default")
}

// validSingleWatcherConfig returns a valid SingleWatcherConfig for testing.
func validSingleWatcherConfig() *types.SingleWatcherConfig {
	return &types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			return nil
		},
	}
}

// createUnstructuredConfigMap creates an unstructured ConfigMap for testing.
func createUnstructuredConfigMap(name, namespace, resourceVersion string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
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

// TestSingleWatcher_SkipsResyncCallback verifies that Update events with unchanged
// resource version (resync events) don't trigger callbacks.
func TestSingleWatcher_SkipsResyncCallback(t *testing.T) {
	k8sClient := createFakeClientForSingleWatcher()

	callbackCount := 0
	cfg := types.SingleWatcherConfig{
		GVR: schema.GroupVersionResource{
			Group:    "",
			Version:  "v1",
			Resource: "configmaps",
		},
		Namespace: "default",
		Name:      "test-config",
		OnChange: func(obj interface{}) error {
			callbackCount++
			return nil
		},
	}

	w, err := NewSingle(&cfg, k8sClient)
	if err != nil {
		t.Fatalf("failed to create watcher: %v", err)
	}

	// Mark as synced
	w.synced.Store(true)

	// Simulate real update: old version "100" -> new version "101"
	oldResource := createUnstructuredConfigMap("test-config", "default", "100")
	newResource := createUnstructuredConfigMap("test-config", "default", "101")
	w.handleUpdate(oldResource, newResource)

	// Callback should have been called for real update
	if callbackCount != 1 {
		t.Errorf("expected 1 callback for real update, got %d", callbackCount)
	}

	// Simulate resync: same version "101" -> "101"
	w.handleUpdate(newResource, newResource)

	// Callback should NOT have been called for resync
	if callbackCount != 1 {
		t.Errorf("expected still 1 callback (resync should be skipped), got %d", callbackCount)
	}

	// Simulate another real update: version "101" -> "102"
	newerResource := createUnstructuredConfigMap("test-config", "default", "102")
	w.handleUpdate(newResource, newerResource)

	// Callback should have been called again for the second real update
	if callbackCount != 2 {
		t.Errorf("expected 2 callbacks after second real update, got %d", callbackCount)
	}
}
