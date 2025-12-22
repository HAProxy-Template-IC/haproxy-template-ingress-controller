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

// Package resourcewatcher provides the ResourceWatcherComponent that creates and manages
// watchers for all Kubernetes resources defined in the controller configuration.
//
// The component:
//   - Creates a k8s.Watcher for each resource type in Config.WatchedResources
//   - Merges global WatchedResourcesIgnoreFields with per-resource ignore fields
//   - Publishes ResourceIndexUpdatedEvent on resource changes
//   - Publishes ResourceSyncCompleteEvent when a resource type completes initial sync
//   - Provides access to stores for template rendering
package resourcewatcher

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"haproxy-template-ic/pkg/controller/events"
	coreconfig "haproxy-template-ic/pkg/core/config"
	busevents "haproxy-template-ic/pkg/events"
	"haproxy-template-ic/pkg/k8s/client"
	"haproxy-template-ic/pkg/k8s/types"
	"haproxy-template-ic/pkg/k8s/watcher"
)

// ResourceWatcherComponent creates and manages watchers for all configured resources.
type ResourceWatcherComponent struct {
	watchers  map[string]*watcher.Watcher // resourceTypeName -> watcher
	stores    map[string]types.Store      // resourceTypeName -> store
	eventBus  *busevents.EventBus
	k8sClient *client.Client
	logger    *slog.Logger

	syncMu sync.RWMutex
	synced map[string]bool // resourceTypeName -> synced
}

// New creates a new ResourceWatcherComponent.
//
// For each entry in cfg.WatchedResources, this creates a k8s.Watcher that:
//   - Watches the specified Kubernetes resource type
//   - Indexes resources using the configured IndexBy expressions
//   - Filters fields by merging global WatchedResourcesIgnoreFields with per-resource ignore fields
//   - Publishes events to the EventBus on resource changes
//
// Returns an error if:
//   - Configuration validation fails
//   - Watcher creation fails for any resource type
func New(
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	eventBus *busevents.EventBus,
	logger *slog.Logger,
) (*ResourceWatcherComponent, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if k8sClient == nil {
		return nil, fmt.Errorf("k8s client is nil")
	}
	if eventBus == nil {
		return nil, fmt.Errorf("event bus is nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is nil")
	}

	rwc := &ResourceWatcherComponent{
		watchers:  make(map[string]*watcher.Watcher),
		stores:    make(map[string]types.Store),
		eventBus:  eventBus,
		k8sClient: k8sClient,
		logger:    logger,
		synced:    make(map[string]bool),
	}

	// Auto-inject HAProxy pods watcher based on PodSelector
	// This watcher is always created regardless of WatchedResources configuration
	resourcesWithHAProxyPods := make(map[string]coreconfig.WatchedResource)

	// Copy user-configured resources
	for k, v := range cfg.WatchedResources {
		resourcesWithHAProxyPods[k] = v
	}

	// Add haproxy-pods watcher (or override if user configured it)
	resourcesWithHAProxyPods["haproxy-pods"] = coreconfig.WatchedResource{
		APIVersion:    "v1",
		Resources:     "pods",
		LabelSelector: cfg.PodSelector.MatchLabels,
		IndexBy: []string{
			"metadata.namespace",
			"metadata.name",
		},
	}

	logger.Debug("auto-injected haproxy-pods watcher",
		"label_selector", cfg.PodSelector.MatchLabels)

	// Create a watcher for each resource type (including auto-injected haproxy-pods)
	for resourceTypeName, watchedResource := range resourcesWithHAProxyPods {
		// Convert APIVersion/Kind to GVR
		gvr, err := toGVR(&watchedResource)
		if err != nil {
			return nil, fmt.Errorf("invalid resource %q: %w", resourceTypeName, err)
		}

		// Merge global and per-resource ignore fields
		ignoreFields := mergeIgnoreFields(cfg.WatchedResourcesIgnoreFields, nil)

		// Convert label selector map to metav1.LabelSelector
		var labelSelector *metav1.LabelSelector
		if len(watchedResource.LabelSelector) > 0 {
			labelSelector = &metav1.LabelSelector{
				MatchLabels: watchedResource.LabelSelector,
			}
		}

		// Calculate cache TTL as slightly over 2x drift prevention interval
		// This allows one rendering cycle to fail while still keeping resources cached
		driftInterval := cfg.Dataplane.GetDriftPreventionInterval()
		cacheTTL := driftInterval * 22 / 10 // 2.2x drift interval

		// Create watcher configuration
		watcherConfig := &types.WatcherConfig{
			GVR:              gvr,
			Namespace:        determineNamespace(resourceTypeName, k8sClient),
			LabelSelector:    labelSelector,
			IndexBy:          watchedResource.IndexBy,
			IgnoreFields:     ignoreFields,
			StoreType:        determineStoreType(watchedResource.Store),
			CacheTTL:         cacheTTL,
			DebounceInterval: 0, // Use default (500ms)

			// OnChange publishes ResourceIndexUpdatedEvent
			OnChange: func(store types.Store, changeStats types.ChangeStats) {
				eventBus.Publish(events.NewResourceIndexUpdatedEvent(
					resourceTypeName,
					changeStats,
				))
			},

			// OnSyncComplete publishes ResourceSyncCompleteEvent
			OnSyncComplete: func(store types.Store, initialCount int) {
				rwc.syncMu.Lock()
				rwc.synced[resourceTypeName] = true
				rwc.syncMu.Unlock()

				eventBus.Publish(events.NewResourceSyncCompleteEvent(
					resourceTypeName,
					initialCount,
				))
			},

			// Don't call OnChange during initial sync (wait for full state)
			CallOnChangeDuringSync: false,
		}

		// Create watcher (dereference pointer to pass value)
		w, err := watcher.New(*watcherConfig, k8sClient, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create watcher for %q: %w", resourceTypeName, err)
		}

		rwc.watchers[resourceTypeName] = w
		rwc.stores[resourceTypeName] = w.Store()

		rwc.logger.Debug("created resource watcher",
			"resource_type", resourceTypeName,
			"gvr", gvr.String(),
			"index_by", watchedResource.IndexBy,
			"ignore_fields", len(ignoreFields))
	}

	return rwc, nil
}

