package resourcewatcher

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/watcher"
)

func TestToGVR(t *testing.T) {
	tests := []struct {
		name    string
		wr      coreconfig.WatchedResource
		want    schema.GroupVersionResource
		wantErr bool
	}{
		{
			name: "core resource",
			wr: coreconfig.WatchedResource{
				APIVersion: "v1",
				Resources:  "services",
			},
			want: schema.GroupVersionResource{
				Group:    "",
				Version:  "v1",
				Resource: "services",
			},
		},
		{
			name: "networking resource",
			wr: coreconfig.WatchedResource{
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
			},
			want: schema.GroupVersionResource{
				Group:    "networking.k8s.io",
				Version:  "v1",
				Resource: "ingresses",
			},
		},
		{
			name: "discovery resource",
			wr: coreconfig.WatchedResource{
				APIVersion: "discovery.k8s.io/v1",
				Resources:  "endpointslices",
			},
			want: schema.GroupVersionResource{
				Group:    "discovery.k8s.io",
				Version:  "v1",
				Resource: "endpointslices",
			},
		},
		{
			name: "missing api_version",
			wr: coreconfig.WatchedResource{
				Resources: "ingresses",
			},
			wantErr: true,
		},
		{
			name: "missing kind",
			wr: coreconfig.WatchedResource{
				APIVersion: "v1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toGVR(&tt.wr)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseAPIVersion(t *testing.T) {
	tests := []struct {
		name        string
		apiVersion  string
		wantGroup   string
		wantVersion string
	}{
		{
			name:        "core resource",
			apiVersion:  "v1",
			wantGroup:   "",
			wantVersion: "v1",
		},
		{
			name:        "namespaced resource",
			apiVersion:  "networking.k8s.io/v1",
			wantGroup:   "networking.k8s.io",
			wantVersion: "v1",
		},
		{
			name:        "custom resource",
			apiVersion:  "example.com/v1alpha1",
			wantGroup:   "example.com",
			wantVersion: "v1alpha1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, version := parseAPIVersion(tt.apiVersion)
			assert.Equal(t, tt.wantGroup, group)
			assert.Equal(t, tt.wantVersion, version)
		})
	}
}

func TestMergeIgnoreFields(t *testing.T) {
	tests := []struct {
		name        string
		global      []string
		perResource []string
		want        []string
	}{
		{
			name:        "only global",
			global:      []string{"metadata.managedFields", "metadata.annotations"},
			perResource: nil,
			want:        []string{"metadata.managedFields", "metadata.annotations"},
		},
		{
			name:        "only per-resource",
			global:      nil,
			perResource: []string{"spec.template"},
			want:        []string{"spec.template"},
		},
		{
			name:        "merge without duplicates",
			global:      []string{"metadata.managedFields"},
			perResource: []string{"spec.template"},
			want:        []string{"metadata.managedFields", "spec.template"},
		},
		{
			name:        "deduplicate",
			global:      []string{"metadata.managedFields", "metadata.annotations"},
			perResource: []string{"metadata.managedFields", "spec.template"},
			want:        []string{"metadata.managedFields", "metadata.annotations", "spec.template"},
		},
		{
			name:        "both empty",
			global:      []string{},
			perResource: []string{},
			want:        []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeIgnoreFields(tt.global, tt.perResource)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetermineStoreType(t *testing.T) {
	tests := []struct {
		name        string
		storeConfig string
		want        types.StoreType
	}{
		{
			name:        "on-demand returns cached",
			storeConfig: "on-demand",
			want:        types.StoreTypeCached,
		},
		{
			name:        "full returns memory",
			storeConfig: "full",
			want:        types.StoreTypeMemory,
		},
		{
			name:        "empty returns memory",
			storeConfig: "",
			want:        types.StoreTypeMemory,
		},
		{
			name:        "other value returns memory",
			storeConfig: "some-other",
			want:        types.StoreTypeMemory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineStoreType(tt.storeConfig)
			assert.Equal(t, tt.want, got)
		})
	}
}

// mockStore implements types.Store for testing.
type mockStore struct {
	name string
}

func (m *mockStore) Get(...string) ([]interface{}, error)            { return nil, nil }
func (m *mockStore) List() ([]interface{}, error)                    { return nil, nil }
func (m *mockStore) Add(interface{}, []string) error                 { return nil }
func (m *mockStore) Update(interface{}, []string) error              { return nil }
func (m *mockStore) Delete(...string) error                          { return nil }
func (m *mockStore) Clear() error                                    { return nil }
func (m *mockStore) GetKeys(interface{}, []string) ([]string, error) { return nil, nil }

func (m *mockStore) Refresh(interface{}, []string, []string) (changed, deleted bool) {
	return false, false
}
func (m *mockStore) Count() int { return 0 }

// createTestComponent creates a minimal ResourceWatcherComponent for testing
// without requiring a Kubernetes cluster.
func createTestComponent() *ResourceWatcherComponent {
	return &ResourceWatcherComponent{
		watchers: map[string]*watcher.Watcher{
			"services":  nil,
			"ingresses": nil,
		},
		stores: map[string]types.Store{
			"services":  &mockStore{name: "services"},
			"ingresses": &mockStore{name: "ingresses"},
		},
		eventBus:  busevents.NewEventBus(10),
		k8sClient: nil,
		logger:    slog.Default(),
		synced:    make(map[string]bool),
	}
}

func TestGetStore_UnitTest(t *testing.T) {
	rwc := createTestComponent()

	t.Run("existing resource type", func(t *testing.T) {
		store := rwc.GetStore("services")
		assert.NotNil(t, store)
	})

	t.Run("another existing resource type", func(t *testing.T) {
		store := rwc.GetStore("ingresses")
		assert.NotNil(t, store)
	})

	t.Run("non-existent resource type", func(t *testing.T) {
		store := rwc.GetStore("pods")
		assert.Nil(t, store)
	})

	t.Run("empty string", func(t *testing.T) {
		store := rwc.GetStore("")
		assert.Nil(t, store)
	})
}

func TestGetAllStores_UnitTest(t *testing.T) {
	rwc := createTestComponent()

	stores := rwc.GetAllStores()

	t.Run("returns correct count", func(t *testing.T) {
		assert.Len(t, stores, 2)
	})

	t.Run("contains expected stores", func(t *testing.T) {
		assert.NotNil(t, stores["services"])
		assert.NotNil(t, stores["ingresses"])
	})

	t.Run("returns copy not original", func(t *testing.T) {
		// Modify returned map
		stores["services"] = nil
		// Original should be unchanged
		assert.NotNil(t, rwc.stores["services"])
	})
}

func TestIsSynced_UnitTest(t *testing.T) {
	rwc := createTestComponent()

	t.Run("initially not synced", func(t *testing.T) {
		assert.False(t, rwc.IsSynced("services"))
		assert.False(t, rwc.IsSynced("ingresses"))
	})

	t.Run("synced after marking", func(t *testing.T) {
		rwc.syncMu.Lock()
		rwc.synced["services"] = true
		rwc.syncMu.Unlock()

		assert.True(t, rwc.IsSynced("services"))
		assert.False(t, rwc.IsSynced("ingresses"))
	})

	t.Run("non-existent resource returns false", func(t *testing.T) {
		assert.False(t, rwc.IsSynced("pods"))
	})
}

func TestAllSynced_UnitTest(t *testing.T) {
	rwc := createTestComponent()

	t.Run("initially not all synced", func(t *testing.T) {
		assert.False(t, rwc.AllSynced())
	})

	t.Run("partially synced returns false", func(t *testing.T) {
		rwc.syncMu.Lock()
		rwc.synced["services"] = true
		rwc.syncMu.Unlock()

		assert.False(t, rwc.AllSynced())
	})

	t.Run("all synced returns true", func(t *testing.T) {
		rwc.syncMu.Lock()
		rwc.synced["services"] = true
		rwc.synced["ingresses"] = true
		rwc.syncMu.Unlock()

		assert.True(t, rwc.AllSynced())
	})
}

func TestAllSynced_EmptyWatchers(t *testing.T) {
	rwc := &ResourceWatcherComponent{
		watchers: map[string]*watcher.Watcher{},
		stores:   map[string]types.Store{},
		synced:   make(map[string]bool),
		syncMu:   sync.RWMutex{},
	}

	// With no watchers, AllSynced should return true (vacuously true)
	assert.True(t, rwc.AllSynced())
}

func TestAllSynced_ConcurrentAccess(t *testing.T) {
	rwc := createTestComponent()

	// Test concurrent reads and writes
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			rwc.syncMu.Lock()
			rwc.synced["services"] = i%2 == 0
			rwc.syncMu.Unlock()
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = rwc.AllSynced()
			_ = rwc.IsSynced("services")
		}
		done <- true
	}()

	// Wait for both to complete
	<-done
	<-done
}

func TestDetermineNamespace(t *testing.T) {
	// Create a dummy client - Namespace() returns empty string for uninitialized client
	dummyClient := &client.Client{}

	tests := []struct {
		name             string
		resourceTypeName string
		want             string
	}{
		{
			name:             "haproxy-pods returns client namespace",
			resourceTypeName: "haproxy-pods",
			want:             "", // Empty because dummyClient.Namespace() returns ""
		},
		{
			name:             "services returns empty (cluster-wide)",
			resourceTypeName: "services",
			want:             "",
		},
		{
			name:             "ingresses returns empty (cluster-wide)",
			resourceTypeName: "ingresses",
			want:             "",
		},
		{
			name:             "pods returns empty (cluster-wide)",
			resourceTypeName: "pods",
			want:             "",
		},
		{
			name:             "custom resource returns empty (cluster-wide)",
			resourceTypeName: "my-custom-resource",
			want:             "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineNamespace(tt.resourceTypeName, dummyClient)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestStart_EmptyWatchers verifies Start behavior with no watchers configured.
func TestStart_EmptyWatchers(t *testing.T) {
	rwc := &ResourceWatcherComponent{
		watchers: map[string]*watcher.Watcher{},
		stores:   map[string]types.Store{},
		synced:   make(map[string]bool),
		logger:   slog.Default(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start should return after context cancellation
	done := make(chan error, 1)
	go func() {
		done <- rwc.Start(ctx)
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Start() did not return after context cancellation")
	}
}

// TestWaitForAllSync_EmptyWatchers verifies WaitForAllSync with no watchers.
func TestWaitForAllSync_EmptyWatchers(t *testing.T) {
	rwc := &ResourceWatcherComponent{
		watchers: map[string]*watcher.Watcher{},
		stores:   map[string]types.Store{},
		synced:   make(map[string]bool),
		logger:   slog.Default(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// With no watchers, WaitForAllSync should return immediately
	err := rwc.WaitForAllSync(ctx)
	require.NoError(t, err)
}

// TestWaitForAllSync_ContextCancelled verifies WaitForAllSync respects context cancellation.
func TestWaitForAllSync_ContextCancelled(t *testing.T) {
	rwc := &ResourceWatcherComponent{
		watchers: map[string]*watcher.Watcher{},
		stores:   map[string]types.Store{},
		synced:   make(map[string]bool),
		logger:   slog.Default(),
	}

	// Create already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should return immediately without error (no watchers to wait for)
	err := rwc.WaitForAllSync(ctx)
	require.NoError(t, err)
}

func TestNew_NilParameters(t *testing.T) {
	cfg := &coreconfig.Config{}
	bus := busevents.NewEventBus(10)
	logger := slog.Default()

	// Create a mock k8s client (nil is okay for these tests since we're testing nil validation)
	// We use a placeholder that's non-nil but won't be used
	dummyClient := &client.Client{}

	tests := []struct {
		name      string
		cfg       *coreconfig.Config
		k8sClient *client.Client
		bus       *busevents.EventBus
		logger    *slog.Logger
		wantErr   string
	}{
		{
			name:      "nil config",
			cfg:       nil,
			k8sClient: dummyClient,
			bus:       bus,
			logger:    logger,
			wantErr:   "config is nil",
		},
		{
			name:      "nil k8s client",
			cfg:       cfg,
			k8sClient: nil,
			bus:       bus,
			logger:    logger,
			wantErr:   "k8s client is nil",
		},
		{
			name:      "nil event bus",
			cfg:       cfg,
			k8sClient: dummyClient,
			bus:       nil,
			logger:    logger,
			wantErr:   "event bus is nil",
		},
		{
			name:      "nil logger",
			cfg:       cfg,
			k8sClient: dummyClient,
			bus:       bus,
			logger:    nil,
			wantErr:   "logger is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg, tt.k8sClient, tt.bus, tt.logger)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestNew_EmptyConfig(t *testing.T) {
	t.Skip("Requires Kubernetes cluster - run as integration test")

	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{},
	}
	k8sClient, err := client.New(client.Config{})
	require.NoError(t, err)
	bus := busevents.NewEventBus(10)
	logger := slog.Default()

	rwc, err := New(cfg, k8sClient, bus, logger)
	require.NoError(t, err)
	require.NotNil(t, rwc)

	// Should have no watchers for empty config
	assert.Empty(t, rwc.watchers)
	assert.Empty(t, rwc.stores)
}

func TestNew_InvalidResource(t *testing.T) {
	t.Skip("Requires Kubernetes cluster - run as integration test")

	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"invalid": {
				// Missing APIVersion and Kind
				IndexBy: []string{"metadata.namespace"},
			},
		},
	}
	k8sClient, err := client.New(client.Config{})
	require.NoError(t, err)
	bus := busevents.NewEventBus(10)
	logger := slog.Default()

	_, err = New(cfg, k8sClient, bus, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid resource")
}

func TestGetStore(t *testing.T) {
	t.Skip("Requires Kubernetes cluster - run as integration test")

	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
	}
	k8sClient, err := client.New(client.Config{})
	require.NoError(t, err)
	bus := busevents.NewEventBus(10)
	logger := slog.Default()

	rwc, err := New(cfg, k8sClient, bus, logger)
	require.NoError(t, err)

	// Existing resource type
	store := rwc.GetStore("services")
	assert.NotNil(t, store)

	// Non-existent resource type
	store = rwc.GetStore("ingresses")
	assert.Nil(t, store)
}

func TestGetAllStores(t *testing.T) {
	t.Skip("Requires Kubernetes cluster - run as integration test")

	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace"},
			},
			"pods": {
				APIVersion: "v1",
				Resources:  "pods",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
	}
	k8sClient, err := client.New(client.Config{})
	require.NoError(t, err)
	bus := busevents.NewEventBus(10)
	logger := slog.Default()

	rwc, err := New(cfg, k8sClient, bus, logger)
	require.NoError(t, err)

	stores := rwc.GetAllStores()
	assert.Len(t, stores, 2)
	assert.NotNil(t, stores["services"])
	assert.NotNil(t, stores["pods"])

	// Verify it returns a copy (modifying return value doesn't affect internal state)
	stores["services"] = nil
	assert.NotNil(t, rwc.stores["services"])
}

func TestSyncTracking(t *testing.T) {
	t.Skip("Requires Kubernetes cluster - run as integration test")

	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace"},
			},
			"pods": {
				APIVersion: "v1",
				Resources:  "pods",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
	}
	k8sClient, err := client.New(client.Config{})
	require.NoError(t, err)
	bus := busevents.NewEventBus(10)
	logger := slog.Default()

	rwc, err := New(cfg, k8sClient, bus, logger)
	require.NoError(t, err)

	// Initially nothing is synced
	assert.False(t, rwc.IsSynced("services"))
	assert.False(t, rwc.IsSynced("pods"))
	assert.False(t, rwc.AllSynced())

	// Simulate OnSyncComplete callback for services
	rwc.syncMu.Lock()
	rwc.synced["services"] = true
	rwc.syncMu.Unlock()

	assert.True(t, rwc.IsSynced("services"))
	assert.False(t, rwc.IsSynced("pods"))
	assert.False(t, rwc.AllSynced())

	// Simulate OnSyncComplete callback for pods
	rwc.syncMu.Lock()
	rwc.synced["pods"] = true
	rwc.syncMu.Unlock()

	assert.True(t, rwc.IsSynced("services"))
	assert.True(t, rwc.IsSynced("pods"))
	assert.True(t, rwc.AllSynced())
}

// TestEventPublishing verifies that the component publishes correct events.
// This is an integration test that requires a bit more setup.
func TestEventPublishing(t *testing.T) {
	t.Skip("Integration test - requires real k8s client or extensive mocking")

	// This test would verify:
	// 1. ResourceIndexUpdatedEvent is published on resource changes
	// 2. ResourceSyncCompleteEvent is published on initial sync
	// 3. Events contain correct resource type names and stats
}

// TestStart verifies that Start() begins watching and waits for context cancellation.
func TestStart(t *testing.T) {
	t.Skip("Requires Kubernetes cluster - run as integration test")

	cfg := &coreconfig.Config{
		WatchedResources: map[string]coreconfig.WatchedResource{
			"services": {
				APIVersion: "v1",
				Resources:  "services",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
	}
	k8sClient, err := client.New(client.Config{})
	require.NoError(t, err)
	bus := busevents.NewEventBus(10)
	logger := slog.Default()

	rwc, err := New(cfg, k8sClient, bus, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start should not block
	done := make(chan error, 1)
	go func() {
		done <- rwc.Start(ctx)
	}()

	// Verify it completes when context is cancelled
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Start() did not return after context cancellation")
	}
}
