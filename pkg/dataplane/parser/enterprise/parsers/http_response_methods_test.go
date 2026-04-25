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

// HTTPResponses is the future-proofing wrapper that will intercept EE
// http-response actions when any are added (oidc-sso was deliberately
// skipped per the comment in http_response.go). Today, the wrapper
// delegates everything to client-native, but the EE bucket and CRUD
// machinery already need to behave correctly so the interception
// point isn't a flaky surface when the first EE action lands.
//
// Mirror the HTTPRequests test structure so the two wrappers stay
// aligned as new EE actions are added.

func TestHTTPResponses_GetParserName(t *testing.T) {
	p := NewHTTPResponses()
	assert.Equal(t, "http-response", p.GetParserName())
}

func TestHTTPResponses_Init_ClearsState(t *testing.T) {
	p := NewHTTPResponses()
	require.NoError(t, p.Insert(&EEHTTPResponseAction{Type: "future-action"}, 0))
	p.SetPreComments([]string{"# old"})

	p.Init()

	pc, _ := p.GetPreComments()
	assert.Nil(t, pc, "Init must clear pre-comments")
	assert.Empty(t, p.GetEEActions(), "Init must clear EE actions")
}

// EE CRUD round-trip — the wrapper accepts EEHTTPResponseAction even
// though no EE actions are implemented yet, so the future-extensibility
// bucket already needs the right semantics.
func TestHTTPResponses_EECRUDRoundTrip(t *testing.T) {
	p := NewHTTPResponses()

	a1 := &EEHTTPResponseAction{Type: "future-a", Profile: "main"}
	require.NoError(t, p.Insert(a1, 0))

	a2 := &EEHTTPResponseAction{Type: "future-b", Profile: "secondary"}
	require.NoError(t, p.Insert(a2, 999)) // append (>=len)

	mid := &EEHTTPResponseAction{Type: "future-c", Profile: "tertiary"}
	require.NoError(t, p.Insert(mid, 1)) // middle insert

	// Order is now: [a1, mid, a2]
	got, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "main", got.(*EEHTTPResponseAction).Profile)

	got, err = p.GetOne(1)
	require.NoError(t, err)
	assert.Equal(t, "tertiary", got.(*EEHTTPResponseAction).Profile)

	got, err = p.GetOne(2)
	require.NoError(t, err)
	assert.Equal(t, "secondary", got.(*EEHTTPResponseAction).Profile)

	// Set replaces in place
	replaced := &EEHTTPResponseAction{Type: "future-x", Profile: "quaternary"}
	require.NoError(t, p.Set(replaced, 1))
	got, _ = p.GetOne(1)
	assert.Equal(t, "quaternary", got.(*EEHTTPResponseAction).Profile)

	// Delete in the middle
	require.NoError(t, p.Delete(1))
	got, _ = p.GetOne(0)
	assert.Equal(t, "main", got.(*EEHTTPResponseAction).Profile)
	got, _ = p.GetOne(1)
	assert.Equal(t, "secondary", got.(*EEHTTPResponseAction).Profile)

	// GetEEActions exposes the per-EE-bucket view
	ee := p.GetEEActions()
	assert.Len(t, ee, 2)
}

func TestHTTPResponses_PreCommentsRoundTrip(t *testing.T) {
	p := NewHTTPResponses()

	pc, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Nil(t, pc)

	p.SetPreComments([]string{"# header"})
	pc, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# header"}, pc)
}

func TestHTTPResponses_Get_ReturnsHTTPResponsesData(t *testing.T) {
	p := NewHTTPResponses()
	require.NoError(t, p.Insert(&EEHTTPResponseAction{Type: "future-action", Profile: "main"}, 0))

	got, err := p.Get(false)
	require.NoError(t, err)
	require.NotNil(t, got)

	data, ok := got.(*HTTPResponsesData)
	require.True(t, ok, "Get must return *HTTPResponsesData")
	require.Len(t, data.EEActions, 1)
	assert.Equal(t, "main", data.EEActions[0].Profile)
}

// ResultAll serializes EE actions back to ReturnResultLine shape.
// Pin the format match with HTTPRequests (the parallel structure
// keeps callers simple).
func TestHTTPResponses_ResultAll_EEActionFormat(t *testing.T) {
	p := NewHTTPResponses()

	require.NoError(t, p.Insert(&EEHTTPResponseAction{
		Type:     "future-action",
		Profile:  "main",
		Cond:     "if",
		CondTest: "{ src 10.0.0.0/8 }",
		Comment:  "company traffic",
	}, 0))
	// Type-only (no profile, no condition).
	require.NoError(t, p.Insert(&EEHTTPResponseAction{Type: "type-only"}, 1))

	results, _, err := p.ResultAll()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 2)

	assert.Equal(t, "http-response future-action profile main if { src 10.0.0.0/8 }", results[0].Data)
	assert.Equal(t, "company traffic", results[0].Comment)

	assert.Equal(t, "http-response type-only", results[1].Data,
		"type-only action must serialize without trailing space")
}
