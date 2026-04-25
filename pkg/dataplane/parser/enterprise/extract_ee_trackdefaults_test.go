// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseTrackDefaults parses the "track-defaults" botmgmt directive,
// which has the shape: `track-defaults <key1> <val1> [<key2> <val2>]...`
//
// The function recognises three keys (size, expire, period), pairs
// up consecutive tokens as key/value pairs, and only returns a
// non-nil result when at least one recognised key was present.
//
// Pinning the contracts:
//   - empty input yields nil (no fields to populate)
//   - only-unrecognised keys yields nil (hasValues stays false)
//   - each recognised key populates its own field
//   - values flow through parseInt: invalid strings produce nil
//     pointers (not 0)
//   - odd-length inputs drop the orphan trailing token (the loop
//     condition is `i < len(values)-1`)
//   - unrecognised keys are silently skipped without breaking the
//     pair iteration
func TestParseTrackDefaults(t *testing.T) {
	t.Run("empty input returns nil", func(t *testing.T) {
		assert.Nil(t, parseTrackDefaults(nil))
		assert.Nil(t, parseTrackDefaults([]string{}))
	})

	t.Run("only unrecognised keys returns nil", func(t *testing.T) {
		// Two unknown keys, nothing matches the size/expire/period
		// switch — hasValues stays false, function returns nil.
		assert.Nil(t, parseTrackDefaults([]string{"unknown", "1", "other", "2"}))
	})

	t.Run("size only", func(t *testing.T) {
		got := parseTrackDefaults([]string{"size", "1024"})
		require.NotNil(t, got)
		require.NotNil(t, got.Size)
		assert.Equal(t, 1024, *got.Size)
		assert.Nil(t, got.Expire, "Expire must remain nil when not in input")
		assert.Nil(t, got.Period, "Period must remain nil when not in input")
	})

	t.Run("expire only", func(t *testing.T) {
		got := parseTrackDefaults([]string{"expire", "60"})
		require.NotNil(t, got)
		require.NotNil(t, got.Expire)
		assert.Equal(t, 60, *got.Expire)
		assert.Nil(t, got.Size)
		assert.Nil(t, got.Period)
	})

	t.Run("period only", func(t *testing.T) {
		got := parseTrackDefaults([]string{"period", "30"})
		require.NotNil(t, got)
		require.NotNil(t, got.Period)
		assert.Equal(t, 30, *got.Period)
		assert.Nil(t, got.Size)
		assert.Nil(t, got.Expire)
	})

	t.Run("all three keys populate independently", func(t *testing.T) {
		got := parseTrackDefaults([]string{"size", "1024", "expire", "60", "period", "30"})
		require.NotNil(t, got)
		require.NotNil(t, got.Size)
		require.NotNil(t, got.Expire)
		require.NotNil(t, got.Period)
		assert.Equal(t, 1024, *got.Size)
		assert.Equal(t, 60, *got.Expire)
		assert.Equal(t, 30, *got.Period)
	})

	t.Run("invalid value flows through parseInt to nil pointer (NOT 0)", func(t *testing.T) {
		// parseInt returns nil for non-numeric strings — pin that the
		// nil flows through to the field rather than silently being
		// coerced to 0.
		got := parseTrackDefaults([]string{"size", "not-a-number"})
		require.NotNil(t, got, "key was recognised, hasValues must flip to true")
		assert.Nil(t, got.Size, "invalid value must produce nil pointer, not 0")
	})

	t.Run("odd-length input drops orphan trailing token", func(t *testing.T) {
		// Loop condition `i < len(values)-1` ensures the trailing
		// "unpaired" token is not read past the slice bound. Pin that
		// the pairs before it still parse correctly.
		got := parseTrackDefaults([]string{"size", "1024", "orphan"})
		require.NotNil(t, got)
		require.NotNil(t, got.Size)
		assert.Equal(t, 1024, *got.Size)
		assert.Nil(t, got.Expire)
		assert.Nil(t, got.Period)
	})

	t.Run("unrecognised keys interleaved with recognised ones", func(t *testing.T) {
		got := parseTrackDefaults([]string{"foo", "bar", "size", "1024", "baz", "qux", "expire", "60"})
		require.NotNil(t, got)
		require.NotNil(t, got.Size)
		require.NotNil(t, got.Expire)
		assert.Equal(t, 1024, *got.Size)
		assert.Equal(t, 60, *got.Expire)
		assert.Nil(t, got.Period)
	})

	t.Run("single token (no value) yields nil", func(t *testing.T) {
		// With one token, the loop's `i < len(values)-1` is i<0 → false,
		// so no pairs are iterated, hasValues stays false.
		assert.Nil(t, parseTrackDefaults([]string{"size"}))
	})
}
