// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// StartLeaderOnlyComponents has three branches the existing
// TestRegistry_StartLeaderOnlyComponents does NOT exercise:
//
//  1. prepareLeaderOnlyComponents finds NO leader-only components
//     (e.g. all components are all-replica). The function must
//     return nil immediately without spinning up an errgroup or
//     blocking. A regression that always entered the errgroup loop
//     would still happen to "work" here, but a regression that
//     forgot the early-return and called g.Wait() on an empty group
//     would block leadership-acquisition forever the first time a
//     pod with no leader-only components became leader.
//
//  2. prepareLeaderOnlyComponents returns an error from
//     validateDependencies (a leader-only component declares
//     DependsOn for an UNREGISTERED component). The function must
//     surface that error verbatim to the caller — leadership
//     acquisition has to FAIL LOUDLY when the wiring is broken so
//     the operator notices, instead of silently starting a partial
//     subset of components and leaving the cluster in an
//     inconsistent state.
//
//  3. (Implicit by negation) The errgroup.WithContext goroutine
//     fan-out is only entered when there are components to start —
//     so branch (1) doubles as the guard that protects the
//     errgroup-allocation path from being reached unnecessarily.
//
// The existing happy-path test covers the "components present and
// all start cleanly" branch. These two pin the boundary cases.
func TestRegistry_StartLeaderOnlyComponents_EdgeCases(t *testing.T) {
	t.Run("no leader-only components — returns nil immediately", func(t *testing.T) {
		registry := NewRegistry()

		// Register only all-replica components — none of these
		// should be picked up by prepareLeaderOnlyComponents.
		registry.Register(newMockComponent("all-replica-A"))
		registry.Register(newMockComponent("all-replica-B"))

		// Use a SHORT timeout. If the function were to incorrectly
		// enter the errgroup branch with an empty component list it
		// would still return quickly since g.Wait() on an empty group
		// is fine — but the assertion that we returned BEFORE the
		// timeout is the primary signal: it must not block.
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- registry.StartLeaderOnlyComponents(ctx)
		}()

		select {
		case err := <-done:
			assert.NoError(t, err,
				"StartLeaderOnlyComponents with no leader-only components "+
					"must return nil immediately — there is nothing to start "+
					"and the early-return must fire before the errgroup is created")
		case <-time.After(150 * time.Millisecond):
			t.Fatal("StartLeaderOnlyComponents did not return promptly when " +
				"there are no leader-only components — a regression that " +
				"dropped the early-return and entered the errgroup branch " +
				"may block leadership acquisition")
		}
	})

	t.Run("missing dependency — error surfaces from prepare step", func(t *testing.T) {
		registry := NewRegistry()

		// Register a leader-only component that depends on a
		// component name that was never registered. The
		// validateDependencies call inside prepareLeaderOnlyComponents
		// must reject this and the error must propagate out of
		// StartLeaderOnlyComponents — no goroutine fan-out, no partial
		// startup.
		leaderComp := newMockComponent("leader-with-bad-dep")
		registry.Register(leaderComp,
			LeaderOnly(),
			DependsOn("never-registered-component"))

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := registry.StartLeaderOnlyComponents(ctx)

		require.Error(t, err,
			"StartLeaderOnlyComponents MUST return an error when a "+
				"leader-only component declares a dependency on an "+
				"unregistered component. The system must fail loudly "+
				"so the operator notices the broken wiring instead of "+
				"silently starting a partial subset.")
		assert.Contains(t, err.Error(), "never-registered-component",
			"the propagated error must name the missing dependency so "+
				"the operator can fix the wiring without grepping logs")

		// And the leader component must NOT have been started.
		assert.False(t, leaderComp.IsStarted(),
			"leader-only component MUST NOT be started when its "+
				"dependency validation failed — partial startup leaves "+
				"the cluster in an inconsistent state")
	})
}
