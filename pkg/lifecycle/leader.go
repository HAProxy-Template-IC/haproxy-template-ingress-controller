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
		if comp.leaderOnly && (comp.status == StatusPending || comp.status == StatusStandby) {
			comp.status = StatusStarting
			// Re-create ready channel in case this is called multiple times
			comp.ready = make(chan struct{})
			componentsToStart = append(componentsToStart, comp)
		}
	}

	return componentsToStart
}

// StartLeaderOnly starts leader-only components and waits for them to be
// subscription-ready before returning. This method returns as soon as all components
// have signaled they're ready to receive events, rather than waiting for their
// Start() methods to complete.
//
// This is designed for use with the EventBus Pause/Start pattern, where leader-only
// components need to be subscribed before EventBus.Start() replays buffered events.
//
// Returns:
//   - A ComponentRun whose Done channel closes after every Component.Start returns.
//   - An error if the context is cancelled while waiting for components to become ready.
//
// Example (illustrative — see pkg/controller/leaderelection/component.go's
// OnStartedLeading wrapper for the real Pause/Start choreography around
// this call):
//
//	func (h *leadershipHandler) onBecameLeader(ctx context.Context) error {
//	    run, err := h.registry.StartLeaderOnly(ctx)
//	    if err != nil {
//	        return err
//	    }
//	    // Components are now subscribed, safe to call eventBus.Start()
//	    // Track errors asynchronously
//	    go func() {
//	        if err := run.Wait(); err != nil {
//	            log.Error("Leader component failed", "error", err)
//	        }
//	    }()
//	    return nil
//	}
func (r *Registry) StartLeaderOnly(ctx context.Context) (*ComponentRun, error) {
	componentsToStart := r.prepareLeaderOnlyComponents()
	run := newComponentRun()

	if len(componentsToStart) == 0 {
		run.finish(nil)
		return run, nil
	}

	// Track completion of all components
	g, gCtx := errgroup.WithContext(ctx)
	startupFailure := make(chan error, 1)

	// Start all components in goroutines
	for _, comp := range componentsToStart {
		g.Go(func() error {
			err := r.startComponent(gCtx, comp)
			if err != nil {
				select {
				case startupFailure <- err:
				default:
				}
			}
			return err
		})
	}
	go func() {
		run.finish(g.Wait())
	}()

	// Wait for all components to be ready (subscription complete)
	for _, comp := range componentsToStart {
		select {
		case <-comp.ready:
		case err := <-startupFailure:
			return run, err
		case <-ctx.Done():
			return run, ctx.Err()
		case <-run.Done():
			if ctx.Err() != nil {
				return run, ctx.Err()
			}
			err := run.Wait()
			if err == nil {
				err = errors.New("leader components stopped before becoming ready")
			}
			return run, err
		}
	}

	return run, nil
}
