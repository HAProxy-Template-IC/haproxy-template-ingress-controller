// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package httpstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseOptionsArg and parseAuthFromArg are the wrapper-package adapters
// that bridge template arguments into the pure FetchOptions / AuthConfig
// types in pkg/httpstore. The "outer shape" branches (nil arg, single
// arg, options-not-a-map, auth-not-a-map) are already covered through
// TestParseArgs_* and TestParseOptionsArg_*.
//
// What's NOT covered there is the INNER-error WRAP branch: when the map
// itself is well-formed but a nested value is invalid (e.g. delay is a
// string that doesn't parse as a duration, or a header value isn't a
// string), the adapter must:
//
//  1. Re-wrap the inner error with the "http.Fetch:" prefix so template
//     authors see WHICH template function rejected their input — without
//     this prefix the error trace is just "invalid delay: ..." with no
//     hint that it came from the http.Fetch call.
//
//  2. Use %w (NOT %v) so the inner error chain is preserved and callers
//     can errors.Is / errors.As against domain-specific sentinel errors
//     buried two layers down (e.g. a future *DurationParseError).
//
// A regression that swapped %w for %v or dropped the wrap entirely would
// pass every other test in the file — the outer error message would
// still mention "http.Fetch", but the chain to the underlying cause
// would silently break. Pin both contracts with synthetic invalid maps
// that drive the inner parse helpers into their error paths.

// invalidInnerCases drives the inner parse helpers into an error from
// VALID-shaped outer arguments. Each case is one branch of the inner
// parseFetchOptions / parseAuthConfig error space — keeping them in a
// table catches any of them silently losing the wrap if a future
// refactor changes the adapter's error handling.
var (
	invalidOptionInnerCases = []struct {
		name       string
		options    map[string]any
		wantSubstr string // unique substring from the underlying inner error
	}{
		{
			name:       "delay string is not a duration",
			options:    map[string]any{"delay": "not-a-duration"},
			wantSubstr: "invalid delay",
		},
		{
			name:       "timeout string is not a duration",
			options:    map[string]any{"timeout": "12 parsecs"},
			wantSubstr: "invalid timeout",
		},
		{
			name:       "retries is not a number",
			options:    map[string]any{"retries": "many"},
			wantSubstr: "invalid retries",
		},
		{
			name:       "critical is not a bool",
			options:    map[string]any{"critical": "yes please"},
			wantSubstr: "invalid critical",
		},
	}

	invalidAuthInnerCases = []struct {
		name       string
		auth       any
		wantSubstr string
	}{
		{
			name:       "type is not a string",
			auth:       map[string]any{"type": 42},
			wantSubstr: "invalid auth type",
		},
		{
			name:       "username is not a string",
			auth:       map[string]any{"username": 42},
			wantSubstr: "invalid username",
		},
		{
			name:       "password is not a string",
			auth:       map[string]any{"password": 42},
			wantSubstr: "invalid password",
		},
		{
			name:       "token is not a string",
			auth:       map[string]any{"token": 42},
			wantSubstr: "invalid token",
		},
		{
			name:       "headers itself is not a map",
			auth:       map[string]any{"headers": "not-a-map"},
			wantSubstr: "invalid headers",
		},
		{
			name:       "header value is not a string",
			auth:       map[string]any{"headers": map[string]any{"X-Api-Key": 42}},
			wantSubstr: "invalid header value for X-Api-Key",
		},
	}
)

func TestParseOptionsArg_WrapsInnerError(t *testing.T) {
	for _, tt := range invalidOptionInnerCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseOptionsArg([]any{"http://example.com", tt.options})

			require.Error(t, err, "an invalid inner option must produce an error")

			// (1) prefix contract: every wrapped error starts with the
			// fixed "http.Fetch:" tag so log scrapers and template
			// authors can attribute the failure to this template function.
			assert.True(t, strings.HasPrefix(err.Error(), "http.Fetch: "),
				"error must be prefixed with 'http.Fetch: ' so template authors "+
					"see which template function rejected their input; got %q",
				err.Error())

			// (2) inner-message survival: the underlying parse error's
			// message must still be visible in the wrapped string. This is
			// the only signal a template author has about WHICH option was
			// invalid.
			assert.Contains(t, err.Error(), tt.wantSubstr,
				"wrapped error must still expose the inner cause's message; "+
					"a regression that built a generic 'invalid options' message "+
					"would hide the offending field name from the user")
		})
	}
}

func TestParseAuthFromArg_WrapsInnerError(t *testing.T) {
	for _, tt := range invalidAuthInnerCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAuthFromArg(tt.auth)

			require.Error(t, err, "an invalid inner auth field must produce an error")
			assert.Nil(t, got, "on error the returned *AuthConfig must be nil — "+
				"a regression that returned a partially-populated config would let "+
				"the caller silently use credentials missing the rejected field")

			assert.True(t, strings.HasPrefix(err.Error(), "http.Fetch: "),
				"error must be prefixed with 'http.Fetch: ' so template authors "+
					"see which template function rejected their input; got %q",
				err.Error())

			assert.Contains(t, err.Error(), tt.wantSubstr,
				"wrapped error must still expose the inner cause's message")
		})
	}
}

// TestParseOptionsAndAuth_PreserveErrorChain pins the %w (NOT %v) wrap.
// This is the contract that lets callers do errors.Is(err, sentinel) or
// errors.As(err, &targetErr) against a domain-specific error stashed
// inside the parse helper — a regression to %v would break those checks
// silently because the formatted string would still look correct.
func TestParseOptionsAndAuth_PreserveErrorChain(t *testing.T) {
	t.Run("parseOptionsArg preserves Unwrap chain", func(t *testing.T) {
		_, err := parseOptionsArg([]any{"http://example.com", map[string]any{
			"delay": "not-a-duration",
		}})
		require.Error(t, err)

		// errors.Unwrap must reach the inner parse error. If a regression
		// switched to %v the chain would terminate at the outer wrap.
		inner := errors.Unwrap(err)
		require.NotNil(t, inner,
			"parseOptionsArg must wrap with %%w so errors.Unwrap reaches the "+
				"inner parse error; a switch to %%v would silently break "+
				"errors.Is/errors.As checks against future sentinel errors")
		assert.Contains(t, inner.Error(), "invalid delay",
			"the unwrapped inner error must carry the original parse failure context")
	})

	t.Run("parseAuthFromArg preserves Unwrap chain", func(t *testing.T) {
		_, err := parseAuthFromArg(map[string]any{
			"headers": map[string]any{"X-Api-Key": 42},
		})
		require.Error(t, err)

		inner := errors.Unwrap(err)
		require.NotNil(t, inner,
			"parseAuthFromArg must wrap with %%w so errors.Unwrap reaches the "+
				"inner parse error")
		assert.Contains(t, inner.Error(), "X-Api-Key")
	})
}
