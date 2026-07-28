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

package controller

import (
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/indextracker"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/watcher"
)

// setupResourceWatchers creates and starts resource watchers and index tracker, then waits for sync.
//
// Returns the ResourceWatcherComponent and an error if watcher creation or synchronization fails.
func setupResourceWatchers(
	setup *componentSetup,
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	logger *slog.Logger,
) (*resourcewatcher.ResourceWatcherComponent, error) {
	logger.Info("Stage 3: Starting resource watchers")

	// Extract resource type names for IndexSynchronizationTracker
	// Include haproxy-pods which is auto-injected by ResourceWatcherComponent
	resourceNames := make([]string, 0, len(cfg.WatchedResources)+1)
	for name := range cfg.WatchedResources {
		resourceNames = append(resourceNames, name)
	}
	// Add haproxy-pods (auto-injected)
	resourceNames = append(resourceNames, names.HAProxyPodsResourceType)

	// Create ResourceWatcherComponent
	resourceWatcher, err := resourcewatcher.New(cfg, k8sClient, setup.Bus, logger)
	if err != nil {
		return nil, fmt.Errorf("creating resource watcher: %w", err)
	}

	// Create IndexSynchronizationTracker
	indexTracker := indextracker.New(setup.Bus, logger, resourceNames)

	// Start resource watcher and index tracker (tracked by errgroup for graceful shutdown)
	startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, "resource watcher", resourceWatcher.Start)
	startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, "index tracker", indexTracker.Start)

	// Wait for all resource indices to sync
	logger.Debug("Waiting for resource indices to sync")
	if err := resourceWatcher.WaitForAllSync(setup.IterCtx); err != nil {
		return nil, fmt.Errorf("resource watcher sync failed: %w", err)
	}
	logger.Debug("All resource indices synced")

	return resourceWatcher, nil
}

// setupConfigWatchers creates and starts the HAProxyTemplateConfig CRD and
// credentials Secret watchers, then waits for sync.
//
// Returns an error if watcher creation or synchronization fails.
func setupConfigWatchers(
	setup *componentSetup,
	k8sClient *client.Client,
	crdNames []string,
	secretName string,
	crdGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	logger *slog.Logger,
) error {
	logger.Info("Stage 4: Starting config watchers")

	// One watcher per configured HAProxyTemplateConfig. Each emits its own
	// change event; the configloader keeps the set and re-merges, so a change
	// to any one of them re-derives the whole config. A helm upgrade writes
	// them one at a time, which the handler's reinit debounce coalesces.
	crdWatchers := make([]*watcher.SingleWatcher, 0, len(crdNames))
	for _, crdName := range crdNames {
		crdWatcher, err := watcher.NewSingle(&types.SingleWatcherConfig{
			GVR:       crdGVR,
			Namespace: k8sClient.Namespace(),
			Name:      crdName,
			OnChange: func(obj any) error {
				setup.Bus.Publish(events.NewConfigResourceChangedEvent(obj))
				return nil
			},
			// OnSyncComplete delivers the current state after initial sync.
			// This ensures eventual consistency: if updates arrived during the sync window
			// (when OnChange callbacks are suppressed), the current state is delivered here.
			OnSyncComplete: func(obj any) error {
				if obj == nil {
					logger.Debug("CRD watcher sync complete, no resource in cache (skipping event)", "name", crdName)
					return nil
				}
				logger.Debug("CRD watcher sync complete, publishing current state", "name", crdName)
				setup.Bus.Publish(events.NewConfigResourceChangedEvent(obj))
				return nil
			},
		}, k8sClient)
		if err != nil {
			return fmt.Errorf("creating HAProxyTemplateConfig watcher for %q: %w", crdName, err)
		}
		crdWatchers = append(crdWatchers, crdWatcher)
	}

	secretWatcher, err := watcher.NewSingle(&types.SingleWatcherConfig{
		GVR:       secretGVR,
		Namespace: k8sClient.Namespace(),
		Name:      secretName,
		OnChange: func(obj any) error {
			setup.Bus.Publish(events.NewSecretResourceChangedEvent(obj))
			return nil
		},
		// OnSyncComplete delivers the current state after initial sync.
		// This ensures eventual consistency: if updates arrived during the sync window
		// (when OnChange callbacks are suppressed), the current state is delivered here.
		OnSyncComplete: func(obj any) error {
			if obj == nil {
				logger.Debug("Secret watcher sync complete, no resource in cache (skipping event)")
				return nil
			}
			logger.Debug("Secret watcher sync complete, publishing current state")
			setup.Bus.Publish(events.NewSecretResourceChangedEvent(obj))
			return nil
		},
	}, k8sClient)
	if err != nil {
		return fmt.Errorf("creating Secret watcher: %w", err)
	}

	// Start watchers (tracked by errgroup for graceful shutdown)
	for i, crdWatcher := range crdWatchers {
		startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel,
			fmt.Sprintf("HAProxyTemplateConfig watcher (%s)", crdNames[i]), crdWatcher.Start)
	}
	startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, "Secret watcher", secretWatcher.Start)

	logger.Debug("Watchers started, waiting for initial sync")

	// Wait for watchers to complete initial sync in parallel
	watcherGroup, watcherCtx := errgroup.WithContext(setup.IterCtx)

	for i, crdWatcher := range crdWatchers {
		watcherGroup.Go(func() error {
			if err := crdWatcher.WaitForSync(watcherCtx); err != nil {
				return fmt.Errorf("HAProxyTemplateConfig watcher sync failed for %q: %w", crdNames[i], err)
			}
			return nil
		})
	}

	watcherGroup.Go(func() error {
		if err := secretWatcher.WaitForSync(watcherCtx); err != nil {
			return fmt.Errorf("secret watcher sync failed: %w", err)
		}
		return nil
	})

	// Wait for watchers to sync
	if err := watcherGroup.Wait(); err != nil {
		return fmt.Errorf("config watcher sync failed: %w", err)
	}

	logger.Debug("Config and secret watchers synced")

	// Initial config already passed via bootstrap event. Watchers handle subsequent changes only.

	return nil
}

