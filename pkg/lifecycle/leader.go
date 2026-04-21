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

package lifecycle

import (
	"context"
	"errors"

	"golang.org/x/sync/errgroup"
)

// prepareLeaderOnlyComponents promotes leader-only components that are
// Pending or Standby to Starting, re-creates their ready channel, and
// validates dependencies. Must be called without r.mu held.
func (r *Registry) prepareLeaderOnlyComponents() ([]*registeredComponent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	componentsToStart := make([]*registeredComponent, 0)
	startSet := make(map[string]bool)

	for _, comp := range r.components {
		// Only start leader-only components that are pending or standby
		if comp.config.leaderOnly && (comp.status == StatusPending || comp.status == StatusStandby) {
			comp.status = StatusStarting
			// Re-create ready channel in case this is called multiple times
			comp.ready = make(chan struct{})
			componentsToStart = append(componentsToStart, comp)
			startSet[comp.component.Name()] = true
		}
	}

	// Add already running components to the start set for dependency validation
	for _, comp := range r.components {
		if comp.status == StatusRunning {
			startSet[comp.component.Name()] = true
		}
	}

	if err := r.validateDependencies(componentsToStart, startSet); err != nil {
		return nil, err
	}

	return componentsToStart, nil
}

// StartLeaderOnlyComponents starts components marked as leader-only.
//
// This should be called when leadership is acquired. Returns an error
// if any leader-only component fails to start.
//
// Example:
//
//	// In leadership callback
//	func (c *Controller) onBecameLeader() {
//	    if err := c.registry.StartLeaderOnlyComponents(ctx); err != nil {
//	        log.Error("Failed to start leader components", "error", err)
//	    }
//	}
func (r *Registry) StartLeaderOnlyComponents(ctx context.Context) error {
	componentsToStart, err := r.prepareLeaderOnlyComponents()
	if err != nil {
		return err
	}

	if len(componentsToStart) == 0 {
		return nil
	}

	g, gCtx := errgroup.WithContext(ctx)

	for _, comp := range componentsToStart {
		g.Go(func() error {
			// Wait for dependencies to be ready
			if err := r.waitForDependencies(gCtx, comp); err != nil {
				return err
			}
			return r.startComponent(gCtx, comp)
		})
	}

	return g.Wait()
}

// StartLeaderOnlyComponentsAsync starts leader-only components and waits for them to be
// subscription-ready before returning. Unlike StartLeaderOnlyComponents, this method
// returns as soon as all components have signaled they're ready to receive events,
// rather than waiting for their Start() methods to complete.
//
// This is designed for use with the EventBus Pause/Start pattern, where leader-only
// components need to be subscribed before EventBus.Start() replays buffered events.
//
// Returns:
//   - A channel that will receive an error if any component fails, or be closed if all
//     components complete successfully. The caller should track this in an errgroup.
//   - An error if components cannot be started (e.g., dependency validation fails)
//
// Example:
//
//	// In leadership callback
//	func (c *Controller) onBecameLeader() error {
//	    errCh, err := c.registry.StartLeaderOnlyComponentsAsync(ctx)
//	    if err != nil {
//	        return err
//	    }
//	    // Components are now subscribed, safe to call eventBus.Start()
//	    // Track errors asynchronously
//	    go func() {
//	        if err := <-errCh; err != nil {
//	            log.Error("Leader component failed", "error", err)
//	        }
//	    }()
//	    return nil
//	}
func (r *Registry) StartLeaderOnlyComponentsAsync(ctx context.Context) (<-chan error, error) {
	componentsToStart, err := r.prepareLeaderOnlyComponents()
	if err != nil {
		return nil, err
	}

	errCh := make(chan error, 1)

	if len(componentsToStart) == 0 {
		close(errCh)
		return errCh, nil
	}

	// Track completion of all components
	g, gCtx := errgroup.WithContext(ctx)

	// Start all components in goroutines
	for _, comp := range componentsToStart {
		g.Go(func() error {
			// Wait for dependencies to be ready
			if err := r.waitForDependencies(gCtx, comp); err != nil {
				return err
			}
			return r.startComponent(gCtx, comp)
		})
	}

	// Wait for all components to be ready (subscription complete)
	for _, comp := range componentsToStart {
		select {
		case <-comp.ready:
			// Component is subscription-ready
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Launch goroutine to track completion and propagate errors
	go func() {
		err := g.Wait()
		if err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
		close(errCh)
	}()

	return errCh, nil
}
