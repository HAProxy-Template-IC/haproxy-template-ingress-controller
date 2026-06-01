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
	"context"
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
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/watcher"
)

// setupResourceWatchers creates and starts resource watchers and index tracker, then waits for sync.
//
// Returns the ResourceWatcherComponent and an error if watcher creation or synchronization fails.
func setupResourceWatchers(
	iterCtx context.Context,
	cfg *coreconfig.Config,
	k8sClient *client.Client,
	bus *busevents.EventBus,
	logger *slog.Logger,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) (*resourcewatcher.ResourceWatcherComponent, error) {
	// Extract resource type names for IndexSynchronizationTracker
	// Include haproxy-pods which is auto-injected by ResourceWatcherComponent
	resourceNames := make([]string, 0, len(cfg.WatchedResources)+1)
	for name := range cfg.WatchedResources {
		resourceNames = append(resourceNames, name)
	}
	// Add haproxy-pods (auto-injected)
	resourceNames = append(resourceNames, names.HAProxyPodsResourceType)

	// Create ResourceWatcherComponent
	resourceWatcher, err := resourcewatcher.New(cfg, k8sClient, bus, logger)
	if err != nil {
		return nil, fmt.Errorf("creating resource watcher: %w", err)
	}

	// Create IndexSynchronizationTracker
	indexTracker := indextracker.New(bus, logger, resourceNames)

	// Start resource watcher and index tracker (tracked by errgroup for graceful shutdown)
	startInErrGroup(errGroup, iterCtx, logger, cancel, "resource watcher", resourceWatcher.Start)
	startInErrGroup(errGroup, iterCtx, logger, cancel, "index tracker", indexTracker.Start)

	// Wait for all resource indices to sync
	logger.Debug("Waiting for resource indices to sync")
	if err := resourceWatcher.WaitForAllSync(iterCtx); err != nil {
		return nil, fmt.Errorf("resource watcher sync failed: %w", err)
	}
	logger.Debug("all resource indices synced")

	return resourceWatcher, nil
}