// Start begins watching all configured resources.
//
// This method:
//   - Starts all watchers in separate goroutines
//   - Returns immediately without blocking
//   - Continues running until ctx is cancelled
//
// Use WaitForAllSync() to wait for initial synchronization to complete.
func (r *ResourceWatcherComponent) Start(ctx context.Context) error {
	r.logger.Debug("starting resource watchers", "count", len(r.watchers))

	// Start all watchers in goroutines
	for resourceTypeName, w := range r.watchers {
		// Capture loop variables to avoid closure bug
		name := resourceTypeName
		resourceWatcher := w

		go func() {
			r.logger.Debug("starting watcher", "resource_type", name)

			if err := resourceWatcher.Start(ctx); err != nil {
				r.logger.Error("watcher failed",
					"resource_type", name,
					"error", err)
			}
		}()
	}

	r.logger.Debug("all resource watchers started")

	// Wait for context cancellation
	<-ctx.Done()

	r.logger.Info("Resource watchers stopping")
	return nil
}

// WaitForAllSync blocks until all watchers have completed initial synchronization.
//
// Returns:
//   - nil if all watchers synced successfully
//   - error if sync fails or context is cancelled
func (r *ResourceWatcherComponent) WaitForAllSync(ctx context.Context) error {
	r.logger.Debug("waiting for all resource watchers to sync", "count", len(r.watchers))

	// Wait for all watchers to sync in parallel using errgroup
	g, gCtx := errgroup.WithContext(ctx)

	for resourceTypeName, w := range r.watchers {
		g.Go(func() error {
			r.logger.Debug("waiting for watcher sync", "resource_type", resourceTypeName)

			if _, err := w.WaitForSync(gCtx); err != nil {
				return fmt.Errorf("watcher sync failed for %q: %w", resourceTypeName, err)
			}

			r.logger.Debug("watcher synced", "resource_type", resourceTypeName)
			return nil
		})
	}

	// Wait for all watchers to complete
	if err := g.Wait(); err != nil {
		return err
	}

	r.logger.Debug("All resource watchers synced successfully")
	return nil
}

