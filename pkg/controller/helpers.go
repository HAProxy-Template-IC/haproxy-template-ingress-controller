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
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcewatcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// buildStoreProvider preserves the concrete stores and their optional capabilities.
func buildStoreProvider(k8sStores map[string]types.Store) stores.StoreProvider {
	converted := make(map[string]stores.Store, len(k8sStores))
	for resourceType, store := range k8sStores {
		if store == nil {
			continue
		}
		converted[resourceType] = store
	}
	return stores.NewRealStoreProvider(converted)
}

func buildFreshStoreProvider(watcher *resourcewatcher.ResourceWatcherComponent) func(context.Context) (stores.StoreProvider, error) {
	return func(ctx context.Context) (stores.StoreProvider, error) {
		fresh, err := watcher.FreshStores(ctx)
		if err != nil {
			return nil, err
		}
		return buildStoreProvider(fresh), nil
	}
}

// initRenderState creates debug state and the leader-term currentFiles authority.
func initRenderState(
	setup *componentSetup,
	resourceWatcher *resourcewatcher.ResourceWatcherComponent,
	k8sClient *client.Client,
	crdName string,
	logger *slog.Logger,
) (*StateCache, *currentFilesAuthority, error) {
	stateCache := NewStateCache(setup.Bus, resourceWatcher, logger)
	startBackgroundComponents(setup, stateCache, setup.MetricsComponent, logger)

	publishedAux, err := setupPublishedAuxFilesStore(setup, k8sClient, crdName, logger)
	if err != nil {
		return nil, nil, err
	}

	return stateCache, newCurrentFilesAuthority(publishedAux), nil
}

// startBackgroundComponents starts the StateCache and metrics component in background goroutines.
// These components subscribe immediately and wait for events. Errors are logged but non-fatal.
func startBackgroundComponents(
	setup *componentSetup,
	stateCache *StateCache,
	metricsComponent *metrics.Component,
	logger *slog.Logger,
) {
	startNonFatalInErrGroup(setup.ErrGroup, setup.IterCtx, logger, "State cache", stateCache.Start)
	startNonFatalInErrGroup(setup.ErrGroup, setup.IterCtx, logger, "Metrics component", metricsComponent.Start)
}

func startNonFatalInErrGroup(
	errGroup *errgroup.Group,
	iterCtx context.Context,
	logger *slog.Logger,
	componentName string,
	startFn func(context.Context) error,
) {
	errGroup.Go(func() error {
		err := startFn(iterCtx)
		if err == nil && iterCtx.Err() == nil {
			err = errors.New("stopped unexpectedly")
		}
		logBackgroundComponentError(iterCtx, logger, componentName, err)
		return nil
	})
}

func logBackgroundComponentError(ctx context.Context, logger *slog.Logger, componentName string, err error) {
	if err == nil {
		return
	}
	if isContextTermination(ctx, err) {
		return
	}
	logger.Error(componentName+" failed", "error", err)
}

func isContextTermination(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, ctx.Err())
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
		err := startFn(iterCtx)
		if err == nil && iterCtx.Err() == nil {
			err = fmt.Errorf("%s stopped unexpectedly", componentName)
		}
		if err != nil {
			if isContextTermination(iterCtx, err) {
				return nil
			}
			logger.Error(componentName+" failed", "error", err)
			cancel()
			return err
		}
		return nil
	})
}

type iterationTeardownTimeoutError struct {
	phase   string
	timeout time.Duration
}

func (e *iterationTeardownTimeoutError) Error() string {
	return fmt.Sprintf("%s did not finish within %s", e.phase, e.timeout)
}

// waitForGoroutinesToFinish waits for all goroutines in errgroup to finish with a timeout.
func waitForGoroutinesToFinish(errGroup *errgroup.Group, logger *slog.Logger, prefix string, timeout time.Duration) error {
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
	timeoutTimer := time.NewTimer(timeout)
	defer timeoutTimer.Stop()

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
			return err

		case <-ticker.C:
			elapsed := time.Since(startTime)
			logger.Info("Still waiting for goroutines",
				"phase", prefix,
				"elapsed_s", int(elapsed.Seconds()),
				"remaining_s", int((timeout - elapsed).Seconds()),
				"goroutine_count", runtime.NumGoroutine())

		case <-timeoutTimer.C:
			logger.Warn("Timeout exceeded - some goroutines may not have finished",
				"phase", prefix,
				"timeout_s", int(timeout.Seconds()),
				"goroutine_count", runtime.NumGoroutine())
			return &iterationTeardownTimeoutError{phase: strings.ToLower(prefix), timeout: timeout}
		}
	}
}