// setupConfigWatchers creates and starts HAProxyTemplateConfig CRD, credentials Secret,
// and optional webhook-cert Secret watchers, then waits for sync.
//
// webhookCertSecretName is empty when webhook validation is disabled, in
// which case no third watcher is created. When non-empty, a single-resource
// watcher is created for the named Secret that publishes
// CertResourceChangedEvent so the certloader can extract the rotated cert
// and ConfigChangeHandler can trigger an iteration restart with the new
// cert. Without this watcher the cert-rotation pipeline is open-loop:
// CertLoader subscribes to CertResourceChangedEvent but no producer ever
// fires, so an external rotation of the webhook-cert Secret (cert-manager,
// e2e test harness, manual edit) only takes effect on the next pod
// restart.
//
// Returns an error if watcher creation or synchronization fails.
func setupConfigWatchers(
	iterCtx context.Context,
	k8sClient *client.Client,
	crdName string,
	secretName string,
	webhookCertSecretName string,
	crdGVR schema.GroupVersionResource,
	secretGVR schema.GroupVersionResource,
	bus *busevents.EventBus,
	logger *slog.Logger,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) error {
	// Create watcher for HAProxyTemplateConfig CRD
	crdWatcher, err := watcher.NewSingle(&types.SingleWatcherConfig{
		GVR:       crdGVR,
		Namespace: k8sClient.Namespace(),
		Name:      crdName,
		OnChange: func(obj any) error {
			bus.Publish(events.NewConfigResourceChangedEvent(obj))
			return nil
		},
		// OnSyncComplete delivers the current state after initial sync.
		// This ensures eventual consistency: if updates arrived during the sync window
		// (when OnChange callbacks are suppressed), the current state is delivered here.
		OnSyncComplete: func(obj any) error {
			if obj == nil {
				logger.Debug("CRD watcher sync complete, no resource in cache (skipping event)")
				return nil
			}
			logger.Debug("CRD watcher sync complete, publishing current state")
			bus.Publish(events.NewConfigResourceChangedEvent(obj))
			return nil
		},
	}, k8sClient)
	if err != nil {
		return fmt.Errorf("creating HAProxyTemplateConfig watcher: %w", err)
	}

	secretWatcher, err := watcher.NewSingle(&types.SingleWatcherConfig{
		GVR:       secretGVR,
		Namespace: k8sClient.Namespace(),
		Name:      secretName,
		OnChange: func(obj any) error {
			bus.Publish(events.NewSecretResourceChangedEvent(obj))
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
			bus.Publish(events.NewSecretResourceChangedEvent(obj))
			return nil
		},
	}, k8sClient)
	if err != nil {
		return fmt.Errorf("creating Secret watcher: %w", err)
	}

	// Optional webhook-cert Secret watcher (nil when webhook validation is
	// disabled). Construction extracted to a helper to keep the parent
	// function within the configured cognitive-complexity budget.
	var webhookCertWatcher *watcher.SingleWatcher
	if webhookCertSecretName != "" {
		webhookCertWatcher, err = newWebhookCertWatcher(k8sClient, webhookCertSecretName, secretGVR, bus, logger)
		if err != nil {
			return err
		}
	}

	// Start watchers (tracked by errgroup for graceful shutdown)
	startInErrGroup(errGroup, iterCtx, logger, cancel, "HAProxyTemplateConfig watcher", crdWatcher.Start)
	startInErrGroup(errGroup, iterCtx, logger, cancel, "Secret watcher", secretWatcher.Start)
	if webhookCertWatcher != nil {
		startInErrGroup(errGroup, iterCtx, logger, cancel, "webhook-cert Secret watcher", webhookCertWatcher.Start)
	}

	logger.Debug("Watchers started, waiting for initial sync")

	// Wait for watchers to complete initial sync in parallel
	watcherGroup, watcherCtx := errgroup.WithContext(iterCtx)

	watcherGroup.Go(func() error {
		if err := crdWatcher.WaitForSync(watcherCtx); err != nil {
			return fmt.Errorf("HAProxyTemplateConfig watcher sync failed: %w", err)
		}
		return nil
	})

	watcherGroup.Go(func() error {
		if err := secretWatcher.WaitForSync(watcherCtx); err != nil {
			return fmt.Errorf("secret watcher sync failed: %w", err)
		}
		return nil
	})

	if webhookCertWatcher != nil {
		watcherGroup.Go(func() error {
			if err := webhookCertWatcher.WaitForSync(watcherCtx); err != nil {
				return fmt.Errorf("webhook-cert Secret watcher sync failed: %w", err)
			}
			return nil
		})
	}

	// Wait for watchers to sync
	if err := watcherGroup.Wait(); err != nil {
		return fmt.Errorf("config watcher sync failed: %w", err)
	}

	logger.Debug("config and secret watchers synced")

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
	iterCtx context.Context,
	k8sClient *client.Client,
	crdName string,
	haproxyCfgGVR schema.GroupVersionResource,
	logger *slog.Logger,
	cancel context.CancelFunc,
	errGroup *errgroup.Group,
) (*currentconfigstore.Store, error) {
	// Create CurrentConfigStore to cache parsed HAProxy config
	store, err := currentconfigstore.New(logger)
	if err != nil {
		return nil, fmt.Errorf("creating current config store: %w", err)
	}

	// Sync fetch existing HAProxyCfg (if any)
	// This is critical for slot preservation on controller restart
	haproxyCfgName := configpublisher.GenerateRuntimeConfigName(crdName)
	haproxyCfgResource, err := k8sClient.GetResource(iterCtx, haproxyCfgGVR, haproxyCfgName)
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
	startInErrGroup(errGroup, iterCtx, logger, cancel, "HAProxyCfg watcher", haproxyCfgWatcher.Start)
	logger.Debug("HAProxyCfg watcher started for current config updates")

	return store, nil
}

// newWebhookCertWatcher creates the single-resource Secret watcher that
// publishes CertResourceChangedEvent on add/update of the cert Secret.
// Caller must check name != "" before calling (the parent function does
// this so the helper can stay strictly about construction).
//
// Why a dedicated watcher: the credentials Secret has its own watcher
// publishing SecretResourceChangedEvent; the webhook-cert Secret needs a
// distinct event type because CertLoader subscribes to
// CertResourceChangedEvent (a different filter from the credentials
// pipeline). Without this watcher the cert-rotation pipeline is open-loop:
// CertLoader's subscription drains an empty queue, ConfigChangeHandler
// never sees a CertParsedEvent, and the iteration restart that's supposed
// to load the rotated cert never fires.
func newWebhookCertWatcher(
	k8sClient *client.Client,
	name string,
	secretGVR schema.GroupVersionResource,
	bus *busevents.EventBus,
	logger *slog.Logger,
) (*watcher.SingleWatcher, error) {
	w, err := watcher.NewSingle(&types.SingleWatcherConfig{
		GVR:       secretGVR,
		Namespace: k8sClient.Namespace(),
		Name:      name,
		OnChange: func(obj any) error {
			bus.Publish(events.NewCertResourceChangedEvent(obj))
			return nil
		},
		OnSyncComplete: func(obj any) error {
			if obj == nil {
				logger.Debug("webhook-cert Secret watcher sync complete, no resource in cache (skipping event)")
				return nil
			}
			logger.Debug("webhook-cert Secret watcher sync complete, publishing current state")
			bus.Publish(events.NewCertResourceChangedEvent(obj))
			return nil
		},
	}, k8sClient)
	if err != nil {
		return nil, fmt.Errorf("creating webhook-cert Secret watcher: %w", err)
	}
	return w, nil
}
