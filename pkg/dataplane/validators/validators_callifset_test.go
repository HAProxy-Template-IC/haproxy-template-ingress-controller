// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package validators

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// callIfSet is the tiny generic that every ValidatorSet method
// (~22 of them) uses to gracefully tolerate a not-yet-initialized
// version's missing validator function. The contract is:
//
//   - fn != nil  → call fn(m) and return its result
//   - fn == nil  → return the zero value of R (no panic)
//
// The nil-tolerance is load-bearing: HAProxy DataPlane API versions
// have different validator surface areas (v3.0 has no waf_global,
// v3.1 added log_profiles, etc.). When a ValidatorSet is built for
// an older version, validator-function fields for features-not-yet-
// supported are left nil. Every ValidateXxx method calls callIfSet,
// so a regression that panicked on nil fn (for instance, dropping
// the nil-guard in a refactor) would crash on every validation
// against a version that lacks the corresponding feature.
//
// The "return zero value" semantics is also load-bearing for the
// error case: ValidateServer returns nil (no error) when there is
// no validator, which means "no validation performed, treated as
// valid". Returning a non-zero default would silently fail every
// validation against an older version.

func TestCallIfSet_NonNilFunction_IsCalledAndReturnsResult(t *testing.T) {
	var sentinel = errors.New("validator-fired")

	called := false
	fn := func(s string) error {
		called = true
		assert.Equal(t, "input-x", s, "fn must receive the supplied argument verbatim")
		return sentinel
	}

	got := callIfSet(fn, "input-x")

	require.True(t, called, "non-nil fn must be invoked")
	assert.Same(t, sentinel, got,
		"callIfSet must propagate fn's return value verbatim — "+
			"a refactor that wrapped or transformed the error would silently change validation diagnostics")
}

func TestCallIfSet_NilFunction_ReturnsZeroValue(t *testing.T) {
	// The load-bearing branch. Pin both the no-panic guarantee and
	// the zero-value semantics for the error type used by every
	// ValidateXxx method.
	var fn func(string) error // explicitly nil

	require.NotPanics(t, func() {
		got := callIfSet(fn, "input-x")
		assert.Nil(t, got,
			"nil fn must return the zero value of error (which is nil); "+
				"every ValidateXxx method depends on this to mean 'no validator -> no error -> valid'")
	})
}

func TestCallIfSet_NilFunction_GenericReturnTypes(t *testing.T) {
	// callIfSet is generic in both T (input) and R (result). Pin
	// that the zero-value behavior works for non-error R types too.
	// A regression that hardcoded R = error would compile but blow
	// up if a future ValidatorSet method were retrofitted to return
	// a different type (e.g. a hash).

	t.Run("string return", func(t *testing.T) {
		var fn func(int) string
		got := callIfSet(fn, 42)
		assert.Equal(t, "", got,
			"zero-value of string is the empty string")
	})

	t.Run("int return", func(t *testing.T) {
		var fn func(string) int
		got := callIfSet(fn, "x")
		assert.Equal(t, 0, got, "zero-value of int is 0")
	})

	t.Run("pointer return", func(t *testing.T) {
		var fn func(string) *int
		got := callIfSet(fn, "x")
		assert.Nil(t, got, "zero-value of pointer types is nil")
	})

	t.Run("slice return", func(t *testing.T) {
		var fn func(string) []byte
		got := callIfSet(fn, "x")
		assert.Nil(t, got, "zero-value of slice types is nil (not a non-nil empty slice)")
	})
}
