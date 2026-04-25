// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package enterprise

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The four decode helpers in response.go form the read-side foundation
// for every enterprise API call:
//
//   - decodeResponse[T]: typed JSON decode of a single object
//   - decodeResponseOr404[T]: same, but converts 404 to a sentinel
//     ErrNotFound so callers can errors.Is against it
//   - decodeSliceResponse[T]: typed JSON decode of an array
//   - checkResponseStatus: status check without body decoding
//
// They share two non-obvious contracts that are easy to silently
// regress and have no direct test coverage:
//
//  1. ALL of them treat the 2xx range as success [200, 300). The
//     range is half-open at the top, NOT inclusive of 300. A refactor
//     that wrote `<= 300` would silently accept HTTP 300 (Multiple
//     Choices) as a successful response and try to decode redirect
//     metadata as the typed payload.
//  2. The error messages MUST include the operation name AND the
//     status code (or decode-error text). Log scrapers and operator
//     dashboards parse these — a refactor that dropped either would
//     break correlation between API-call logs and the executor that
//     produced the failure.
//
// decodeResponseOr404 layers a third contract: 404 must return the
// SENTINEL ErrNotFound (not a wrapped fmt.Errorf), so callers'
// errors.Is(err, ErrNotFound) checks keep working. A refactor that
// wrapped the sentinel in fmt.Errorf without %w would break every
// caller's "did this resource exist?" branch.

// makeResp builds an *http.Response with the given status and JSON
// body. Callers must close resp.Body — match the production helper's
// contract documented in response.go.
func makeResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

type sample struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecodeResponse_Success(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   sample
	}{
		{name: "200 OK decodes object", status: 200, body: `{"name":"a","count":1}`, want: sample{Name: "a", Count: 1}},
		{name: "201 Created also accepted", status: 201, body: `{"name":"b","count":2}`, want: sample{Name: "b", Count: 2}},
		{name: "204 boundary at lower edge of 2xx accepted (caller drives the empty body decode)", status: 204, body: `{}`, want: sample{}},
		{name: "299 just below 300 boundary still accepted", status: 299, body: `{"name":"c"}`, want: sample{Name: "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeResp(tt.status, tt.body)
			defer resp.Body.Close()
			got, err := decodeResponse[sample](resp, "fetch sample")
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.want, *got)
		})
	}
}

func TestDecodeResponse_NonSuccessReturnsErrorWithStatusAndOperation(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "300 just outside 2xx is rejected (Multiple Choices is NOT success)", status: 300},
		{name: "400 Bad Request", status: 400},
		{name: "404 Not Found (decodeResponse does NOT special-case this; that is decodeResponseOr404's job)", status: 404},
		{name: "500 Internal Server Error", status: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Body must NOT be decoded on non-2xx. Use intentionally invalid
			// JSON so a regression that tried to decode anyway would fail
			// with a different error than the status-code one.
			resp := makeResp(tt.status, `not-json`)
			defer resp.Body.Close()
			got, err := decodeResponse[sample](resp, "fetch sample")
			require.Nil(t, got, "non-2xx must not return a decoded value")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "fetch sample",
				"error must include the operation name so log scrapers can correlate the failure")
			assert.Contains(t, err.Error(), "unexpected status",
				"error must signal it's a status-code failure (not a decode failure)")
		})
	}
}

func TestDecodeResponse_DecodeErrorOnInvalidJSON(t *testing.T) {
	// 2xx with malformed body must surface the decode error AND
	// preserve the operation name so callers can tell which API call
	// produced bad JSON.
	resp := makeResp(200, `{"name":}`)
	defer resp.Body.Close()
	got, err := decodeResponse[sample](resp, "fetch sample")
	require.Nil(t, got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fetch sample")
	assert.Contains(t, err.Error(), "decoding response")
}

func TestDecodeResponseOr404_Returns404Sentinel(t *testing.T) {
	// The sentinel discipline is the whole point of the Or404 variant.
	// errors.Is must match — a refactor that wrapped the sentinel in
	// fmt.Errorf without %w would silently break every caller's
	// "does this resource exist?" branch.
	resp := makeResp(404, `{}`)
	defer resp.Body.Close()
	got, err := decodeResponseOr404[sample](resp, "fetch sample")
	require.Nil(t, got)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound),
		"404 must return the SENTINEL ErrNotFound (or a wrapped chain) so callers can errors.Is against it; "+
			"a fmt.Errorf without %%w would break every 'does it exist?' branch")
}

