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
	"runtime"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcestore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// registerResourceStores registers all resource stores from the watcher with the store manager.
func registerResourceStores(
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
	storeManager *resourcestore.Manager,
	logger *slog.Logger,
) {
	logger.Debug("Registering resource stores with ResourceStoreManager")
	k8sStores := resourceWatcher.GetAllStores()
	for resourceType, store := range k8sStores {
		storeManager.RegisterStore(resourceType, store)
		logger.Debug("Registered store", "resource_type", resourceType)
	}
}

// startBackgroundComponents starts the StateCache and metrics component in background goroutines.
// These components subscribe immediately and wait for events. Errors are logged but non-fatal.
func startBackgroundComponents(
	ctx context.Context,
	stateCache *StateCache,
	metricsComponent *metrics.Component,
	logger *slog.Logger,
) {
	go func() {
		if err := stateCache.Start(ctx); err != nil {
			logger.Error("state cache failed", "error", err)
		}
	}()

	go func() {
		if err := metricsComponent.Start(ctx); err != nil {
			logger.Error("metrics component failed", "error", err)
		}
	}()
}

// startInErrGroup starts a component in the errgroup with consistent error handling.
// On error, it logs the failure, calls cancel to trigger shutdown, and returns the error.
// This ensures all iteration-scoped goroutines are tracked for graceful shutdown.
func startInErrGroup(
	errGroup *errgroup.Group,
	iterCtx context.Context,
	logger *slog.Logger,
	cancel context.CancelFunc,
	componentName string,
	startFn func(context.Context) error,
) {
	errGroup.Go(func() error {
		if err := startFn(iterCtx); err != nil {
			logger.Error(componentName+" failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
}

// waitForGoroutinesToFinish waits for all goroutines in errgroup to finish with a timeout.
// This is CRITICAL for lease release - elector needs time to call ReleaseOnCancel.
func waitForGoroutinesToFinish(errGroup *errgroup.Group, logger *slog.Logger, prefix string) {
	logger.Info(fmt.Sprintf("Waiting for goroutines to finish %s...", strings.ToLower(prefix)),
		"goroutine_count", runtime.NumGoroutine())

	done := make(chan error, 1)
	go func() {
		done <- errGroup.Wait()
	}()

	// Log progress periodically while waiting
	ticker := time.NewTicker(ShutdownProgressInterval)
	defer ticker.Stop()

	startTime := time.Now()
	timeoutCh := time.After(ShutdownTimeout)

	for {
		select {
		case err := <-done:
			elapsed := time.Since(startTime)
			if err != nil {
				logger.Warn(fmt.Sprintf("Goroutines finished with error during %s", strings.ToLower(prefix)),
					"error", err,
					"elapsed_ms", elapsed.Milliseconds(),
					"goroutine_count", runtime.NumGoroutine())
			} else {
				logger.Info("All goroutines finished gracefully",
					"elapsed_ms", elapsed.Milliseconds(),
					"goroutine_count", runtime.NumGoroutine())
			}
			return

		case <-ticker.C:
			elapsed := time.Since(startTime)
			logger.Info(fmt.Sprintf("%s: still waiting for goroutines...", prefix),
				"elapsed_s", int(elapsed.Seconds()),
				"remaining_s", int((ShutdownTimeout - elapsed).Seconds()),
				"goroutine_count", runtime.NumGoroutine())

		case <-timeoutCh:
			logger.Warn(fmt.Sprintf("%s timeout exceeded - some goroutines may not have finished", prefix),
				"timeout_s", int(ShutdownTimeout.Seconds()),
				"goroutine_count", runtime.NumGoroutine())
			return
		}
	}
}

// resourceStoreManagerAdapter adapts resourcestore.Manager to stores.StoreProvider.
//
// This adapter is needed because resourcestore.Manager uses k8s/types.Store
// while stores.StoreProvider expects stores.Store. Although both interfaces
// have identical methods, Go's type system treats them as different types.
type resourceStoreManagerAdapter struct {
	manager    *resourcestore.Manager
	storeNames []string
}

// GetStore implements stores.StoreProvider.
func (a *resourceStoreManagerAdapter) GetStore(name string) stores.Store {
	store, exists := a.manager.GetStore(name)
	if !exists || store == nil {
		return nil
	}
	// Wrap the types.Store to satisfy stores.Store interface
	return &stores.TypesStoreAdapter{Inner: store}
}

// StoreNames implements stores.StoreProvider.
func (a *resourceStoreManagerAdapter) StoreNames() []string {
	return a.storeNames
}

// newStoreProviderFromManager creates a stores.StoreProvider from a resourcestore.Manager.
// This extracts the pattern used in multiple places to avoid duplication.
func newStoreProviderFromManager(manager *resourcestore.Manager) stores.StoreProvider {
	return &resourceStoreManagerAdapter{
		manager:    manager,
		storeNames: manager.StoreNames(),
	}
}
