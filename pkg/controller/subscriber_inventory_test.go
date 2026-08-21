// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bus drops events for a subscriber whose buffer is full, so not losing
// them is a per-subscription property every component author has to get right.
// component.CoalescingHandler states the rule; this test is what makes it a
// rule rather than prose. It fails when a package starts subscribing without a
// recorded decision, so the question gets answered when the component is
// written instead of after an incident.
//
// See pkg/controller/component/base.go (CoalescingHandler) for the criterion.

// mailboxDecision records why a subscriber package does or does not need the
// coalescing mailbox.
type mailboxDecision struct {
	// mailbox is true when the package declares CoalescesOn. The test verifies
	// the claim against the source rather than trusting the table.
	mailbox bool
	// why must name the criterion limb that decides it: which blocking call the
	// handler makes, or which of the two conditions it fails.
	why string
}

// subscriberInventory covers every package under pkg/controller that opens a
// bus subscription. Adding a subscriber without adding a row fails this test.
var subscriberInventory = map[string]mailboxDecision{
	"component": {
		mailbox: false,
		why:     "the mailbox itself — infrastructure, not a subscriber",
	},
	"commentator": {
		mailbox: false,
		why:     "lossy subscription: drops are the documented contract",
	},
	"debug": {
		mailbox: false,
		why:     "lossy subscription: drops are the documented contract",
	},
	"controller": {
		mailbox: false,
		why:     "StateCache handler only writes in-memory fields; never blocks",
	},
	"configchange": {
		mailbox: false,
		why: "handler runs validation off the event loop in its own goroutine, and " +
			"StatusUpdater's CR writes are driven by per-config-change events that " +
			"cannot outrun them",
	},
	"configpublisher": {
		mailbox: false,
		why: "writes output CRDs per render, but the leader-only 200-slot buffer is " +
			"fed by the coordinator's already-coalesced render rate",
	},
	"deployer": {
		mailbox: true,
		why:     "Dataplane API round-trips per pod against per-render scheduling",
	},
	"discovery": {
		mailbox: false,
		why: "probes pods (blocking) on the undebounced resource.index.updated stream, " +
			"so it meets both limbs — but that type string is shared across every " +
			"watched kind and must not collapse. Uses the drift-tick level re-read " +
			"instead (handleDriftPrevention); pkg/controller/events/coalescible_inventory_test.go " +
			"keeps the type unarmed so a later mailbox adopter cannot silently collapse it",
	},
	"eventemitter": {
		mailbox: false,
		why:     "emits Kubernetes Events per reconcile; leader-gated and rate-limited upstream",
	},
	"httpstore": {
		mailbox: false,
		why:     "fetches on its own refresh timer, not on the subscribed events",
	},
	"indextracker": {
		mailbox: false,
		why:     "counts sync completions in memory; never blocks",
	},
	"metrics": {
		mailbox: false,
		why:     "Prometheus counter updates only; never blocks",
	},
	"proposalvalidator": {
		mailbox: false,
		why: "render+validate is slow, but each URL has at most one request in " +
			"flight (the store short-circuits while validating)",
	},
	"reconciler": {
		mailbox: false,
		why:     "publishes a trigger and returns; the coordinator absorbs the burst",
	},
	"rendergate": {
		mailbox: false,
		why: "the handler only records the newest render and signals its worker; " +
			"`haproxy -c` runs on that worker, off the event loop",
	},
	"resourceapplier": {
		mailbox: true,
		why:     "server-side apply of rendered resources per render",
	},
	"resourceloader": {
		mailbox: false,
		why:     "shared loader scaffold; its events are per-config-change",
	},
	"statusapplier": {
		mailbox: true,
		why:     "server-side apply of status patches per render and per deploy",
	},
	"validator": {
		mailbox: false,
		why:     "scatter-gather responder: one request per config change, bounded by the caller's timeout",
	},
}

// subscribeCalls are the bus entry points that open a subscription directly.
var subscribeCalls = []string{
	"Subscribe", "SubscribeLossy", "SubscribeTypes", "SubscribeTypesLeaderOnly",
}

// opensSubscription reports whether a call subscribes to the bus, either
// directly or through component.New, which subscribes on the caller's behalf.
func opensSubscription(sel *ast.SelectorExpr) bool {
	if slices.Contains(subscribeCalls, sel.Sel.Name) {
		return true
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "component" && sel.Sel.Name == "New"
}

func TestSubscriberInventory_CoversEverySubscriber(t *testing.T) {
	found, coalescing := scanSubscribers(t)
	require.NotEmpty(t, found, "the scan must find subscribers; a broken scan would pass vacuously")

	for _, pkg := range found {
		decision, ok := subscriberInventory[pkg]
		assert.True(t, ok,
			"package %q opens a bus subscription but has no row in subscriberInventory. "+
				"Decide whether its handler can block AND its events can outrun it "+
				"(see component.CoalescingHandler); then add the row with the reason.", pkg)
		if !ok {
			continue
		}

		assert.Equal(t, decision.mailbox, slices.Contains(coalescing, pkg),
			"package %q: subscriberInventory says mailbox=%v but the source %s declare "+
				"CoalescesOn. The table must describe the code, not the intent.",
			pkg, decision.mailbox, map[bool]string{true: "does", false: "does not"}[!decision.mailbox])

		assert.NotEmpty(t, decision.why, "package %q needs a reason, not just a verdict", pkg)
	}
}

func TestSubscriberInventory_HasNoStaleRows(t *testing.T) {
	found, _ := scanSubscribers(t)

	for pkg := range subscriberInventory {
		assert.Contains(t, found, pkg,
			"subscriberInventory lists %q, which no longer subscribes to anything — "+
				"drop the row so the table keeps meaning something", pkg)
	}
}

// scanSubscribers walks the controller tree and returns the packages that open
// a subscription and those that declare CoalescesOn, both as directory names.
func scanSubscribers(t *testing.T) (subscribers, coalescing []string) {
	t.Helper()

	root, err := os.Getwd()
	require.NoError(t, err)

	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}

		pkg := filepath.Base(filepath.Dir(path))
		if fileSubscribes(file) {
			subscribers = appendUnique(subscribers, pkg)
		}
		if fileDeclaresCoalescesOn(file) {
			coalescing = appendUnique(coalescing, pkg)
		}
		return nil
	})
	require.NoError(t, err)

	return subscribers, coalescing
}

// fileSubscribes reports whether the file opens a bus subscription.
func fileSubscribes(file *ast.File) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && opensSubscription(sel) {
			found = true
		}
		return !found
	})
	return found
}

// fileDeclaresCoalescesOn reports whether the file declares a CoalescesOn
// method, i.e. the package opts into mailbox mode.
func fileDeclaresCoalescesOn(file *ast.File) bool {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv != nil && fn.Name.Name == "CoalescesOn" {
			return true
		}
	}
	return false
}

func appendUnique(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}
	return append(list, value)
}