// setupCurrentConfigStore creates and initializes the CurrentConfigStore for slot-aware
// server assignment during rolling deployments.
//
// This function:
//  1. Creates a CurrentConfigStore to cache parsed HAProxy config
//  2. Sync fetches existing HAProxyCfg (if any) to populate the store BEFORE first render
//  3. Creates an async watcher for silent updates (no events published)
//
// The sync fetch is critical: if first render happens before HAProxyCfg is loaded,
// currentConfig would be nil and we'd scramble existing server slots.
//
// The async watcher only updates the store - it does NOT trigger reconciliation.
// HAProxyCfg changes are passive state used only when rendering for other reasons.
func setupCurrentConfigStore(
	setup *componentSetup,
	k8sClient *client.Client,
	crdName string,
	haproxyCfgGVR schema.GroupVersionResource,
	logger *slog.Logger,
) (*currentconfigstore.Store, error) {
	// Create CurrentConfigStore to cache parsed HAProxy config
	store, err := currentconfigstore.New(logger)
	if err != nil {
		return nil, fmt.Errorf("creating current config store: %w", err)
	}

	// Sync fetch existing HAProxyCfg (if any)
	// This is critical for slot preservation on controller restart
	haproxyCfgName := configpublisher.GenerateRuntimeConfigName(crdName)
	haproxyCfgResource, err := k8sClient.GetResource(setup.IterCtx, haproxyCfgGVR, haproxyCfgName)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("fetching HAProxyCfg: %w", err)
		}
		logger.Info("No existing HAProxyCfg found (first deployment)")
	} else {
		// Populate store with existing config BEFORE first render
		store.Update(haproxyCfgResource)
		logger.Info("Loaded existing HAProxyCfg into current config store")
	}

	// Create async watcher for HAProxyCfg updates (silent updates, NO events)
	haproxyCfgWatcher, err := watcher.NewSingle(&types.SingleWatcherConfig{
		GVR:       haproxyCfgGVR,
		Namespace: k8sClient.Namespace(),
		Name:      haproxyCfgName,
		OnSyncComplete: func(obj any) error {
			// Silent update - NO events published
			store.Update(obj)
			return nil
		},
		OnChange: func(obj any) error {
			// Silent update - NO events published
			// This does NOT trigger reconciliation
			store.Update(obj)
			return nil
		},
	}, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("creating HAProxyCfg watcher: %w", err)
	}

	// Start HAProxyCfg watcher (tracked by errgroup for graceful shutdown)
	startInErrGroup(setup.ErrGroup, setup.IterCtx, logger, setup.Cancel, "HAProxyCfg watcher", haproxyCfgWatcher.Start)
	logger.Debug("HAProxyCfg watcher started for current config updates")

	return store, nil
}
