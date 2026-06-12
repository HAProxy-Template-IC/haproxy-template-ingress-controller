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
// Pending or Standby to Starting and re-creates their ready channel.
// Must be called without r.mu held.
func (r *Registry) prepareLeaderOnlyComponents() []*registeredComponent {
	r.mu.Lock()
	defer r.mu.Unlock()

	componentsToStart := make([]*registeredComponent, 0)

	for _, comp := range r.components {
		// Only start leader-only components that are pending or standby
		if comp.config.leaderOnly && (comp.status == StatusPending || comp.status == StatusStandby) {
			comp.status = StatusStarting
			// Re-create ready channel in case this is called multiple times
			comp.ready = make(chan struct{})
			componentsToStart = append(componentsToStart, comp)
		}
	}

	return componentsToStart
}

// StartLeaderOnlyComponentsAsync starts leader-only components and waits for them to be
// subscription-ready before returning. This method returns as soon as all components
// have signaled they're ready to receive events, rather than waiting for their
// Start() methods to complete.
//
// This is designed for use with the EventBus Pause/Start pattern, where leader-only
// components need to be subscribed before EventBus.Start() replays buffered events.
//
// Returns:
//   - A channel that will receive an error if any component fails, or be closed if all
//     components complete successfully. The caller should track this in an errgroup.
//   - An error if the context is cancelled while waiting for components to become ready.
//
// Example (illustrative — see pkg/controller/leaderelection/component.go's
// OnStartedLeading wrapper for the real Pause/Start choreography around
// this call):
//
//	func (h *leadershipHandler) onBecameLeader(ctx context.Context) error {
//	    errCh, err := h.registry.StartLeaderOnlyComponentsAsync(ctx)
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
	componentsToStart := r.prepareLeaderOnlyComponents()

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
