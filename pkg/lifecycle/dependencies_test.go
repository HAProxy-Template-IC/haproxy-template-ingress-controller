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
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pkg/lifecycle/dependencies.go has THREE load-bearing functions that
// previously had no direct test coverage — only indirect coverage
// through StartAll happy paths. The contracts protect against:
//
//  1. validateDependencies — missing/unstarted deps must error AT
//     STARTUP, not silently let components race ahead of dependencies
//     they need. The "already running" branch must skip the startSet
//     check (otherwise re-registering a component during a running
//     iteration would falsely fail validation).
//
//  2. detectCycles — DFS-based cycle detection. Must catch direct
//     cycles (A→B→A), indirect cycles (A→B→C→A), and self-cycles
//     (A→A). Must NOT false-positive on legitimate fan-in/fan-out
//     graphs (A→B, A→C, B→D, C→D — diamond is acyclic).
//
//  3. waitForDependencies — blocks on dep.ready channel until
//     dependency signals readiness OR context cancels. Context
//     cancellation MUST wrap the error with the dependency name so
//     callers can identify which dep blocked startup.

// makeRegisteredComponent builds a registeredComponent for direct
// invocation of dependency helpers. The production codepath builds
// these via Registry.Register; replicating just the fields these
// helpers touch keeps each test focused on the helper's logic.
func makeRegisteredComponent(name string, deps ...string) *registeredComponent {
	return &registeredComponent{
		component: &mockComponent{name: name},
		config: registrationConfig{
			dependencies: deps,
		},
		status: StatusPending,
		ready:  make(chan struct{}),
	}
}

// registerInto attaches the component to the registry's internal maps
// (mirroring what Registry.Register does for the fields these helpers
// read). Returns the same pointer for chaining.
func registerInto(r *Registry, comp *registeredComponent) *registeredComponent {
	r.components = append(r.components, comp)
	r.byName[comp.component.Name()] = comp
	return comp
}

func newDepRegistry() *Registry {
	return &Registry{
		byName: make(map[string]*registeredComponent),
		logger: slog.Default(),
	}
}

