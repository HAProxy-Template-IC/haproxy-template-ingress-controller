// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package introspection

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The existing TestServer_HandleAllVars tests the success path: every
// registered Var.Get() returns nil error. The 500 error branch — what
// happens when ANY single Var.Get() fails — is unexercised. This is
// the load-bearing failure mode for the /debug/vars/all endpoint
// because operators rely on its 200/500 status to know whether the
// snapshot is complete.
//
// Two contracts to pin:
//
//  1. A failing Var must surface as HTTP 500 (not silently dropped or
//     papered over with a partial response). Returning a partial 200
//     would let operators believe they have a complete snapshot when
//     a critical variable failed to read — for example, if `config`
//     fails because the controller is mid-reload, the operator must
//     see 500 rather than a {"credentials":...} response missing
//     `config`.
//
//  2. The failing variable's path must be in the error body so the
//     operator knows WHICH var to investigate. registry.All wraps the
//     inner error with `getting variable "<path>": <err>` for exactly
//     this purpose; handleAllVars surfaces that string verbatim.
//
// These guards prevent a regression that would change the failure
// from 500-with-context to 200-with-partial-data.

// failingVar is a Var that always returns an error from Get(). Used to
// drive registry.All into its error branch.
type failingVar struct {
	failure error
}

func (f *failingVar) Get() (any, error) {
	return nil, f.failure
}

func TestHandleAllVars_PropagatesVarErrorAs500(t *testing.T) {
	registry := NewRegistry()

	// Register one healthy var and one failing var. The failing one
	// must be enough to fail the whole response — there's no
	// "best-effort partial dump" mode by design.
	registry.Publish("healthy", Func(func() (any, error) {
		return map[string]string{"key": "value"}, nil
	}))
	registry.Publish("config", &failingVar{
		failure: errors.New("config not loaded yet"),
	})

	server := NewServer("localhost:0", registry)
	cancel := startServer(t, server)
	defer cancel()

	resp, err := http.Get("http://" + server.addrForTest() + "/debug/vars/all")
	require.NoError(t, err)
	defer resp.Body.Close()

	// (1) Status code: a single failing var must propagate as 500.
	// A regression that returned 200 with the partial healthy data
	// would mislead operators into thinking the snapshot is complete.
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode,
		"any failing Var.Get() must surface as HTTP 500; a regression that "+
			"returned 200 with partial data would let operators believe they "+
			"have a complete snapshot when a critical variable failed to read")

	// (2) Error body must mention the failing variable path so the
	// operator knows WHICH var to investigate. Without this an
	// operator would get a generic 500 and have to grep code to find
	// which Var was failing.
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Contains(t, body, "error",
		"500 responses must use the structured {\"error\": ...} envelope so "+
			"clients can parse them programmatically")
	assert.Contains(t, body["error"], "config",
		"the failing variable path must appear in the error body so operators "+
			"see which Var failed without grepping code")
	assert.Contains(t, body["error"], "config not loaded yet",
		"the underlying Var error message must be preserved verbatim so the "+
			"operator sees the actual failure cause, not just 'something failed'")
}

func TestWriteJSONWithStatus_HeadersWrittenBeforeEncodeError(t *testing.T) {
	// WriteJSONWithStatus has a documented contract: if json.Encode
	// fails AFTER the status header is written, the function silently
	// returns (no panic, no second WriteHeader call) — the partial or
	// empty body signals the error to the client.
	//
	// The existing TestWriteJSONWithStatus only covers the success
	// path. This test pins the silent-failure path by passing a value
	// that json.Encoder cannot encode (a channel — channels have no
	// JSON representation).
	w := httptest.NewRecorder()

	// chan int is the canonical un-encodable type. Wrapping it in any
	// is what json.Encoder will choke on.
	unencodable := map[string]any{"ch": make(chan int)}

	require.NotPanics(t, func() {
		WriteJSONWithStatus(w, http.StatusServiceUnavailable, unencodable)
	}, "encode failure must NOT panic — the partial body is the "+
		"signal, not a process crash")

	// Status code: the WriteHeader call happens BEFORE the encode
	// attempt, so the requested status must be visible to the client
	// even though the body is empty/partial. A regression that
	// reordered the calls would change the wire-visible status to 200
	// and silently mask the original status the caller asked for.
	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"WriteHeader fires BEFORE Encode; the status the caller asked for "+
			"must be visible to the client even when the body is partial")

	// Content-Type is also set before the encode attempt.
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestWriteError_ProducesStructuredErrorBody(t *testing.T) {
	// Pin the {"error": "<message>"} envelope shape that clients
	// (including handleAllVars's error branch above) rely on. The
	// existing TestWriteError already checks the envelope, but only
	// for 404. Add a table covering several status codes to guard
	// against a regression that would change the envelope shape based
	// on status code (e.g., omitting the JSON body for 5xx).
	tests := []struct {
		name    string
		code    int
		message string
	}{
		{name: "404 not found", code: http.StatusNotFound, message: "variable not found"},
		{name: "500 internal", code: http.StatusInternalServerError, message: "decoder failure"},
		{name: "503 unavailable", code: http.StatusServiceUnavailable, message: "controller not ready"},
		{name: "405 method not allowed", code: http.StatusMethodNotAllowed, message: "only GET is allowed"},
		{name: "empty message still produces JSON envelope", code: http.StatusBadRequest, message: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteError(w, tt.code, tt.message)

			assert.Equal(t, tt.code, w.Code, "status code must match the caller's request")
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"),
				"Content-Type must be application/json regardless of status code")

			var body map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&body),
				"body must be valid JSON for every status code; a regression that "+
					"omitted the body for some codes would break clients that always "+
					"json.Decode the response")
			assert.Equal(t, tt.message, body["error"],
				"the message must be in the 'error' field verbatim — a regression that "+
					"changed the envelope key (e.g. to 'message') would break every "+
					"client that does errBody[\"error\"]")
		})
	}
}
