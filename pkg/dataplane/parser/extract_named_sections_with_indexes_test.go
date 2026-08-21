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
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// extractPeersWithIndexes, extractResolversWithIndexes, and
// extractMailersWithIndexes are three SISTER extractors with the
// same shape:
//
//   - SectionsGet on the parser
//   - For each section, ParseSection populates the section struct
//   - Per-section sub-entries are parsed and stored in a pointer
//     index (PeerEntryIndex / NameserverIndex / MailerEntryIndex)
//     keyed by section name and then by entry name
//   - The section is appended to the corresponding slice on the
//     StructuredConfig (Peers / Resolvers / Mailers)
//
// The end-to-end contract is the same in all three: when the source
// config contains a named section with sub-entries, BOTH the slice
// MUST gain the section AND the index MUST be populated with every
// sub-entry under its name.
//
// The index population is the load-bearing part: downstream code
// (sync, comparator) does zero-copy iteration via the index instead
// of walking the slice every reconcile, so a regression that built
// the section but skipped the index would silently make every
// comparator pass treat all sub-entries as MISSING — producing a
// flood of spurious "delete then re-create" operations.
//
// Existing parser tests cover BACKEND/FRONTEND extraction but none
// assert on these three sister fields together. Single table-driven
// test exercises all three through the public ParseFromString
// surface (extract* functions are package-private).
func TestParseFromString_NamedSectionsWithIndexes(t *testing.T) {
	const cfg = `
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

peers mypeers
    peer haproxy1 192.168.0.1:1024
    peer haproxy2 192.168.0.2:1024

resolvers mirko
    nameserver dns_a 10.0.0.1:53
    nameserver dns_b 10.0.0.2:53

mailers mymail
    mailer smtp1 192.168.0.1:587
    mailer smtp2 192.168.0.2:587
`

	p := newTestParser(t)
	conf, err := p.ParseFromString(cfg)
	require.NoError(t, err)
	require.NotNil(t, conf)

	// Per-section probe: pull section name + sub-entry names from the
	// matching slice + index. Each closure uses its section's concrete
	// model type, but the comparison shape is identical.
	cases := []struct {
		name        string
		gotSlice    int
		gotSection  string
		gotEntries  []string
		wantSection string
		wantEntries []string
		regression  string
	}{
		{
			name:        "peers — slice + PeerEntryIndex populated",
			gotSlice:    len(conf.Peers),
			gotSection:  ifNonEmpty(conf.Peers, func(i int) string { return conf.Peers[i].Name }),
			gotEntries:  sortedKeys(conf.PeerEntryIndex["mypeers"]),
			wantSection: "mypeers",
			wantEntries: []string{"haproxy1", "haproxy2"},
			regression: "comparator iterates PeerEntryIndex for zero-copy peer-entry diff; " +
				"a regression that skipped index population would treat every peer entry " +
				"as missing on every reconcile and emit spurious delete/recreate ops",
		},
		{
			name:        "resolvers — slice + NameserverIndex populated",
			gotSlice:    len(conf.Resolvers),
			gotSection:  ifNonEmpty(conf.Resolvers, func(i int) string { return conf.Resolvers[i].Name }),
			gotEntries:  sortedKeys(conf.NameserverIndex["mirko"]),
			wantSection: "mirko",
			wantEntries: []string{"dns_a", "dns_b"},
			regression: "comparator iterates NameserverIndex for nameserver diff; missing index " +
				"means every nameserver looks deleted on every reconcile and DNS resolver config thrashes",
		},
		{
			name:        "mailers — slice + MailerEntryIndex populated",
			gotSlice:    len(conf.Mailers),
			gotSection:  ifNonEmpty(conf.Mailers, func(i int) string { return conf.Mailers[i].Name }),
			gotEntries:  sortedKeys(conf.MailerEntryIndex["mymail"]),
			wantSection: "mymail",
			wantEntries: []string{"smtp1", "smtp2"},
			regression: "comparator iterates MailerEntryIndex for mailer-entry diff; missing index " +
				"means SMTP-alert mailers are continuously re-created and send duplicate alerts to oncall",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, 1, tc.gotSlice,
				"section slice length: "+tc.regression)
			assert.Equal(t, tc.wantSection, tc.gotSection,
				"section name: "+tc.regression)
			assert.Equal(t, tc.wantEntries, tc.gotEntries,
				"index entries: "+tc.regression)
		})
	}
}

// ifNonEmpty returns getName(0) when the slice has at least one
// element, otherwise the empty string. Lets the table cases above
// stay one-line per field across three different section types.
func ifNonEmpty[T any](s []T, getName func(i int) string) string {
	if len(s) == 0 {
		return ""
	}
	return getName(0)
}

// sortedKeys returns the map keys in alphabetical order so test
// expectations stay deterministic. Generic so the same helper works
// for every per-section pointer index (PeerEntryIndex,
// NameserverIndex, MailerEntryIndex) regardless of value type.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
