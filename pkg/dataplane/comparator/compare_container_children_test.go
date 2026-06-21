// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package comparator

import (
	"sort"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// The three Comparator methods exercised here — compareNameserversWithIndex,
// compareMailerEntriesWithIndex, comparePeerEntriesWithIndex — are thin
// wrappers around the generic compareNamedMaps helper. compareNamedMaps
// itself is well-tested in compare_named_maps_test.go, but each wrapper
// adds a load-bearing concern that the generic test cannot cover:
//
//   The PARENT NAME (resolverSection / mailersSection / peersSection)
//   must flow through the closure into EVERY operation factory call —
//   create, update, AND delete — because the executor uses the parent
//   name to scope the API call (the resolver's nameservers, the mailers
//   section's entries, etc.). A regression that hard-coded "" or used
//   the wrong variable in any one branch would silently target the
//   wrong parent on the Dataplane API and either no-op (parent doesn't
//   exist) or, worse, mutate an unrelated parent.
//
// We pin the contract by exercising all four diff branches in a single
// call (one create, one delete, one update, one no-op) and asserting on
// the public surface of the resulting Operations:
//
//   - Type():     which factory fired (Create/Update/Delete)
//   - Section():  the API section name (nameserver / mailer_entry / peer_entry)
//   - Describe(): includes the parent name we passed in (proof the
//                 parent flows into the closure for that branch)
//
// Why the named-child describe? It's the public output that embeds the parent
// name verbatim ("Update nameserver 'dns1' in resolvers section 'mydns'"),
// so a parent-name regression is observable through the existing public
// Operation interface — no need to reach into private fields.

// summarizeOps produces "<type>:<section>:<describe>" so all four
// load-bearing facts — operation type, section name, child name, and
// parent name (the latter two embedded in Describe) — are pinned in a
// single sortable string. Sorting is necessary because compareNamedMaps
// iterates Go maps in unspecified order.
func summarizeOps(ops []Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, opTypeName(op.Type())+":"+op.Section()+":"+op.Describe())
	}
	sort.Strings(out)
	return out
}

func opTypeName(t sections.OperationType) string {
	switch t {
	case sections.OperationCreate:
		return "create"
	case sections.OperationUpdate:
		return "update"
	case sections.OperationDelete:
		return "delete"
	default:
		return "unknown"
	}
}

func TestCompareNameserversWithIndex_AllFourBranches(t *testing.T) {
	comp := New()

	port := int64(53)
	addrA := "8.8.8.8"
	addrB := "1.1.1.1"
	addrBnew := "1.0.0.1" // forces an update on "keep-update"

	current := map[string]*models.Nameserver{
		"keep-noop":   {Name: "keep-noop", Address: &addrA, Port: &port},
		"keep-update": {Name: "keep-update", Address: &addrB, Port: &port},
		"to-delete":   {Name: "to-delete", Address: &addrA, Port: &port},
	}
	desired := map[string]*models.Nameserver{
		"keep-noop":   {Name: "keep-noop", Address: &addrA, Port: &port},
		"keep-update": {Name: "keep-update", Address: &addrBnew, Port: &port},
		"to-create":   {Name: "to-create", Address: &addrB, Port: &port},
	}

	ops := comp.compareNameserversWithIndex("mydns", current, desired)

	require.Len(t, ops, 3, "all four branches in one call → exactly 3 ops (one no-op + 3 changes)")
	assert.Equal(t,
		[]string{
			"create:nameserver:Create nameserver 'to-create' in resolvers section 'mydns'",
			"delete:nameserver:Delete nameserver 'to-delete' from resolvers section 'mydns'",
			"update:nameserver:Update nameserver 'keep-update' in resolvers section 'mydns'",
		},
		summarizeOps(ops),
		"the resolver section name 'mydns' must appear in every Describe() output — "+
			"a regression that dropped the parent name on any single branch (especially "+
			"the rarely-exercised update branch) would target the wrong resolver on the API",
	)
}

