// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package parser

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// normalizeBackendMetadataWithIndexes is the Backend cousin of
// normalizeFrontendMetadataWithIndex. It visits THIRTEEN distinct
// sub-collections on a Backend (ACLs, Filters, HTTPAfterResponse/
// Check/Error/Request/Response rules, LogTargets, ServerSwitchingRules,
// StickRules, TCPCheck/Request/Response rules) PLUS the Backend
// itself PLUS servers and server templates via two side-channel
// pointer indexes (serverIndex, serverTemplateIndex).
//
// The high-risk regression is identical to the Frontend one: "added a
// new sub-collection but forgot to add a normalize loop for it" is
// completely silent until comparator drift surfaces later. The
// Backend has the additional risk of TWO side-channel indexes (vs the
// Frontend's one bindIndex) — so a regression that mixed up which
// index is keyed on what would be even harder to spot.

// bNestedMD / bFlat are local helpers (b-prefix avoids collision with
// the Frontend-test helpers if both files end up in the same package
// after a parallel-MR merge).
func bNestedMD(key, value string) map[string]any {
	return map[string]any{key: map[string]any{"value": value}}
}

func bFlat(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func TestNormalizeBackendMetadataWithIndexes_NilBackendIsSafe(t *testing.T) {
	// Defensive: a nil backend pointer must NOT panic. Same contract
	// as the Frontend variant.
	assert.NotPanics(t, func() {
		normalizeBackendMetadataWithIndexes(nil, nil, nil)
	})
	assert.NotPanics(t, func() {
		normalizeBackendMetadataWithIndexes(
			nil,
			map[string]map[string]*models.Server{},
			map[string]map[string]*models.ServerTemplate{},
		)
	})
}

func TestNormalizeBackendMetadataWithIndexes_FlattensEverySubCollection(t *testing.T) {
	// Build a Backend with EVERY sub-collection populated. Each
	// entry's Metadata is in the nested {value: ...} form so a
	// regression in any single branch leaves that collection's
	// metadata visibly unflattened.
	server := &models.Server{Metadata: bNestedMD("srv", "S")}
	template := &models.ServerTemplate{Metadata: bNestedMD("tmpl", "T")}
	serverIndex := map[string]map[string]*models.Server{
		"backend-A": {"srv1": server},
	}
	serverTemplateIndex := map[string]map[string]*models.ServerTemplate{
		"backend-A": {"tmpl1": template},
	}

	b := &models.Backend{
		BackendBase: models.BackendBase{
			Name:     "backend-A",
			Metadata: bNestedMD("self", "B"),
		},
		ACLList: models.Acls{
			&models.ACL{ACLName: "is_api", Metadata: bNestedMD("acl", "A")},
		},
		FilterList: models.Filters{
			&models.Filter{Metadata: bNestedMD("filt", "FL")},
		},
		HTTPAfterResponseRuleList: models.HTTPAfterResponseRules{
			&models.HTTPAfterResponseRule{Metadata: bNestedMD("har", "HAR")},
		},
		HTTPCheckList: models.HTTPChecks{
			&models.HTTPCheck{Metadata: bNestedMD("hcheck", "HCK")},
		},
		HTTPErrorRuleList: models.HTTPErrorRules{
			&models.HTTPErrorRule{Metadata: bNestedMD("herr", "HER")},
		},
		HTTPRequestRuleList: models.HTTPRequestRules{
			&models.HTTPRequestRule{Metadata: bNestedMD("hreq", "HRQ")},
		},
		HTTPResponseRuleList: models.HTTPResponseRules{
			&models.HTTPResponseRule{Metadata: bNestedMD("hres", "HRS")},
		},
		LogTargetList: models.LogTargets{
			&models.LogTarget{Metadata: bNestedMD("log", "L")},
		},
		ServerSwitchingRuleList: models.ServerSwitchingRules{
			&models.ServerSwitchingRule{Metadata: bNestedMD("ssr", "SSR")},
		},
		StickRuleList: models.StickRules{
			&models.StickRule{Metadata: bNestedMD("stick", "ST")},
		},
		TCPCheckRuleList: models.TCPChecks{
			&models.TCPCheck{Metadata: bNestedMD("tcpchk", "TCK")},
		},
		TCPRequestRuleList: models.TCPRequestRules{
			&models.TCPRequestRule{Metadata: bNestedMD("tcpreq", "TRQ")},
		},
		TCPResponseRuleList: models.TCPResponseRules{
			&models.TCPResponseRule{Metadata: bNestedMD("tcpres", "TRS")},
		},
	}

	normalizeBackendMetadataWithIndexes(b, serverIndex, serverTemplateIndex)

	// Per-collection assertions — naming each so a missing-branch
	// regression produces a clear failure pointing to the dropped
	// collection.
	type collectionCase struct {
		name string
		md   map[string]any
		key  string
		want string
	}
	cases := []collectionCase{
		{name: "Backend.Metadata (self)", md: b.Metadata, key: "self", want: "B"},
		{name: "Server via serverIndex side-channel", md: server.Metadata, key: "srv", want: "S"},
		{name: "ServerTemplate via serverTemplateIndex side-channel", md: template.Metadata, key: "tmpl", want: "T"},
		{name: "ACLList[0]", md: b.ACLList[0].Metadata, key: "acl", want: "A"},
		{name: "FilterList[0]", md: b.FilterList[0].Metadata, key: "filt", want: "FL"},
		{name: "HTTPAfterResponseRuleList[0]", md: b.HTTPAfterResponseRuleList[0].Metadata, key: "har", want: "HAR"},
		{name: "HTTPCheckList[0]", md: b.HTTPCheckList[0].Metadata, key: "hcheck", want: "HCK"},
		{name: "HTTPErrorRuleList[0]", md: b.HTTPErrorRuleList[0].Metadata, key: "herr", want: "HER"},
		{name: "HTTPRequestRuleList[0]", md: b.HTTPRequestRuleList[0].Metadata, key: "hreq", want: "HRQ"},
		{name: "HTTPResponseRuleList[0]", md: b.HTTPResponseRuleList[0].Metadata, key: "hres", want: "HRS"},
		{name: "LogTargetList[0]", md: b.LogTargetList[0].Metadata, key: "log", want: "L"},
		{name: "ServerSwitchingRuleList[0]", md: b.ServerSwitchingRuleList[0].Metadata, key: "ssr", want: "SSR"},
		{name: "StickRuleList[0]", md: b.StickRuleList[0].Metadata, key: "stick", want: "ST"},
		{name: "TCPCheckRuleList[0]", md: b.TCPCheckRuleList[0].Metadata, key: "tcpchk", want: "TCK"},
		{name: "TCPRequestRuleList[0]", md: b.TCPRequestRuleList[0].Metadata, key: "tcpreq", want: "TRQ"},
		{name: "TCPResponseRuleList[0]", md: b.TCPResponseRuleList[0].Metadata, key: "tcpres", want: "TRS"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bFlat(t, tc.md, tc.key)
			assert.Equal(t, tc.want, got,
				"%s metadata must be flattened from {value: ...} to <value>; "+
					"a regression that dropped this collection's normalize loop "+
					"would leave nested metadata visible and silently corrupt "+
					"comparator drift detection downstream", tc.name)
		})
	}
}

