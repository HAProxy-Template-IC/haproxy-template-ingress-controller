// Package watcher provides Kubernetes resource watching with indexing,
// field filtering, and debounced callbacks.
package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/client"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// SingleWatcher watches a single named Kubernetes resource and invokes callbacks on changes.
//
// Unlike the bulk Watcher (which watches collections of resources with indexing and debouncing),
// SingleWatcher is optimized for watching one specific resource:
//   - No indexing or store (just the current resource)
//   - No debouncing (immediate callbacks)
//   - Simpler API (resource object in callback, not Store)
//
// Callback Behavior:
//   - During initial sync: All events (Add/Update/Delete) are suppressed (no callbacks)
//   - After sync completes: All events trigger callbacks immediately
//   - Use WaitForSync() to ensure initial state is loaded before relying on callbacks
//
// Thread Safety:
//   - Safe for concurrent use
//   - Callbacks may be invoked from multiple goroutines
//   - Users should ensure callbacks are thread-safe
//
// This is ideal for watching configuration or credentials in a specific ConfigMap or Secret.
type SingleWatcher struct {
	config    types.SingleWatcherConfig
	client    *client.Client
	informer  cache.SharedIndexInformer
	stopCh    chan struct{}
	synced    atomic.Bool   // True after initial sync completes
	syncCh    chan struct{} // Closed when sync completes
	stopOnce  sync.Once     // Ensures Stop() is idempotent
	startOnce sync.Once     // Ensures Start() is idempotent
	started   atomic.Bool   // True if Start() has been called

	// Health tracking for diagnostics
	lastEventTime  atomic.Int64 // Unix timestamp of last event received
	lastWatchError atomic.Int64 // Unix timestamp of last watch error
}

// NewSingle creates a new single-resource watcher with the provided configuration.
//
// Returns an error if:
//   - Configuration validation fails
//   - Client is nil
//   - Informer creation fails
//
// Example:
//
//	watcher, err := watcher.NewSingle(types.SingleWatcherConfig{
//	    GVR: schema.GroupVersionResource{
//	        Group:    "",
//	        Version:  "v1",
//	        Resource: "configmaps",
//	    },
//	    Namespace: "default",
//	    Name:      "haproxy-config",
//	    OnChange: func(obj interface{}) error {
//	        cm := obj.(*corev1.ConfigMap)
//	        // Process configuration
//	        return nil
//	    },
//	}, k8sClient)
func NewSingle(cfg *types.SingleWatcherConfig, k8sClient *client.Client) (*SingleWatcher, error) {
	// Set defaults
	cfg.SetDefaults()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if k8sClient == nil {
		return nil, fmt.Errorf("k8s client is nil")
	}

	w := &SingleWatcher{
		config: *cfg,
		client: k8sClient,
		stopCh: make(chan struct{}),
		syncCh: make(chan struct{}),
		// synced defaults to false (atomic.Bool zero value)
	}

	// Create informer
	if err := w.createInformer(); err != nil {
		return nil, fmt.Errorf("failed to create informer: %w", err)
	}

	return w, nil
}

// resyncPeriod is the interval at which the informer re-lists resources from the API server.
// This provides resilience against missed watch events during network issues.
// 30 seconds is the recommended value used by sample-controller and production operators.
const resyncPeriod = 30 * time.Second

// createInformer creates a SharedIndexInformer for the single watched resource.
func (w *SingleWatcher) createInformer() error {
	// Get dynamic client
	dynamicClient := w.client.DynamicClient()
	if dynamicClient == nil {
		return fmt.Errorf("dynamic client is nil")
	}

	// Create informer factory with field selector for specific resource name.
	// The resyncPeriod enables periodic re-listing which recovers from missed watch events.
	informerFactory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		dynamicClient,
		resyncPeriod,
		w.config.Namespace,
		func(options *metav1.ListOptions) {
			// Filter by resource name using field selector
			options.FieldSelector = fields.OneTermEqualSelector("metadata.name", w.config.Name).String()
		},
	)

	// Get informer for resource
	w.informer = informerFactory.ForResource(w.config.GVR).Informer()

	// Register watch error handler for observability.
	// The Reflector handles retry automatically with exponential backoff,
	// but we need visibility into when watch connections fail.
	if err := w.informer.SetWatchErrorHandler(w.handleWatchError); err != nil {
		return fmt.Errorf("failed to set watch error handler: %w", err)
	}

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

// handleWatchError is called when the watch connection drops.
// The Reflector will automatically retry with exponential backoff after this handler returns.
func (w *SingleWatcher) handleWatchError(_ *cache.Reflector, err error) {
	w.lastWatchError.Store(time.Now().Unix())
	slog.Warn("SingleWatcher watch error (Reflector will retry)",
		"gvr", w.config.GVR.String(),
		"namespace", w.config.Namespace,
		"name", w.config.Name,
		"error", err)
}