func TestDecodeResponseOr404_PassesThrough2xxAndOtherErrors(t *testing.T) {
	t.Run("2xx behaves like decodeResponse", func(t *testing.T) {
		resp := makeResp(200, `{"name":"x"}`)
		defer resp.Body.Close()
		got, err := decodeResponseOr404[sample](resp, "fetch sample")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, "x", got.Name)
	})

	t.Run("500 returns a non-sentinel error (NOT ErrNotFound)", func(t *testing.T) {
		resp := makeResp(500, ``)
		defer resp.Body.Close()
		got, err := decodeResponseOr404[sample](resp, "fetch sample")
		require.Nil(t, got)
		require.Error(t, err)
		assert.False(t, errors.Is(err, ErrNotFound),
			"non-404 errors must NOT match ErrNotFound — otherwise callers would treat genuine server failures as 'resource missing' and skip retry")
		assert.Contains(t, err.Error(), "fetch sample")
		assert.Contains(t, err.Error(), "unexpected status")
	})
}

func TestDecodeSliceResponse(t *testing.T) {
	t.Run("2xx decodes array", func(t *testing.T) {
		resp := makeResp(200, `[{"name":"a"},{"name":"b","count":3}]`)
		defer resp.Body.Close()
		got, err := decodeSliceResponse[sample](resp, "list samples")
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, sample{Name: "a"}, got[0])
		assert.Equal(t, sample{Name: "b", Count: 3}, got[1])
	})

	t.Run("empty array decodes to empty slice (not nil)", func(t *testing.T) {
		resp := makeResp(200, `[]`)
		defer resp.Body.Close()
		got, err := decodeSliceResponse[sample](resp, "list samples")
		require.NoError(t, err)
		// Distinguish empty-array vs nil so callers can range over the
		// result without a nil-check. Encoding/json fills the target
		// to an empty (non-nil) slice for `[]`.
		assert.NotNil(t, got, "empty JSON array must decode to an empty (non-nil) slice for safe ranging")
		assert.Empty(t, got)
	})

	t.Run("non-2xx returns error containing operation and status", func(t *testing.T) {
		resp := makeResp(503, `irrelevant`)
		defer resp.Body.Close()
		got, err := decodeSliceResponse[sample](resp, "list samples")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list samples")
		assert.Contains(t, err.Error(), "unexpected status")
	})

	t.Run("malformed JSON surfaces decode error with operation name", func(t *testing.T) {
		resp := makeResp(200, `[{`)
		defer resp.Body.Close()
		got, err := decodeSliceResponse[sample](resp, "list samples")
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list samples")
		assert.Contains(t, err.Error(), "decoding response")
	})
}

func TestCheckResponseStatus(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantError bool
	}{
		{name: "200 OK passes", status: 200, wantError: false},
		{name: "201 Created passes", status: 201, wantError: false},
		{name: "299 boundary still passes", status: 299, wantError: false},
		{name: "300 (Multiple Choices) is rejected — 2xx is half-open at top", status: 300, wantError: true},
		{name: "404 Not Found", status: 404, wantError: true},
		{name: "500 Server Error", status: 500, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := makeResp(tt.status, ``)
			defer resp.Body.Close()
			err := checkResponseStatus(resp, "delete resource")
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "delete resource")
				assert.Contains(t, err.Error(), "unexpected status")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
