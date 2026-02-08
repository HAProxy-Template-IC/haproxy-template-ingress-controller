// Package watcher provides Kubernetes resource watching with indexing,
// field filtering, and debounced callbacks.
//
// This package integrates all k8s subpackages to provide a high-level
// interface for watching Kubernetes resources and reacting to changes.
package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Watcher watches Kubernetes resources and maintains an indexed store.
//
// Resources are:
// - Filtered by namespace and label selector
// - Filtered by field selector (client-side JSONPath evaluation)
// - Indexed using JSONPath expressions for O(1) lookups
// - Filtered to remove unnecessary fields
// - Stored in memory or API-backed cache
//
// Changes are debounced and delivered via callback with aggregated statistics.
type Watcher struct {
	config               types.WatcherConfig
	client               *client.Client
	indexer              *indexer.Indexer
	fieldSelectorMatcher *indexer.FieldSelectorMatcher // nil if no field selector configured
	store                types.Store
	debouncer            *Debouncer
	informer             cache.SharedIndexInformer
	stopCh               chan struct{}
	synced               bool // True after initial sync completes
	syncMu               sync.RWMutex
	initialCount         int // Number of resources loaded during initial sync
	logger               *slog.Logger
}

// New creates a new resource watcher with the provided configuration.
//
// Returns an error if:
//   - Configuration validation fails
//   - Client creation fails
//   - Indexer creation fails
//   - Store creation fails
//
// Example:
//
//	watcher, err := watcher.New(types.WatcherConfig{
//	    GVR: schema.GroupVersionResource{
//	        Group:    "networking.k8s.io",
//	        Version:  "v1",
//	        Resource: "ingresses",
//	    },
//	    IndexBy: []string{"metadata.namespace", "metadata.name"},
//	    IgnoreFields: []string{"metadata.managedFields"},
//	    StoreType: types.StoreTypeMemory,
//	    DebounceInterval: 500 * time.Millisecond,
//	    OnChange: func(store types.Store, stats types.ChangeStats) {
//	        slog.Info("Resources changed", "stats", stats)
//	    },
//	})
//
//nolint:gocritic // hugeParam: Config passed by value to prevent external mutation
func New(cfg types.WatcherConfig, k8sClient *client.Client, logger *slog.Logger) (*Watcher, error) {
	// Set defaults
	cfg.SetDefaults()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Handle namespaced watch
	if cfg.NamespacedWatch {
		cfg.Namespace = k8sClient.Namespace()
	}

	// Create indexer
	idx, err := indexer.New(indexer.Config{
		IndexBy:      cfg.IndexBy,
		IgnoreFields: cfg.IgnoreFields,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create indexer: %w", err)
	}

	// Create field selector matcher if configured
	var fieldSelectorMatcher *indexer.FieldSelectorMatcher
	if cfg.FieldSelector != "" {
		fieldSelectorMatcher, err = indexer.NewFieldSelectorMatcher(cfg.FieldSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to create field selector matcher: %w", err)
		}
	}

	// Create store based on type
	var resourceStore types.Store
	switch cfg.StoreType {
	case types.StoreTypeMemory:
		resourceStore = store.NewMemoryStore(len(cfg.IndexBy))

	case types.StoreTypeCached:
		dynamicClient := k8sClient.DynamicClient()
		if dynamicClient == nil {
			return nil, fmt.Errorf("cached store requires dynamic client")
		}

		cachedStore, err := store.NewCachedStore(&store.CachedStoreConfig{
			NumKeys:   len(cfg.IndexBy),
			CacheTTL:  cfg.CacheTTL,
			Client:    dynamicClient,
			GVR:       cfg.GVR,
			Namespace: cfg.Namespace,
			Indexer:   idx,
			Logger:    logger,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create cached store: %w", err)
		}
		resourceStore = cachedStore

	default:
		return nil, fmt.Errorf("unsupported store type: %v", cfg.StoreType)
	}

	// Create debouncer
	// Suppress callbacks during sync if CallOnChangeDuringSync is false (default)
	suppressDuringSync := !cfg.CallOnChangeDuringSync
	debouncer := NewDebouncer(cfg.DebounceInterval, cfg.OnChange, resourceStore, suppressDuringSync)

	w := &Watcher{
		config:               cfg,
		client:               k8sClient,
		indexer:              idx,
		fieldSelectorMatcher: fieldSelectorMatcher,
		store:                resourceStore,
		debouncer:            debouncer,
		stopCh:               make(chan struct{}),
		synced:               false,
		initialCount:         0,
		logger:               logger,
	}

	// Create informer
	if err := w.createInformer(); err != nil {
		return nil, fmt.Errorf("failed to create informer: %w", err)
	}

	return w, nil
}

// createInformer creates a SharedIndexInformer for the watched resource.
func (w *Watcher) createInformer() error {
	// Get dynamic client
	dynamicClient := w.client.DynamicClient()
	if dynamicClient == nil {
		return fmt.Errorf("dynamic client is nil")
	}

	// Create informer factory
	var informerFactory dynamicinformer.DynamicSharedInformerFactory
	if w.config.Namespace != "" {
		informerFactory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(
			dynamicClient,
			0, // No resync
			w.config.Namespace,
			func(options *metav1.ListOptions) {
				w.applyListOptions(options)
			},
		)
	} else {
		informerFactory = dynamicinformer.NewFilteredDynamicSharedInformerFactory(
			dynamicClient,
			0, // No resync
			metav1.NamespaceAll,
			func(options *metav1.ListOptions) {
				w.applyListOptions(options)
			},
		)
	}

	// Get informer for resource
	w.informer = informerFactory.ForResource(w.config.GVR).Informer()

	// Add event handlers
	_, err := w.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    w.handleAdd,
		UpdateFunc: w.handleUpdate,
		DeleteFunc: w.handleDelete,
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}

	return nil
}

// applyListOptions applies label selector to list options.
func (w *Watcher) applyListOptions(options *metav1.ListOptions) {
	if w.config.LabelSelector != nil {
		selector, err := metav1.LabelSelectorAsSelector(w.config.LabelSelector)
		if err != nil {
			// This should never happen with valid MatchLabels
			return
		}
		options.LabelSelector = selector.String()
	}
}

// Start begins watching resources.
//
// This method blocks until the context is cancelled or an error occurs.
// Initial sync is performed before continuing, and OnSyncComplete is called if configured.
func (w *Watcher) Start(ctx context.Context) error {
	// Start informer
	go w.informer.Run(w.stopCh)

	// Wait for cache sync
	if !cache.WaitForCacheSync(ctx.Done(), w.informer.HasSynced) {
		return fmt.Errorf("failed to sync cache")
	}

	// Mark sync as complete and disable sync mode
	w.markSyncComplete()

	// Call OnSyncComplete if configured
	if w.config.OnSyncComplete != nil {
		w.config.OnSyncComplete(w.store, w.initialCount)
	}

	// Wait for context cancellation
	<-ctx.Done()

	return w.Stop()
}

// Stop stops watching resources.
func (w *Watcher) Stop() error {
	// Stop informer
	close(w.stopCh)

	// Flush pending changes
	w.debouncer.Flush()

	return nil
}

// Store returns the underlying store for direct access.
func (w *Watcher) Store() types.Store {
	return w.store
}

// WaitForSync blocks until initial synchronization is complete.
//
// This is useful when you need to wait for the store to be fully populated
// before performing operations that depend on complete data.
//
// Returns:
//   - The number of resources loaded during initial sync
//   - An error if sync fails or context is cancelled
//
// Example:
//
//	watcher, _ := watcher.New(cfg, client)
//	go watcher.Start(ctx)
//
//	count, err := watcher.WaitForSync(ctx)
//	if err != nil {
//	    slog.Error("watcher sync failed", "error", err)
//	    os.Exit(1)
//	}
//	slog.Info("Watcher synced", "resource_count", count)
func (w *Watcher) WaitForSync(ctx context.Context) (int, error) {
	// Wait for informer sync
	if !cache.WaitForCacheSync(ctx.Done(), w.informer.HasSynced) {
		return 0, fmt.Errorf("failed to sync cache")
	}

	// Mark sync as complete (idempotent - safe if Start() already did this)
	count := w.markSyncComplete()

	return count, nil
}

// IsSynced returns true if initial synchronization has completed.
//
// This provides a non-blocking way to check if the store is fully populated.
func (w *Watcher) IsSynced() bool {
	w.syncMu.RLock()
	defer w.syncMu.RUnlock()

	return w.synced
}

// markSyncComplete transitions the watcher from syncing to synced state.
// It is safe to call multiple times - only the first call takes effect.
// Returns the number of resources loaded during initial sync.
func (w *Watcher) markSyncComplete() int {
	w.syncMu.Lock()
	defer w.syncMu.Unlock()

	if w.synced {
		return w.initialCount
	}

	w.synced = true
	w.initialCount = w.debouncer.GetInitialCount()
	w.debouncer.SetSyncMode(false)

	return w.initialCount
}

// ForceSync forces an immediate callback with current statistics.
func (w *Watcher) ForceSync() {
	w.debouncer.Flush()
}

// Ensure watch.Interface compatibility for informer.
var _ watch.Interface = (*watch.FakeWatcher)(nil)