// handleAdd handles resource addition events.
func (w *SingleWatcher) handleAdd(obj interface{}) {
	// Track last event time for health monitoring
	w.lastEventTime.Store(time.Now().Unix())

	slog.Debug("SingleWatcher received add event",
		"gvr", w.config.GVR.String(),
		"synced", w.synced.Load())

	resource := w.convertToUnstructured(obj)
	if resource == nil {
		slog.Warn("SingleWatcher.handleAdd: resource conversion returned nil")
		return
	}

	slog.Debug("SingleWatcher processing add",
		"resource_name", resource.GetName(),
		"resource_namespace", resource.GetNamespace(),
		"resource_version", resource.GetResourceVersion())

	// Skip callback during initial sync - we don't want to trigger change events
	// for resources that already exist. Only invoke callback for real additions
	// that happen after sync completes.
	if !w.synced.Load() {
		// During initial sync, skip callback
		slog.Debug("SingleWatcher skipping add callback - not yet synced")
		return
	}

	// Invoke callback immediately (no debouncing for single resource)
	if w.config.OnChange != nil {
		if err := w.config.OnChange(resource); err != nil {
			// Log error but don't propagate - callback failures shouldn't stop the watcher
			slog.Warn("Watcher callback failed on Add event",
				"error", err,
				"resource_name", resource.GetName(),
				"resource_namespace", resource.GetNamespace(),
				"resource_kind", resource.GetKind())
		} else {
			slog.Debug("SingleWatcher add callback succeeded",
				"resource_name", resource.GetName(),
				"resource_version", resource.GetResourceVersion())
		}
	}
}

// handleUpdate handles resource update events.
func (w *SingleWatcher) handleUpdate(oldObj, newObj interface{}) {
	// Track last event time for health monitoring
	w.lastEventTime.Store(time.Now().Unix())

	slog.Debug("SingleWatcher received update event",
		"gvr", w.config.GVR.String(),
		"synced", w.synced.Load())

	oldResource := w.convertToUnstructured(oldObj)
	resource := w.convertToUnstructured(newObj)
	if resource == nil {
		slog.Warn("SingleWatcher.handleUpdate: resource conversion returned nil")
		return
	}

	// Skip callback if resource version hasn't changed (resync event).
	// This happens during resync - the informer re-lists all resources and
	// triggers Update events even when nothing has changed. We detect this
	// by comparing resource versions and skip the callback to avoid
	// unnecessary reconciliation cycles.
	// Note: Only skip if resource version is non-empty (real Kubernetes resources
	// always have a version, but test mocks might not).
	oldVersion := ""
	if oldResource != nil {
		oldVersion = oldResource.GetResourceVersion()
	}
	newVersion := resource.GetResourceVersion()
	if oldVersion != "" && newVersion != "" && oldVersion == newVersion {
		slog.Debug("SingleWatcher skipping update callback - resource version unchanged (resync)",
			"resource_name", resource.GetName(),
			"resource_version", resource.GetResourceVersion())
		return
	}

	slog.Debug("SingleWatcher processing update",
		"resource_name", resource.GetName(),
		"resource_namespace", resource.GetNamespace(),
		"resource_version", resource.GetResourceVersion())

	// Skip callback during initial sync - we don't want to trigger change events
	// for resources that are updating during the sync process. Only invoke callback
	// for real updates that happen after sync completes.
	if !w.synced.Load() {
		// During initial sync, skip callback
		slog.Debug("SingleWatcher skipping callback - not yet synced")
		return
	}

	// Invoke callback immediately (no debouncing for single resource)
	if w.config.OnChange != nil {
		if err := w.config.OnChange(resource); err != nil {
			// Log error but don't propagate - callback failures shouldn't stop the watcher
			slog.Warn("Watcher callback failed on Update event",
				"error", err,
				"resource_name", resource.GetName(),
				"resource_namespace", resource.GetNamespace(),
				"resource_kind", resource.GetKind())
		} else {
			slog.Debug("SingleWatcher update callback succeeded",
				"resource_name", resource.GetName(),
				"resource_version", resource.GetResourceVersion())
		}
	}
}