// GetStore returns the store for a specific resource type.
//
// Returns:
//   - The store if the resource type exists
//   - nil if the resource type is not watched
func (r *ResourceWatcherComponent) GetStore(resourceTypeName string) types.Store {
	return r.stores[resourceTypeName]
}

// GetAllStores returns a map of all stores keyed by resource type name.
//
// Returns a copy of the internal map to prevent external modification.
func (r *ResourceWatcherComponent) GetAllStores() map[string]types.Store {
	stores := make(map[string]types.Store, len(r.stores))
	for k, v := range r.stores {
		stores[k] = v
	}
	return stores
}

// IsSynced returns true if the specified resource type has completed initial sync.
func (r *ResourceWatcherComponent) IsSynced(resourceTypeName string) bool {
	r.syncMu.RLock()
	defer r.syncMu.RUnlock()

	return r.synced[resourceTypeName]
}

// AllSynced returns true if all resource types have completed initial sync.
func (r *ResourceWatcherComponent) AllSynced() bool {
	r.syncMu.RLock()
	defer r.syncMu.RUnlock()

	for resourceTypeName := range r.watchers {
		if !r.synced[resourceTypeName] {
			return false
		}
	}

	return true
}

// -----------------------------------------------------------------------------
// Helper Functions
// -----------------------------------------------------------------------------

// determineStoreType returns the appropriate store type based on the configuration.
// Supported values:
//   - "on-demand": Uses CachedStore for memory-efficient storage with API-backed retrieval
//   - "full" or empty: Uses MemoryStore for fast in-memory storage (default)
func determineStoreType(storeConfig string) types.StoreType {
	if storeConfig == "on-demand" {
		return types.StoreTypeCached
	}
	return types.StoreTypeMemory // Default to full in-memory store
}

// determineNamespace returns the appropriate namespace for a resource watcher.
// HAProxy pods ("haproxy-pods") are scoped to the controller namespace for security.
// All other resources are watched cluster-wide.
func determineNamespace(resourceTypeName string, k8sClient *client.Client) string {
	if resourceTypeName == "haproxy-pods" {
		return k8sClient.Namespace()
	}
	return "" // Cluster-wide for other resources
}

// toGVR converts a WatchedResource configuration to a GroupVersionResource.
func toGVR(wr *coreconfig.WatchedResource) (schema.GroupVersionResource, error) {
	if wr.APIVersion == "" {
		return schema.GroupVersionResource{}, fmt.Errorf("api_version is required")
	}
	if wr.Resources == "" {
		return schema.GroupVersionResource{}, fmt.Errorf("resources is required")
	}

	// Parse APIVersion into Group/Version
	group, version := parseAPIVersion(wr.APIVersion)

	// Use the explicit plural resource name from configuration
	resource := wr.Resources

	return schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}, nil
}

// parseAPIVersion splits an API version string into group and version components.
//
// Examples:
//   - "v1" → ("", "v1")  // Core resources
//   - "networking.k8s.io/v1" → ("networking.k8s.io", "v1")
func parseAPIVersion(apiVersion string) (group, version string) {
	parts := strings.SplitN(apiVersion, "/", 2)
	if len(parts) == 1 {
		// Core resources like "v1" have no group
		return "", parts[0]
	}
	return parts[0], parts[1]
}

// mergeIgnoreFields combines global and per-resource ignore field lists.
//
// Deduplicates entries and returns a new slice.
// If perResource is nil, returns a copy of global.
func mergeIgnoreFields(global, perResource []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(global)+len(perResource))

	// Add global fields
	for _, field := range global {
		if !seen[field] {
			result = append(result, field)
			seen[field] = true
		}
	}

	// Add per-resource fields
	for _, field := range perResource {
		if !seen[field] {
			result = append(result, field)
			seen[field] = true
		}
	}

	return result
}