func TestCompareMailerEntriesWithIndex_AllFourBranches(t *testing.T) {
	comp := New()

	current := map[string]*models.MailerEntry{
		"keep-noop":   {Name: "keep-noop", Address: "smtp.a.example.com", Port: 25},
		"keep-update": {Name: "keep-update", Address: "smtp.b.example.com", Port: 25},
		"to-delete":   {Name: "to-delete", Address: "smtp.x.example.com", Port: 25},
	}
	desired := map[string]*models.MailerEntry{
		"keep-noop":   {Name: "keep-noop", Address: "smtp.a.example.com", Port: 25},
		"keep-update": {Name: "keep-update", Address: "smtp.b.example.com", Port: 587}, // port flip → update
		"to-create":   {Name: "to-create", Address: "smtp.new.example.com", Port: 25},
	}

	ops := comp.compareMailerEntriesWithIndex("alerts", current, desired)

	require.Len(t, ops, 3)
	assert.Equal(t,
		[]string{
			"create:mailer_entry:Create mailer entry 'to-create' in mailers section 'alerts'",
			"delete:mailer_entry:Delete mailer entry 'to-delete' from mailers section 'alerts'",
			"update:mailer_entry:Update mailer entry 'keep-update' in mailers section 'alerts'",
		},
		summarizeOps(ops),
		"the mailers section name 'alerts' must appear in every Describe() output for the entry ops",
	)
}

func TestComparePeerEntriesWithIndex_AllFourBranches(t *testing.T) {
	comp := New()

	addrA := "10.0.0.1"
	addrB := "10.0.0.2"
	addrBnew := "10.0.0.20" // address change → update
	addrDel := "10.0.0.9"
	addrNew := "10.0.0.3"
	current := map[string]*models.PeerEntry{
		"keep-noop":   {Name: "keep-noop", Address: &addrA, Port: ptrInt64(7777)},
		"keep-update": {Name: "keep-update", Address: &addrB, Port: ptrInt64(7777)},
		"to-delete":   {Name: "to-delete", Address: &addrDel, Port: ptrInt64(7777)},
	}
	desired := map[string]*models.PeerEntry{
		"keep-noop":   {Name: "keep-noop", Address: &addrA, Port: ptrInt64(7777)},
		"keep-update": {Name: "keep-update", Address: &addrBnew, Port: ptrInt64(7777)},
		"to-create":   {Name: "to-create", Address: &addrNew, Port: ptrInt64(7777)},
	}

	ops := comp.comparePeerEntriesWithIndex("peers-cluster", current, desired)

	require.Len(t, ops, 3)
	assert.Equal(t,
		[]string{
			"create:peer_entry:Create peer entry 'to-create' in peer section 'peers-cluster'",
			"delete:peer_entry:Delete peer entry 'to-delete' from peer section 'peers-cluster'",
			"update:peer_entry:Update peer entry 'keep-update' in peer section 'peers-cluster'",
		},
		summarizeOps(ops),
		"the peer section name 'peers-cluster' must appear in every Describe() output for the entry ops",
	)
}

func TestCompareContainerChildrenWithIndex_EmptyAndNilInputs(t *testing.T) {
	comp := New()

	// Defensive check: empty maps and nil maps must produce no operations
	// (and not panic) for all three wrappers. compareNamedMaps already
	// promises this, but pinning it through the wrappers guards against
	// a regression where a wrapper added a pre-call that dereferences an
	// uninitialized container or panics on a nil map argument.

	cases := []struct {
		name string
		fn   func() []Operation
	}{
		{
			name: "nameservers nil/empty",
			fn:   func() []Operation { return comp.compareNameserversWithIndex("mydns", nil, nil) },
		},
		{
			name: "mailer entries nil/empty",
			fn:   func() []Operation { return comp.compareMailerEntriesWithIndex("alerts", nil, nil) },
		},
		{
			name: "peer entries nil/empty",
			fn:   func() []Operation { return comp.comparePeerEntriesWithIndex("peers-cluster", nil, nil) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				assert.Empty(t, tc.fn(),
					"empty inputs must produce zero ops; a regression that "+
						"dereferenced a nil map or short-circuited incorrectly would surface here")
			})
		})
	}
}