// handleDelete handles resource deletion events.
func (w *SingleWatcher) handleDelete(obj interface{}) {
	// Track last event time for health monitoring
	w.lastEventTime.Store(time.Now().Unix())

	slog.Debug("SingleWatcher received delete event",
		"gvr", w.config.GVR.String(),
		"synced", w.synced.Load())

	resource := w.convertToUnstructured(obj)
	if resource == nil {
		// Handle DeletedFinalStateUnknown
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			resource = w.convertToUnstructured(tombstone.Obj)
		}
		if resource == nil {
			slog.Warn("SingleWatcher.handleDelete: resource conversion returned nil")
			return
		}
	}

	slog.Debug("SingleWatcher processing delete",
		"resource_name", resource.GetName(),
		"resource_namespace", resource.GetNamespace(),
		"resource_version", resource.GetResourceVersion())

	// Skip callback during initial sync for consistency (unlikely scenario but possible)
	// Only invoke callback for real deletions that happen after sync completes.
	if !w.synced.Load() {
		// During initial sync, skip callback
		slog.Debug("SingleWatcher skipping delete callback - not yet synced")
		return
	}

	// Invoke callback to signal deletion
	if w.config.OnChange != nil {
		if err := w.config.OnChange(resource); err != nil {
			// Log error but don't propagate - callback failures shouldn't stop the watcher
			slog.Warn("Watcher callback failed on Delete event",
				"error", err,
				"resource_name", resource.GetName(),
				"resource_namespace", resource.GetNamespace(),
				"resource_kind", resource.GetKind())
		} else {
			slog.Debug("SingleWatcher delete callback succeeded",
				"resource_name", resource.GetName(),
				"resource_version", resource.GetResourceVersion())
		}
	}
}

// convertToUnstructured converts a resource to *unstructured.Unstructured.
func (w *SingleWatcher) convertToUnstructured(obj interface{}) *unstructured.Unstructured {
	if u, ok := obj.(*unstructured.Unstructured); ok {
		return u
	}
	return nil
}

// Start begins watching the resource.
//
// This method blocks until the context is cancelled or an error occurs.
// Initial sync is performed before continuing.
//
// This method is idempotent - calling it multiple times is safe. The initialization
// logic (starting informer, syncing) only runs once, but each call will block until
// the context is cancelled.
func (w *SingleWatcher) Start(ctx context.Context) error {
	var startErr error

	// Ensure initialization only happens once
	w.startOnce.Do(func() {
		w.started.Store(true)

		// Start informer
		go w.informer.Run(w.stopCh)

		// Wait for cache sync
		if !cache.WaitForCacheSync(ctx.Done(), w.informer.HasSynced) {
			startErr = fmt.Errorf("failed to sync cache")
			return
		}

		// Mark sync as complete atomically
		w.synced.Store(true)
		close(w.syncCh)
	})

	// Return any error from initialization
	if startErr != nil {
		return startErr
	}

	// Wait for context cancellation
	<-ctx.Done()

	return w.Stop()
}

// Stop stops watching the resource.
//
// This method is idempotent and safe to call multiple times.
func (w *SingleWatcher) Stop() error {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	return nil
}

// WaitForSync blocks until initial synchronization is complete.
//
// This is useful when you need to wait for the watcher to have the current
// state of the resource before performing operations that depend on it.
//
// Returns an error if sync fails or context is cancelled.
//
// Example:
//
//	watcher, _ := watcher.NewSingle(cfg, client)
//	go watcher.Start(ctx)
//
//	err := watcher.WaitForSync(ctx)
//	if err != nil {
//	    slog.Error("watcher sync failed", "error", err)
//	    os.Exit(1)
//	}
//	slog.Info("Watcher synced, resource is available")
func (w *SingleWatcher) WaitForSync(ctx context.Context) error {
	// Wait for sync channel to close or context to be cancelled
	select {
	case <-w.syncCh:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("context cancelled while waiting for sync")
	}
}

// IsSynced returns true if initial synchronization has completed.
//
// This provides a non-blocking way to check if the watcher has synced.
func (w *SingleWatcher) IsSynced() bool {
	return w.synced.Load()
}

// IsStarted returns true if Start() has been called.
//
// This provides a non-blocking way to check if the watcher has been started.
func (w *SingleWatcher) IsStarted() bool {
	return w.started.Load()
}

// LastEventTime returns the time when the last event was received.
// Returns zero time if no events have been received yet.
//
// This is useful for health checks to detect stalled watches where
// no events are being received despite the resync period.
func (w *SingleWatcher) LastEventTime() time.Time {
	ts := w.lastEventTime.Load()
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}

// LastWatchError returns the time when the last watch error occurred.
// Returns zero time if no watch errors have occurred.
//
// Watch errors trigger automatic retry with exponential backoff by the Reflector.
// This method is useful for observability and debugging connection issues.
func (w *SingleWatcher) LastWatchError() time.Time {
	ts := w.lastWatchError.Load()
	if ts == 0 {
		return time.Time{}
	}
	return time.Unix(ts, 0)
}
