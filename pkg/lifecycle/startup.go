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
	"fmt"
	"runtime"

	"golang.org/x/sync/errgroup"
)

// StartAll starts all registered components.
//
// Components are started concurrently.
// Leader-only components are skipped unless isLeader is true.
//
// This method blocks until all components are running or an error occurs.
// Returns the first error encountered, or nil if all components started successfully.
//
// Parameters:
//   - ctx: Context for cancellation
//   - isLeader: Whether this instance is currently the leader
//
// Example:
//
//	err := registry.StartAll(ctx, isLeader)
//	if err != nil {
//	    return fmt.Errorf("starting components: %w", err)
//	}
func (r *Registry) StartAll(ctx context.Context, isLeader bool) error {
	r.mu.Lock()
	componentsToStart := r.prepareComponentsToStart(isLeader)
	r.mu.Unlock()

	if len(componentsToStart) == 0 {
		return nil
	}

	// Start components using errgroup for concurrent execution with error handling
	g, gCtx := errgroup.WithContext(ctx)

	for _, comp := range componentsToStart {
		g.Go(func() error {
			return r.startComponent(gCtx, comp)
		})
	}

	return g.Wait()
}

// prepareComponentsToStart returns the components to start.
// Must be called with r.mu held.
func (r *Registry) prepareComponentsToStart(isLeader bool) []*registeredComponent {
	componentsToStart := make([]*registeredComponent, 0, len(r.components))

	for _, comp := range r.components {
		// Skip leader-only components if not leader, but mark them as standby
		if comp.config.leaderOnly && !isLeader {
			comp.status = StatusStandby
			r.logger.Debug("Setting leader-only component to standby (not leader)",
				"name", comp.component.Name())
			continue
		}

		comp.status = StatusStarting
		componentsToStart = append(componentsToStart, comp)
	}

	return componentsToStart
}

// startComponent starts a single component and updates its status.
//
// Design note on timing: Status is set to Running and ready channel is closed
// after Start() has been entered. This ensures:
//  1. All-replica components subscribe in their constructor, not in Start()
//  2. Leader-only components subscribe in Start() and implement SubscriptionReadySignaler
//
// For leader-only components that implement SubscriptionReadySignaler, we wait for
// their signal before considering them ready. This prevents a race condition where
// EventBus.Start() replays events before leader-only components have subscribed.
func (r *Registry) startComponent(ctx context.Context, comp *registeredComponent) error {
	name := comp.component.Name()

	r.logger.Debug("starting component", "name", name)

	// Set status to Running before calling Start()
	r.updateStatus(name, StatusRunning, nil)

	// Use channels to coordinate Start() entry with ready signal
	startEntered := make(chan struct{})
	errChan := make(chan error, 1)

	go func() {
		// Signal that Start() is about to be called
		close(startEntered)
		// Run the component (blocks until context cancelled or error)
		errChan <- comp.component.Start(ctx)
	}()

	// Wait for goroutine to reach the point where Start() is about to be called
	<-startEntered

	// Check if component implements SubscriptionReadySignaler for precise synchronization.
	// Leader-only components subscribe during Start(), so they need this mechanism to
	// ensure EventBus.Start() doesn't replay events before subscription is complete.
	if signaler, ok := comp.component.(SubscriptionReadySignaler); ok {
		readyCh := signaler.SubscriptionReady()
		if readyCh != nil {
			// Wait for component to signal subscription complete
			select {
			case <-readyCh:
				r.logger.Debug("component subscription ready", "name", name)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	} else {
		// Yield to scheduler to give the Start() call a chance to actually begin.
		// This reduces race conditions where callers waiting on the ready channel
		// proceed before the component's Start() has actually begun executing code.
		runtime.Gosched()
	}

	// Signal that this component is ready
	close(comp.ready)

	// Wait for Start() to complete
	err := <-errChan

	// Update status after Start() returns
	if err != nil && !errors.Is(err, context.Canceled) {
		r.updateStatus(name, StatusFailed, err)
		r.logger.Error("Component failed", "name", name, "error", err)

		return fmt.Errorf("component %s failed: %w", name, err)
	}

	r.updateStatus(name, StatusStopped, nil)
	r.logger.Info("Component stopped", "name", name)

	return nil
}
