// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package events

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

// Implementing Coalescible() is what ARMS a type for collapsing: the mailbox in
// pkg/controller/component only collapses an event whose type a component
// declared in CoalescesOn AND which reports Coalescible() == true. Declaring an
// unarmed type is inert, so this method — not the declaration — is the change
// that can start losing events, and it is the one worth gating.
//
// Collapsing keys on the event TYPE STRING alone, which makes two properties
// load-bearing for every armed type:
//
//   - Single subject. If one type string covers several subjects (per watched
//     kind, per pod, per URL, per request ID), collapsing a run discards the
//     subjects the run did not end on. resource.index.updated is the canonical
//     example and is deliberately NOT armed.
//   - Re-derivable. A consumer that accumulates from the event rather than
//     reading its absolute payload loses the skipped increments permanently.
//     Whether a given consumer accumulates is a per-component question, which
//     is why CoalescesOn is opt-in per component — but a type whose payload is
//     inherently a delta should not be armed at all.
//
// A row here is a claim that both hold for the type. Adding Coalescible() to a
// type without adding its row fails this test.

type coalescibleReason struct {
	// why must state what makes collapsing meaning-preserving: the single
	// subject the type describes, and why the payload is absolute rather than a
	// delta.
	why string
	// caveat records a property a future consumer must respect. Non-empty means
	// the type is armed but carries a sharp edge.
	caveat string
}

var armedForCollapsing = map[string]coalescibleReason{
	"HAProxyPodsDiscoveredEvent": {
		why: "carries the whole endpoint set, not a diff — the latest snapshot supersedes every earlier one",
	},
	"ValidationCompletedEvent": {
		why: "one global render-validate cycle; the verdict describes the whole config",
	},
	"ReconciliationTriggeredEvent": {
		why: "a bare 'something changed' edge over the store; the render re-reads current state, so only the latest trigger matters. Emitter-gated: drift and fallback triggers report false so their deploy is never skipped",
	},
	"ReconciliationCompletedEvent": {
		why: "one global reconcile; carries the complete rendered resource set",
	},
	"ResourcesAppliedEvent": {
		why: "one global apply pass; carries the complete status-patch set",
	},
	"TemplateRenderedEvent": {
		why: "one global render; carries the whole config, aux files and patches. Emitter-gated from the trigger",
	},
	"DeploymentCompletedEvent": {
		why: "one global deployment; carries the full per-deploy totals",
		caveat: "the deployer must see EVERY instance to clear its in-flight flag, which is why " +
			"deployer.CoalescesOn declares only deployment.scheduled. Collapsing is safe " +
			"only for consumers that read the payload, like the status applier",
	},
	"DeploymentSkippedEvent": {
		why: "one global skip decision; carries the patches describing what is already deployed",
	},
	"DeploymentScheduledEvent": {
		why: "one global deployment intent; the latest supersedes earlier ones because each carries the full config to push. Emitter-gated so drift and retry dispatches are never skipped",
	},
	"HTTPResourceUpdatedEvent": {
		why: "the only consumer is subject-blind — it fires one constant reconcile trigger and re-reads all URL content from the store at render time",
		caveat: "MULTI-SUBJECT: published per URL from independent refresh timers. Safe only " +
			"while no consumer reads URL. A mailbox consumer that acts per URL must not " +
			"declare this type — it would drop every URL but the last in a run",
	},
}

func TestCoalescibleInventory_CoversEveryArmedType(t *testing.T) {
	armed := scanCoalescibleTypes(t)
	require.NotEmpty(t, armed, "the scan must find armed types; a broken scan would pass vacuously")

	for _, typeName := range armed {
		reason, ok := armedForCollapsing[typeName]
		assert.True(t, ok,
			"%s implements Coalescible(), which permits a mailbox consumer to collapse a run "+
				"of them. Add a row to armedForCollapsing stating the single subject it "+
				"describes and why its payload is absolute rather than a delta — or drop "+
				"Coalescible() if either is untrue.", typeName)
		if ok {
			assert.NotEmpty(t, reason.why, "%s needs a reason, not just a row", typeName)
		}
	}
}

func TestCoalescibleInventory_HasNoStaleRows(t *testing.T) {
	armed := scanCoalescibleTypes(t)

	for typeName := range armedForCollapsing {
		assert.Contains(t, armed, typeName,
			"armedForCollapsing lists %s, which no longer implements Coalescible() — "+
				"drop the row so the table keeps meaning something", typeName)
	}
}

// scanCoalescibleTypes returns the receiver type names of every Coalescible()
// method declared in this package's non-test files.
func scanCoalescibleTypes(t *testing.T) []string {
	t.Helper()

	root, err := os.Getwd()
	require.NoError(t, err)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	fset := token.NewFileSet()
	var armed []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, filepath.Join(root, name), nil, 0)
		require.NoError(t, parseErr)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "Coalescible" {
				continue
			}
			if receiver := receiverTypeName(fn); receiver != "" && !slices.Contains(armed, receiver) {
				armed = append(armed, receiver)
			}
		}
	}
	return armed
}

// receiverTypeName returns the bare type name of a method receiver, stripping
// the pointer star.
func receiverTypeName(fn *ast.FuncDecl) string {
	if len(fn.Recv.List) == 0 {
		return ""
	}
	expr := fn.Recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}
