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

// HTTPRequests is the wrapper that intercepts EE actions
// (waf-evaluate, botmgmt-evaluate) before delegating to client-native
// for CE actions. The CRUD methods on the wrapper need to keep both
// halves coherent so the index space stays consistent across the EE
// and CE buckets.

func TestHTTPRequests_GetParserName(t *testing.T) {
	p := NewHTTPRequests()
	assert.Equal(t, "http-request", p.GetParserName())
}

// Init must reset both EE and CE state so the wrapper can be reused
// across config reloads.
func TestHTTPRequests_Init_ClearsState(t *testing.T) {
	p := NewHTTPRequests()
	require.NoError(t, p.Insert(&EEHTTPRequestAction{Type: "waf-evaluate"}, 0))
	p.SetPreComments([]string{"# old"})

	p.Init()

	pc, _ := p.GetPreComments()
	assert.Nil(t, pc, "Init must clear pre-comments")
	assert.Empty(t, p.GetEEActions(), "Init must clear EE actions")
}

// Insert + GetEEActions + Set + Delete round-trip on EE actions
// alone. Index 0..len(eeActions)-1 addresses EE actions; higher
// indexes route to the CE parser. Pin the boundaries.
func TestHTTPRequests_EECRUDRoundTrip(t *testing.T) {
	p := NewHTTPRequests()

	// Insert at index 0 (prepend on empty == append)
	a1 := &EEHTTPRequestAction{Type: "waf-evaluate", Profile: "main"}
	require.NoError(t, p.Insert(a1, 0))

	// Insert at index 999 (>= len, appends)
	a2 := &EEHTTPRequestAction{Type: "botmgmt-evaluate", Profile: "bots"}
	require.NoError(t, p.Insert(a2, 999))

	// Insert in the middle
	mid := &EEHTTPRequestAction{Type: "waf-evaluate", Profile: "secondary"}
	require.NoError(t, p.Insert(mid, 1))

	// Order is now: [a1, mid, a2]
	got, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "main", got.(*EEHTTPRequestAction).Profile)

	got, err = p.GetOne(1)
	require.NoError(t, err)
	assert.Equal(t, "secondary", got.(*EEHTTPRequestAction).Profile)

	got, err = p.GetOne(2)
	require.NoError(t, err)
	assert.Equal(t, "bots", got.(*EEHTTPRequestAction).Profile)

	// Set replaces in place
	replace := &EEHTTPRequestAction{Type: "waf-evaluate", Profile: "tertiary"}
	require.NoError(t, p.Set(replace, 1))
	got, _ = p.GetOne(1)
	assert.Equal(t, "tertiary", got.(*EEHTTPRequestAction).Profile)

	// Delete in the middle
	require.NoError(t, p.Delete(1))
	// Order is now: [a1, a2]
	got, _ = p.GetOne(0)
	assert.Equal(t, "main", got.(*EEHTTPRequestAction).Profile)
	got, _ = p.GetOne(1)
	assert.Equal(t, "bots", got.(*EEHTTPRequestAction).Profile)

	// GetEEActions returns the per-EE-bucket view
	ee := p.GetEEActions()
	assert.Len(t, ee, 2)
	assert.Equal(t, "main", ee[0].Profile)
	assert.Equal(t, "bots", ee[1].Profile)
}

// SetPreComments / GetPreComments form a simple roundtrip — pin that
// SetPreComments stores the slice and GetPreComments returns it
// verbatim (no defensive copy is the documented contract).
func TestHTTPRequests_PreCommentsRoundTrip(t *testing.T) {
	p := NewHTTPRequests()

	// Initial state: nil
	pc, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Nil(t, pc)

	// Set then get
	p.SetPreComments([]string{"# header", "# subheader"})
	pc, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# header", "# subheader"}, pc)
}

// Get returns a HTTPRequestsData with EEActions populated. CE actions
// from client-native are populated only if any have been parsed —
// for this test we focus on the EE side because it doesn't require
// real client-native data to be added.
func TestHTTPRequests_Get_ReturnsHTTPRequestsData(t *testing.T) {
	p := NewHTTPRequests()
	require.NoError(t, p.Insert(&EEHTTPRequestAction{Type: "waf-evaluate", Profile: "main"}, 0))

	got, err := p.Get(false)
	require.NoError(t, err)
	require.NotNil(t, got)

	data, ok := got.(*HTTPRequestsData)
	require.True(t, ok, "Get must return *HTTPRequestsData")
	require.Len(t, data.EEActions, 1)
	assert.Equal(t, "main", data.EEActions[0].Profile)
}

// ResultAll serializes EE actions back to ReturnResultLine shape:
// "http-request <type> [profile <name>] [if|unless <cond>]". Pin the
// optional fields and the comment passthrough.
func TestHTTPRequests_ResultAll_EEActionFormat(t *testing.T) {
	p := NewHTTPRequests()

	require.NoError(t, p.Insert(&EEHTTPRequestAction{
		Type:     "waf-evaluate",
		Profile:  "main",
		Cond:     "if",
		CondTest: "{ src 10.0.0.0/8 }",
		Comment:  "company traffic",
	}, 0))
	// Type-only action (no profile, no condition).
	require.NoError(t, p.Insert(&EEHTTPRequestAction{Type: "botmgmt-evaluate"}, 1))
	// Profile only, no condition.
	require.NoError(t, p.Insert(&EEHTTPRequestAction{
		Type:    "waf-evaluate",
		Profile: "audit",
	}, 2))

	results, _, err := p.ResultAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 3)

	assert.Equal(t, "http-request waf-evaluate profile main if { src 10.0.0.0/8 }", results[0].Data)
	assert.Equal(t, "company traffic", results[0].Comment)

	assert.Equal(t, "http-request botmgmt-evaluate", results[1].Data,
		"type-only action must serialize without trailing space")

	assert.Equal(t, "http-request waf-evaluate profile audit", results[2].Data)
}
