// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package introspection

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// handleVar splits errors from registry.GetWithField into TWO HTTP
// responses based on whether the error string contains "not found":
//
//	if strings.Contains(err.Error(), "not found") {
//	    WriteError(w, http.StatusNotFound, err.Error())
//	} else {
//	    WriteError(w, http.StatusInternalServerError, err.Error())
//	}
//
// The existing TestServer_HandleVar/not_found case covers the 404
// branch. The 500 branch (any non-"not found" error) is currently
// uncovered.
//
// The 500 branch matters because it is how operators learn about
// MISCONFIGURED debug requests vs MISSING resources:
//
//   - 404 means "you asked for /debug/vars/foo and there's no foo
//     registered" → operator should check the variable name.
//   - 500 means "you asked for /debug/vars/config?field={.bad[..." →
//     operator should fix the JSONPath query, the variable IS there.
//
// A regression that collapsed both branches to the same status code
// (e.g. always 404, or always 500) would break debug-tooling
// dashboards that distinguish "no such metric" from "bad query" — and
// silently degrade the debugging experience right when operators most
// need it (during an incident).
//
// Pin the 500 branch with a real HTTP round-trip: publish a known
// variable, query it with an invalid JSONPath expression, assert the
// response is 500 with the underlying jsonpath error visible in the
// body so operators can fix their query.
func TestServer_HandleVar_InvalidJSONPathReturns500(t *testing.T) {
	registry := NewRegistry()
	registry.Publish("config", Func(func() (any, error) {
		return map[string]any{"version": "1.0"}, nil
	}))

	server := NewServer("localhost:0", registry)
	cancel := startServer(t, server)
	defer cancel()

	// "{.invalid[" is a syntactically broken JSONPath — ExtractField
	// returns an error wrapping "invalid jsonpath expression".
	resp, err := http.Get("http://" + server.addrForTest() + "/debug/vars/config?field={.invalid[")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"a non-'not found' error from registry.GetWithField MUST surface as "+
			"HTTP 500. A regression that collapsed this to 404 would mislead "+
			"operators into thinking the variable is missing when their JSONPath "+
			"query is the actual problem")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "invalid jsonpath",
		"the error body MUST mention the underlying jsonpath problem so "+
			"operators can fix their query without having to re-derive what "+
			"went wrong from logs")
}
