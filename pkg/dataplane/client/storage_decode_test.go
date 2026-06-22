// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two decode helpers in storage_helpers.go (decodeStorageNameList,
// decodeStorageNameListWithFallback) parse the response
// of every GetAll* storage call in the package — maps, SSL certificates,
// general files, log profiles, etc. They share the same basic shape but
// differ on which JSON field they pull (storage_name vs both),
// and decodeStorageNameListWithFallback adds a fallback to "id" when
// "storage_name" is absent (a real API quirk for general files).
//
// Two contracts are load-bearing here that the existing per-storage
// integration tests don't pin in isolation:
//
//  1. Status-code gating: anything other than 200 must return an error
//     containing the resource type and the actual status code so the
//     operator can grep for it. A regression that swallowed e.g. 401
//     would silently return an empty list and look like "no maps exist"
//     to every downstream caller.
//
//  2. Nil-pointer skip vs. inclusion: items with a nil identifier field
//     must be silently SKIPPED (some HAProxy API responses include
//     orphan entries with no name). A regression that included a "" for
//     each nil would corrupt every downstream comparator with phantom
//     resources named "". Equally, a regression that returned an error
//     on the first nil would break every storage listing the moment
//     the API returned even one orphan.
//
// For decodeStorageNameListWithFallback, the IDENTITY OF THE FALLBACK
// also matters: storage_name MUST be preferred over id when both are
// present, otherwise a downstream lookup keyed on storage_name would
// silently fail.
//
// Response objects are constructed inline (matching the existing
// pattern in storage_helpers_test.go) rather than via a helper that
// returns *http.Response — bodyclose flags any function returning
// *http.Response, even when the body is an io.NopCloser that needs
// no actual closing.

func TestDecodeStorageNameList(t *testing.T) {
	t.Run("non-200 status produces error containing resource type and code", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(strings.NewReader(`[]`)),
		}
		_, err := decodeStorageNameList(resp, "maps")
		require.Error(t, err, "non-200 status must surface as an error")
		// Both pieces of context must be in the error so an operator can
		// grep for "maps" in logs and immediately see the status code.
		assert.Contains(t, err.Error(), "maps")
		assert.Contains(t, err.Error(), "401")
	})

	t.Run("malformed JSON produces decoding error with resource type", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`not-valid-json`)),
		}
		_, err := decodeStorageNameList(resp, "maps")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maps",
			"decode errors must still tag the resource type so operator "+
				"sees which storage type is misbehaving")
	})

	t.Run("empty array yields empty slice (NOT nil)", func(t *testing.T) {
		// The make() call promises a non-nil empty slice. Some downstream
		// code paths distinguish nil-from-error vs. empty-from-success.
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`[]`)),
		}
		got, err := decodeStorageNameList(resp, "maps")
		require.NoError(t, err)
		assert.NotNil(t, got, "empty success must return non-nil empty slice")
		assert.Empty(t, got)
	})

	t.Run("items with nil storage_name are silently skipped", func(t *testing.T) {
		// Real-world: HAProxy API has been observed returning entries
		// with "storage_name": null for orphan files. Including those as
		// "" would poison every comparator that keys by name.
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`[
				{"storage_name": "host.map"},
				{"storage_name": null},
				{"storage_name": "path.map"}
			]`)),
		}
		got, err := decodeStorageNameList(resp, "maps")
		require.NoError(t, err)
		assert.Equal(t, []string{"host.map", "path.map"}, got,
			"nil storage_name entries must be silently dropped, not "+
				"included as \"\"; including \"\" would create phantom "+
				"resources in every downstream comparator")
	})

	t.Run("order is preserved as the API returned it", func(t *testing.T) {
		// Some downstream consumers iterate this list and rely on stable
		// order. A regression that sorted (or randomized) here would
		// surface as flaky tests in those consumers.
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`[
				{"storage_name": "z.map"},
				{"storage_name": "a.map"},
				{"storage_name": "m.map"}
			]`)),
		}
		got, err := decodeStorageNameList(resp, "maps")
		require.NoError(t, err)
		assert.Equal(t, []string{"z.map", "a.map", "m.map"}, got)
	})
}

func TestDecodeStorageNameListWithFallback(t *testing.T) {
	// This variant exists specifically because the general-files API
	// sometimes populates "id" instead of "storage_name". The fallback
	// is load-bearing — without it, every general-file listing returns
	// an empty slice and downstream sync silently misses every file.

	t.Run("non-200 status produces error", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader(`[]`)),
		}
		_, err := decodeStorageNameListWithFallback(resp, "general-files")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "general-files")
		assert.Contains(t, err.Error(), "503")
	})

	t.Run("storage_name is preferred when both fields are present", func(t *testing.T) {
		// Critical: if the API returns BOTH "storage_name" and "id" with
		// different values, downstream lookups key on storage_name. A
		// regression that picked id first would silently break those
		// lookups even when both values look reasonable in a log.
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`[{"storage_name": "preferred-name", "id": "should-be-ignored"}]`)),
		}
		got, err := decodeStorageNameListWithFallback(resp, "general-files")
		require.NoError(t, err)
		assert.Equal(t, []string{"preferred-name"}, got,
			"when both storage_name and id are present, storage_name must win — "+
				"otherwise a downstream lookup keyed on storage_name silently "+
				"misses the resource")
	})

	t.Run("id is used when storage_name is absent", func(t *testing.T) {
		// The fallback case — without this branch, general-files listing
		// returns empty and every general-file goes silently unsynced.
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`[{"id": "fallback-name"}]`)),
		}
		got, err := decodeStorageNameListWithFallback(resp, "general-files")
		require.NoError(t, err)
		assert.Equal(t, []string{"fallback-name"}, got,
			"when storage_name is absent, id must be used as fallback — "+
				"this is the documented API quirk for general files; without "+
				"the fallback every general-file listing returns empty")
	})

	t.Run("items with both fields nil are silently skipped", func(t *testing.T) {
		// Defensive: the API has been observed to return entries with
		// both fields as null. Including them would inject "" into the
		// listing and break downstream comparators.
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`[
				{"storage_name": "real-file"},
				{"storage_name": null, "id": null},
				{"id": "via-fallback"}
			]`)),
		}
		got, err := decodeStorageNameListWithFallback(resp, "general-files")
		require.NoError(t, err)
		assert.Equal(t, []string{"real-file", "via-fallback"}, got,
			"items with BOTH fields nil must be silently skipped; a regression "+
				"that included \"\" would corrupt every downstream comparator")
	})

	t.Run("malformed JSON produces decoding error with resource type", func(t *testing.T) {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`not-an-array`)),
		}
		_, err := decodeStorageNameListWithFallback(resp, "general-files")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "general-files")
	})
}
