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

	"golang.org/x/sync/errgroup"
)

// StartAll starts all registered components.
//
// Components are started concurrently.
// Leader-only components are skipped unless isLeader is true.
//
// This method blocks until every started Component.Start call returns.
// Returns the first component error, or nil after graceful cancellation.
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
		if comp.leaderOnly && !isLeader {
			if comp.status == StatusPending {
				comp.status = StatusStandby
			}
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
// Components that subscribe in Start must implement SubscriptionReadySignaler;
// all others are ready because their subscriptions were created by the constructor.
func (r *Registry) startComponent(ctx context.Context, comp *registeredComponent) error {
	name := comp.component.Name()

	r.logger.Debug("Starting component", "name", name)

	var err error
	if signaler, ok := comp.component.(SubscriptionReadySignaler); ok {
		readyCh := signaler.SubscriptionReady()
		if readyCh != nil {
			errChan := make(chan error, 1)
			go func() {
				errChan <- comp.component.Start(ctx)
			}()

			select {
			case <-readyCh:
				r.logger.Debug("Component subscription ready", "name", name)
				r.updateStatus(name, StatusRunning, nil)
				close(comp.ready)
				err = <-errChan
			case err = <-errChan:
				if err == nil && ctx.Err() == nil {
					err = fmt.Errorf("component %s exited before signalling subscription readiness", name)
				}
			case <-ctx.Done():
				err = <-errChan
			}
		} else {
			r.updateStatus(name, StatusRunning, nil)
			close(comp.ready)
			err = comp.component.Start(ctx)
		}
	} else {
		r.updateStatus(name, StatusRunning, nil)
		close(comp.ready)
		err = comp.component.Start(ctx)
	}

	if err == nil && ctx.Err() == nil {
		err = fmt.Errorf("component %s stopped before context cancellation", name)
	}
	if err != nil && !isContextTermination(ctx, err) {
		r.updateStatus(name, StatusFailed, err)
		r.logger.Error("Component failed", "name", name, "error", err)

		return fmt.Errorf("component %s failed: %w", name, err)
	}

	r.updateStatus(name, StatusStopped, nil)
	r.logger.Info("Component stopped", "name", name)

	return nil
}

func isContextTermination(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, ctx.Err())
}
