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
	"log/slog"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// buildStoreProvider builds a stores.StoreProvider from the resource watcher's
// live k8s stores. Each types.Store is wrapped in a stores.TypesStoreAdapter so
// it satisfies the stores.Store interface — Go treats the two identical method
// sets as distinct types. Nil stores are skipped.
func buildStoreProvider(resourceWatcher *resourcewatcher.ResourceWatcherComponent) stores.StoreProvider {
	k8sStores := resourceWatcher.GetAllStores()
	converted := make(map[string]stores.Store, len(k8sStores))
	for resourceType, store := range k8sStores {
		if store == nil {
			continue
		}
		converted[resourceType] = &stores.TypesStoreAdapter{Inner: store}
	}
	return stores.NewRealStoreProvider(converted)
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
			logger.Error("State cache failed", "error", err)
		}
	}()

	go func() {
		if err := metricsComponent.Start(ctx); err != nil {
			logger.Error("Metrics component failed", "error", err)
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
	logger.Info("Waiting for goroutines to finish",
		"phase", strings.ToLower(prefix),
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
				logger.Warn("Goroutines finished with error",
					"phase", strings.ToLower(prefix),
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
			logger.Info("Still waiting for goroutines",
				"phase", prefix,
				"elapsed_s", int(elapsed.Seconds()),
				"remaining_s", int((ShutdownTimeout - elapsed).Seconds()),
				"goroutine_count", runtime.NumGoroutine())

		case <-timeoutCh:
			logger.Warn("Timeout exceeded - some goroutines may not have finished",
				"phase", prefix,
				"timeout_s", int(ShutdownTimeout.Seconds()),
				"goroutine_count", runtime.NumGoroutine())
			return
		}
	}
}
