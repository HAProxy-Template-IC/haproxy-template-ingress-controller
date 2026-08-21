// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build playground

package parser

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// normalizeFrontendMetadataWithIndex visits TWELVE distinct
// sub-collections on a Frontend and applies NormalizeMetadata to each
// entry, plus the Frontend itself plus binds via the side-channel
// bindIndex. The existing tests only exercise normalizeGlobalMetadata
// (one collection, log targets) plus the simple per-section helpers.
//
// The high-risk regression here is "we added a new sub-collection to
// Frontend but forgot to add a normalize loop for it" — that bug is
// completely silent, manifesting only later as comparator drift when
// a section's nested metadata fails to match flat metadata from
// another source. Pin it with a single fixture that has nested
// metadata in EVERY sub-collection plus the bind side-channel, then
// assert per-collection that the flattening happened. Each assertion
// names its collection so a missing branch produces a clear failure.

// nestedMD is the canonical "{<key>: {value: <v>}}" shape that
// NormalizeMetadata flattens to "{<key>: <v>}". A regression in any
// branch leaves the original {value: ...} structure visible.
func nestedMD(key, value string) map[string]any {
	return map[string]any{key: map[string]any{"value": value}}
}

// flatString extracts the flattened value at the given key, or
// returns "" if the value is still nested (which is the failure
// mode we're guarding against).
func flatString(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func TestNormalizeFrontendMetadataWithIndex_NilFrontendIsSafe(t *testing.T) {
	// Defensive: a nil frontend pointer must NOT panic. Partially-
	// constructed configs are common during initial parsing.
	assert.NotPanics(t, func() {
		normalizeFrontendMetadataWithIndex(nil, nil)
	})
	assert.NotPanics(t, func() {
		// Empty bindIndex must also be safe — the .Name lookup just
		// misses and the binds branch is skipped.
		normalizeFrontendMetadataWithIndex(nil, map[string]map[string]*models.Bind{})
	})
}

func TestNormalizeFrontendMetadataWithIndex_FlattensEverySubCollection(t *testing.T) {
	// Build a Frontend with EVERY sub-collection populated. Each
	// entry's Metadata is in the nested {value: ...} form so a
	// regression in any single branch leaves that collection's
	// metadata visibly unflattened.
	bind := &models.Bind{Metadata: nestedMD("bind", "B")}
	bindIndex := map[string]map[string]*models.Bind{
		"frontend-A": {"http": bind},
	}

	f := &models.Frontend{
		FrontendBase: models.FrontendBase{
			Name:     "frontend-A",
			Metadata: nestedMD("self", "F"),
		},
		ACLList: models.Acls{
			&models.ACL{ACLName: "is_api", Metadata: nestedMD("acl", "A")},
		},
		BackendSwitchingRuleList: models.BackendSwitchingRules{
			&models.BackendSwitchingRule{Metadata: nestedMD("bsr", "BSR")},
		},
		CaptureList: models.Captures{
			&models.Capture{Metadata: nestedMD("cap", "C")},
		},
		FilterList: models.Filters{
			&models.Filter{Metadata: nestedMD("filt", "FL")},
		},
		HTTPAfterResponseRuleList: models.HTTPAfterResponseRules{
			&models.HTTPAfterResponseRule{Metadata: nestedMD("har", "HAR")},
		},
		HTTPErrorRuleList: models.HTTPErrorRules{
			&models.HTTPErrorRule{Metadata: nestedMD("herr", "HER")},
		},
		HTTPRequestRuleList: models.HTTPRequestRules{
			&models.HTTPRequestRule{Metadata: nestedMD("hreq", "HRQ")},
		},
		HTTPResponseRuleList: models.HTTPResponseRules{
			&models.HTTPResponseRule{Metadata: nestedMD("hres", "HRS")},
		},
		LogTargetList: models.LogTargets{
			&models.LogTarget{Metadata: nestedMD("log", "L")},
		},
		QUICInitialRuleList: models.QUICInitialRules{
			&models.QUICInitialRule{Metadata: nestedMD("quic", "Q")},
		},
		SSLFrontUses: models.SSLFrontUses{
			&models.SSLFrontUse{Metadata: nestedMD("ssl", "S")},
		},
		TCPRequestRuleList: models.TCPRequestRules{
			&models.TCPRequestRule{Metadata: nestedMD("tcp", "T")},
		},
	}

	normalizeFrontendMetadataWithIndex(f, bindIndex)

	// Per-collection assertions — naming each collection in the
	// message so a missing-branch regression produces a clear failure.
	type collectionCase struct {
		name string
		md   map[string]any
		key  string
		want string
	}
	cases := []collectionCase{
		{name: "Frontend.Metadata (self)", md: f.Metadata, key: "self", want: "F"},
		{name: "Bind via bindIndex side-channel", md: bind.Metadata, key: "bind", want: "B"},
		{name: "ACLList[0]", md: f.ACLList[0].Metadata, key: "acl", want: "A"},
		{name: "BackendSwitchingRuleList[0]", md: f.BackendSwitchingRuleList[0].Metadata, key: "bsr", want: "BSR"},
		{name: "CaptureList[0]", md: f.CaptureList[0].Metadata, key: "cap", want: "C"},
		{name: "FilterList[0]", md: f.FilterList[0].Metadata, key: "filt", want: "FL"},
		{name: "HTTPAfterResponseRuleList[0]", md: f.HTTPAfterResponseRuleList[0].Metadata, key: "har", want: "HAR"},
		{name: "HTTPErrorRuleList[0]", md: f.HTTPErrorRuleList[0].Metadata, key: "herr", want: "HER"},
		{name: "HTTPRequestRuleList[0]", md: f.HTTPRequestRuleList[0].Metadata, key: "hreq", want: "HRQ"},
		{name: "HTTPResponseRuleList[0]", md: f.HTTPResponseRuleList[0].Metadata, key: "hres", want: "HRS"},
		{name: "LogTargetList[0]", md: f.LogTargetList[0].Metadata, key: "log", want: "L"},
		{name: "QUICInitialRuleList[0]", md: f.QUICInitialRuleList[0].Metadata, key: "quic", want: "Q"},
		{name: "SSLFrontUses[0]", md: f.SSLFrontUses[0].Metadata, key: "ssl", want: "S"},
		{name: "TCPRequestRuleList[0]", md: f.TCPRequestRuleList[0].Metadata, key: "tcp", want: "T"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flatString(t, tc.md, tc.key)
			assert.Equal(t, tc.want, got,
				"%s metadata must be flattened from {value: ...} to <value>; "+
					"a regression that dropped this collection's normalize loop "+
					"would leave nested metadata visible and silently corrupt "+
					"comparator drift detection downstream", tc.name)
		})
	}
}