func TestValidateDependencies_MissingAndUnstartedDeps(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(r *Registry) ([]*registeredComponent, map[string]bool)
		wantErr    bool
		wantSubstr string // expected substring in error message
	}{
		{
			name: "ok: dep is in start set",
			setup: func(r *Registry) ([]*registeredComponent, map[string]bool) {
				dep := registerInto(r, makeRegisteredComponent("dep"))
				comp := registerInto(r, makeRegisteredComponent("comp", "dep"))
				return []*registeredComponent{dep, comp}, map[string]bool{"dep": true, "comp": true}
			},
		},
		{
			name: "ok: dep is already running (not in start set)",
			setup: func(r *Registry) ([]*registeredComponent, map[string]bool) {
				dep := registerInto(r, makeRegisteredComponent("dep"))
				dep.status = StatusRunning
				// dep NOT in start set — but it's running so it qualifies.
				comp := registerInto(r, makeRegisteredComponent("comp", "dep"))
				return []*registeredComponent{comp}, map[string]bool{"comp": true}
			},
		},
		{
			name: "error: dep is unknown",
			setup: func(r *Registry) ([]*registeredComponent, map[string]bool) {
				comp := registerInto(r, makeRegisteredComponent("comp", "missing-dep"))
				return []*registeredComponent{comp}, map[string]bool{"comp": true}
			},
			wantErr:    true,
			wantSubstr: `unknown component "missing-dep"`,
		},
		{
			name: "error: dep exists but is neither in start set nor running",
			setup: func(r *Registry) ([]*registeredComponent, map[string]bool) {
				dep := registerInto(r, makeRegisteredComponent("dep"))
				dep.status = StatusPending // not running, not in start set
				comp := registerInto(r, makeRegisteredComponent("comp", "dep"))
				return []*registeredComponent{comp}, map[string]bool{"comp": true}
			},
			wantErr:    true,
			wantSubstr: `not being started`,
		},
		{
			name: "error: multiple deps, one missing",
			setup: func(r *Registry) ([]*registeredComponent, map[string]bool) {
				good := registerInto(r, makeRegisteredComponent("good"))
				good.status = StatusRunning
				comp := registerInto(r, makeRegisteredComponent("comp", "good", "bad"))
				return []*registeredComponent{comp}, map[string]bool{"comp": true}
			},
			wantErr:    true,
			wantSubstr: `unknown component "bad"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newDepRegistry()
			components, startSet := tt.setup(r)

			err := r.validateDependencies(components, startSet)

			if tt.wantErr {
				require.Error(t, err,
					"validateDependencies must surface missing/unstarted deps "+
						"AT STARTUP — silently letting them through would race "+
						"the component against unstarted dependencies in production")
				assert.Contains(t, err.Error(), tt.wantSubstr,
					"error message MUST identify the offending dependency by name "+
						"so operators can fix the wiring quickly")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDetectCycles_DiscriminatesCyclicAndAcyclicGraphs(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(r *Registry) []*registeredComponent
		wantErr   bool
		wantSubst string
	}{
		{
			name: "no cycles: linear chain A→B→C",
			setup: func(r *Registry) []*registeredComponent {
				a := registerInto(r, makeRegisteredComponent("a", "b"))
				b := registerInto(r, makeRegisteredComponent("b", "c"))
				c := registerInto(r, makeRegisteredComponent("c"))
				return []*registeredComponent{a, b, c}
			},
		},
		{
			name: "no cycles: diamond A→B,A→C,B→D,C→D",
			setup: func(r *Registry) []*registeredComponent {
				// Fan-out then fan-in is acyclic; a regression that
				// flagged this would falsely block legitimate
				// initialization graphs.
				a := registerInto(r, makeRegisteredComponent("a", "b", "c"))
				b := registerInto(r, makeRegisteredComponent("b", "d"))
				c := registerInto(r, makeRegisteredComponent("c", "d"))
				d := registerInto(r, makeRegisteredComponent("d"))
				return []*registeredComponent{a, b, c, d}
			},
		},
		{
			name: "no cycles: dep outside start set is skipped",
			setup: func(r *Registry) []*registeredComponent {
				// Component depends on something not in the current
				// start set — DFS skips it, so no cycle is reported.
				a := registerInto(r, makeRegisteredComponent("a", "external"))
				return []*registeredComponent{a}
			},
		},
		{
			name: "cycle: direct two-node A→B→A",
			setup: func(r *Registry) []*registeredComponent {
				a := registerInto(r, makeRegisteredComponent("a", "b"))
				b := registerInto(r, makeRegisteredComponent("b", "a"))
				return []*registeredComponent{a, b}
			},
			wantErr:   true,
			wantSubst: "circular dependency",
		},
		{
			name: "cycle: indirect three-node A→B→C→A",
			setup: func(r *Registry) []*registeredComponent {
				a := registerInto(r, makeRegisteredComponent("a", "b"))
				b := registerInto(r, makeRegisteredComponent("b", "c"))
				c := registerInto(r, makeRegisteredComponent("c", "a"))
				return []*registeredComponent{a, b, c}
			},
			wantErr:   true,
			wantSubst: "circular dependency",
		},
		{
			name: "cycle: self-loop A→A",
			setup: func(r *Registry) []*registeredComponent {
				// A pathological but possible mistake — pin it so the
				// cycle detector catches it instead of infinite-looping.
				a := registerInto(r, makeRegisteredComponent("a", "a"))
				return []*registeredComponent{a}
			},
			wantErr:   true,
			wantSubst: "circular dependency",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newDepRegistry()
			components := tt.setup(r)

			err := r.detectCycles(components)

			if tt.wantErr {
				require.Error(t, err,
					"detectCycles MUST identify circular dependencies — "+
						"missing one would let the registry deadlock at startup "+
						"with all components stuck in waitForDependencies")
				assert.Contains(t, err.Error(), tt.wantSubst,
					"the error message MUST contain 'circular dependency' so "+
						"operators can immediately recognize the root cause")
				return
			}
			require.NoError(t, err,
				"detectCycles MUST NOT false-positive on acyclic graphs — "+
					"flagging a legitimate diamond/fan-in would block normal "+
					"initialization patterns")
		})
	}
}

func TestWaitForDependencies_ReturnsWhenAllReady(t *testing.T) {
	r := newDepRegistry()
	dep1 := registerInto(r, makeRegisteredComponent("dep1"))
	dep2 := registerInto(r, makeRegisteredComponent("dep2"))
	comp := registerInto(r, makeRegisteredComponent("comp", "dep1", "dep2"))

	// Close ready channels (simulating dependencies that are ready).
	close(dep1.ready)
	close(dep2.ready)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := r.waitForDependencies(ctx, comp)
	require.NoError(t, err,
		"waitForDependencies must return nil when all deps signal ready — "+
			"a regression that blocked further would prevent startup completion")
}

func TestWaitForDependencies_BlocksUntilDepReady(t *testing.T) {
	// Pin the actual blocking semantics: waitForDependencies must NOT
	// return until the dep's ready channel is closed. This is the
	// barrier that prevents components from starting before their
	// dependencies have entered Start().
	r := newDepRegistry()
	dep := registerInto(r, makeRegisteredComponent("dep"))
	comp := registerInto(r, makeRegisteredComponent("comp", "dep"))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	returnCh := make(chan error, 1)
	go func() {
		returnCh <- r.waitForDependencies(ctx, comp)
	}()

	// While dep is not ready, the function MUST be blocked.
	select {
	case err := <-returnCh:
		t.Fatalf("waitForDependencies returned BEFORE dep ready (err=%v) — "+
			"a regression here would let the component race ahead of its "+
			"unstarted dependency, causing every dependency-ordering bug "+
			"the registry is supposed to prevent", err)
	case <-time.After(100 * time.Millisecond):
		// expected — function is blocked on dep.ready
	}

	// Now signal dep ready; function should return promptly.
	close(dep.ready)
	select {
	case err := <-returnCh:
		require.NoError(t, err,
			"after dep.ready closes, waitForDependencies must return nil — "+
				"failure here would silently break all dependency ordering")
	case <-time.After(time.Second):
		t.Fatal("waitForDependencies did not return within 1s after dep.ready closed")
	}
}

func TestWaitForDependencies_ContextCancellationWrapsErrorWithDepName(t *testing.T) {
	// Context cancellation must produce an error that names the
	// blocking dependency. Operators triaging "startup hung" issues
	// rely on this to identify which dep never readied.
	r := newDepRegistry()
	// Register blocked-dep so byName resolution succeeds; we never close
	// its ready channel so waitForDependencies must block until cancel.
	registerInto(r, makeRegisteredComponent("blocked-dep"))
	comp := registerInto(r, makeRegisteredComponent("comp", "blocked-dep"))

	ctx, cancel := context.WithCancel(context.Background())

	returnCh := make(chan error, 1)
	go func() {
		returnCh <- r.waitForDependencies(ctx, comp)
	}()

	// Cancel without ever closing dep.ready.
	cancel()

	select {
	case err := <-returnCh:
		require.Error(t, err,
			"context cancellation while waiting for a dep MUST surface as an error")
		assert.Contains(t, err.Error(), "blocked-dep",
			"the error message MUST name the offending dependency so operators "+
				"can identify which dep blocked startup — without this, hung "+
				"startups are nearly impossible to diagnose in production")
		assert.ErrorIs(t, err, context.Canceled,
			"the error MUST wrap context.Canceled so callers can use errors.Is "+
				"to distinguish cancellation from other failures")
	case <-time.After(time.Second):
		t.Fatal("waitForDependencies did not return after context cancellation — " +
			"a regression here would leave goroutines leaked across the lifetime " +
			"of the controller process")
	}
}

func TestWaitForDependencies_MissingDepReturnsExplicitError(t *testing.T) {
	// Defensive contract from the doc comment: "Should not happen
	// after validation, but handle gracefully." If validation was
	// somehow bypassed (or a future refactor moved validation), the
	// wait path must NOT panic on a nil dep — it must return a
	// recognizable error.
	r := newDepRegistry()
	comp := registerInto(r, makeRegisteredComponent("comp", "ghost"))

	ctx := context.Background()

	err := r.waitForDependencies(ctx, comp)
	require.Error(t, err,
		"waitForDependencies MUST return an error (NOT panic) when a dep "+
			"is missing from byName — the function is the last line of "+
			"defense against ordering bugs, and a panic would crash the "+
			"controller process instead of failing gracefully")
	assert.Contains(t, err.Error(), `"ghost"`,
		"the error MUST identify the missing dep by name to aid debugging")
}