func TestNormalizeBackendMetadataWithIndexes_SideChannelMissesAreSilent(t *testing.T) {
	// The Backend has TWO side-channel indexes (vs the Frontend's
	// one bindIndex). Both are keyed by backend name. A backend
	// whose name doesn't appear in either index must short-circuit
	// each lookup cleanly without panicking, and MUST NOT mutate
	// entries belonging to other backends.
	otherServer := &models.Server{Metadata: bNestedMD("other-srv", "OS")}
	otherTemplate := &models.ServerTemplate{Metadata: bNestedMD("other-tmpl", "OT")}

	serverIndex := map[string]map[string]*models.Server{
		"some-other-backend": {"srv": otherServer},
	}
	serverTemplateIndex := map[string]map[string]*models.ServerTemplate{
		"some-other-backend": {"tmpl": otherTemplate},
	}

	b := &models.Backend{
		BackendBase: models.BackendBase{
			Name:     "backend-without-servers-or-templates",
			Metadata: bNestedMD("self", "B"),
		},
	}

	assert.NotPanics(t, func() {
		normalizeBackendMetadataWithIndexes(b, serverIndex, serverTemplateIndex)
	})

	// The backend itself must still be normalized — neither index
	// miss should short-circuit the rest of the function.
	assert.Equal(t, "B", bFlat(t, b.Metadata, "self"),
		"index misses for this backend must NOT prevent the backend's own "+
			"metadata (or any other sub-collection) from being normalized")

	// Other backends' servers/templates must NOT be touched. A
	// regression that flat-iterated the entire index (instead of
	// just this backend's entry) would mutate unrelated backends'
	// servers and could double-normalize on later passes.
	otherSrvNested, ok := otherServer.Metadata["other-srv"].(map[string]any)
	assert.True(t, ok && otherSrvNested["value"] == "OS",
		"servers belonging to other backends must NOT be normalized by this call; "+
			"a regression that iterated the entire serverIndex would mutate unrelated "+
			"backends' servers")
	otherTmplNested, ok := otherTemplate.Metadata["other-tmpl"].(map[string]any)
	assert.True(t, ok && otherTmplNested["value"] == "OT",
		"server templates belonging to other backends must NOT be normalized by "+
			"this call; same risk as the serverIndex side")
}

func TestNormalizeBackendMetadataWithIndexes_NilEntriesInCollectionsPanic(t *testing.T) {
	// Document (and pin as expected behaviour) that the implementation
	// does NOT nil-check entries inside its sub-collections — same
	// contract as the Frontend variant. The parser's invariant is
	// that it never produces nil entries; if a future refactor adds
	// nil-tolerance, this assertion will flip and force the contract
	// change to be reviewed.
	b := &models.Backend{
		BackendBase: models.BackendBase{Name: "backend-A"},
		ACLList: models.Acls{
			nil, // deliberate nil entry to trigger the contract
		},
	}

	assert.Panics(t, func() {
		normalizeBackendMetadataWithIndexes(b, nil, nil)
	}, "the loops dereference entry pointers directly without nil-checking; a "+
		"nil entry in any sub-collection MUST panic. If this assertion ever "+
		"flips to NotPanics it means someone added nil-tolerance, which should "+
		"be a deliberate contract change reviewed against the parser's "+
		"never-produces-nil-entries invariant")
}