func TestNormalizeFrontendMetadataWithIndex_BindIndexMissForFrontendIsSilent(t *testing.T) {
	// The bindIndex is keyed by frontend name. A frontend with no
	// binds (or whose binds aren't in the index — e.g. parser
	// produced a frontend without binds) must NOT panic when the
	// index lookup misses; the binds branch must short-circuit
	// cleanly via the comma-ok pattern.
	f := &models.Frontend{
		FrontendBase: models.FrontendBase{
			Name:     "frontend-without-binds",
			Metadata: nestedMD("self", "F"),
		},
	}
	// bindIndex contains a different frontend's binds.
	bindIndex := map[string]map[string]*models.Bind{
		"some-other-frontend": {
			"http": &models.Bind{Metadata: nestedMD("other", "X")},
		},
	}

	assert.NotPanics(t, func() {
		normalizeFrontendMetadataWithIndex(f, bindIndex)
	})

	// The frontend itself must still be normalized — the bind miss
	// must NOT short-circuit the rest of the function.
	assert.Equal(t, "F", flatString(t, f.Metadata, "self"),
		"a bindIndex miss for this frontend must NOT prevent the frontend's "+
			"own metadata (and other sub-collections) from being normalized")

	// And the OTHER frontend's binds in the index must NOT be
	// touched — only the binds keyed by THIS frontend's name should
	// be visited.
	otherBind := bindIndex["some-other-frontend"]["http"]
	otherKey, ok := otherBind.Metadata["other"].(map[string]any)
	assert.True(t, ok && otherKey["value"] == "X",
		"binds belonging to other frontends must NOT be normalized by this call; "+
			"a regression that iterated the entire bindIndex (instead of just the "+
			"current frontend's entry) would mutate unrelated frontends' binds and "+
			"could double-normalize them on later passes")
}

func TestNormalizeFrontendMetadataWithIndex_NilEntriesInCollectionsPanic(t *testing.T) {
	// Document (and pin as expected behaviour) that the implementation
	// does NOT nil-check entries inside its sub-collections. The loops
	// dereference the entry pointer directly to assign .Metadata, so a
	// nil entry triggers a panic. This is documented as the caller's
	// responsibility (the parser never produces nil entries) — pinning
	// it here makes any future "helpful" refactor that adds nil checks
	// surface explicitly so it can be reviewed against the contract.
	bindIndex := map[string]map[string]*models.Bind{
		"frontend-A": {"http": &models.Bind{Metadata: nestedMD("bind", "B")}},
	}
	f := &models.Frontend{
		FrontendBase: models.FrontendBase{Name: "frontend-A"},
		ACLList: models.Acls{
			nil, // deliberate nil entry to trigger the contract
		},
	}

	assert.Panics(t, func() {
		normalizeFrontendMetadataWithIndex(f, bindIndex)
	}, "the loops dereference entry pointers directly without nil-checking; a "+
		"nil entry in any sub-collection MUST panic. If this assertion ever "+
		"flips to NotPanics it means someone added nil-tolerance, which should "+
		"be a deliberate contract change reviewed against the parser's "+
		"never-produces-nil-entries invariant")
}
