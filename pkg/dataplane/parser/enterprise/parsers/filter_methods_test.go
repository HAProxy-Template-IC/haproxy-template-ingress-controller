// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package parsers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Filters is the wrapper that intercepts EE filter directives
// (filter waf, filter botmgmt) before delegating to client-native
// for CE filters (compression, trace, etc.). The CRUD methods need
// to keep the EE and CE buckets coherent so the index space stays
// consistent across both halves.
//
// Mirrors the structure of the HTTPRequests/HTTPResponses test
// suites since the three EE wrappers share the same shape.

func TestFilters_GetParserName(t *testing.T) {
	p := NewFilters()
	assert.Equal(t, "filter", p.GetParserName())
}

func TestFilters_Init_ClearsState(t *testing.T) {
	p := NewFilters()
	require.NoError(t, p.Insert(&EEFilter{Type: "waf", Name: "main"}, 0))
	p.SetPreComments([]string{"# old"})

	p.Init()

	pc, _ := p.GetPreComments()
	assert.Nil(t, pc, "Init must clear pre-comments")
	assert.Empty(t, p.GetEEFilters(), "Init must clear EE filters")
}

// EE CRUD round-trip — Insert (prepend/append/middle), GetOne, Set,
// Delete with order assertions. Index 0..len(eeFilters)-1 addresses
// the EE bucket; higher indexes route to the CE parser.
func TestFilters_EECRUDRoundTrip(t *testing.T) {
	p := NewFilters()

	f1 := &EEFilter{Type: "waf", Name: "main", RulesFile: "/etc/waf.conf"}
	require.NoError(t, p.Insert(f1, 0))

	f2 := &EEFilter{Type: "botmgmt", Profile: "bots"}
	require.NoError(t, p.Insert(f2, 999)) // append (>=len)

	mid := &EEFilter{Type: "waf", Name: "secondary"}
	require.NoError(t, p.Insert(mid, 1))

	// Order: [f1, mid, f2]
	got, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "main", got.(*EEFilter).Name)

	got, err = p.GetOne(1)
	require.NoError(t, err)
	assert.Equal(t, "secondary", got.(*EEFilter).Name)

	got, err = p.GetOne(2)
	require.NoError(t, err)
	assert.Equal(t, "bots", got.(*EEFilter).Profile)

	// Set replaces in place.
	replaced := &EEFilter{Type: "waf", Name: "tertiary"}
	require.NoError(t, p.Set(replaced, 1))
	got, _ = p.GetOne(1)
	assert.Equal(t, "tertiary", got.(*EEFilter).Name)

	// Delete in the middle.
	require.NoError(t, p.Delete(1))
	got, _ = p.GetOne(0)
	assert.Equal(t, "main", got.(*EEFilter).Name)
	got, _ = p.GetOne(1)
	assert.Equal(t, "bots", got.(*EEFilter).Profile)

	// GetEEFilters exposes the per-EE-bucket view used by extractors.
	ee := p.GetEEFilters()
	assert.Len(t, ee, 2)
	assert.Equal(t, "main", ee[0].Name)
	assert.Equal(t, "bots", ee[1].Profile)
}

func TestFilters_PreCommentsRoundTrip(t *testing.T) {
	p := NewFilters()

	pc, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Nil(t, pc)

	p.SetPreComments([]string{"# header"})
	pc, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# header"}, pc)
}

func TestFilters_Get_ReturnsFiltersData(t *testing.T) {
	p := NewFilters()
	require.NoError(t, p.Insert(&EEFilter{Type: "waf", Name: "main"}, 0))

	got, err := p.Get(false)
	require.NoError(t, err)
	require.NotNil(t, got)

	data, ok := got.(*FiltersData)
	require.True(t, ok, "Get must return *FiltersData")
	require.Len(t, data.EEFilters, 1)
	assert.Equal(t, "main", data.EEFilters[0].Name)
}

// ResultAll serializes EE filters back to ReturnResultLine shape:
// "filter <type> [<name>] [learning] [rules-file <path>] [profile
// <name>] [log-enable]". Pin every optional field branch and the
// type-only / name-only / multi-option combinations.
func TestFilters_ResultAll_EEFilterFormat(t *testing.T) {
	p := NewFilters()

	// WAF filter: name + rules-file + learning + log-enable + comment.
	require.NoError(t, p.Insert(&EEFilter{
		Type:       "waf",
		Name:       "main",
		RulesFile:  "/etc/waf.conf",
		Learning:   true,
		LogEnabled: true,
		Comment:    "production",
	}, 0))
	// BotMgmt filter: profile only.
	require.NoError(t, p.Insert(&EEFilter{
		Type:    "botmgmt",
		Profile: "bots",
	}, 1))
	// Type-only filter (no optional fields).
	require.NoError(t, p.Insert(&EEFilter{Type: "type-only"}, 2))

	results, _, err := p.ResultAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 3)

	assert.Equal(t,
		"filter waf main learning rules-file /etc/waf.conf log-enable",
		results[0].Data,
		"WAF filter serializes name first, then learning, then rules-file, then log-enable",
	)
	assert.Equal(t, "production", results[0].Comment)

	assert.Equal(t, "filter botmgmt profile bots", results[1].Data)

	assert.Equal(t, "filter type-only", results[2].Data,
		"type-only filter must serialize without trailing space")
}
