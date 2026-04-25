// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package indexer

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// reflectValueToString is exercised indirectly through Evaluate's basic-type
// table, but the edge-case branches (invalid values, nil pointers/interfaces,
// the fmt.Sprint default arm for complex types) aren't pinned anywhere.
// Tests below pin those branches directly so a future refactor can't
// silently change how non-primitive values surface in JSONPath results.
func TestReflectValueToString_DirectEdgeCases(t *testing.T) {
	stringPtr := func(s string) *string { return &s }
	intPtr := func(n int) *int { return &n }

	t.Run("invalid value returns empty string", func(t *testing.T) {
		var v reflect.Value // zero Value is !IsValid()
		assert.Equal(t, "", reflectValueToString(v))
	})

	t.Run("nil pointer returns empty string (deref guard)", func(t *testing.T) {
		var p *string
		assert.Equal(t, "", reflectValueToString(reflect.ValueOf(p)))
	})

	t.Run("non-nil pointer dereferences and converts the underlying value", func(t *testing.T) {
		assert.Equal(t, "hello", reflectValueToString(reflect.ValueOf(stringPtr("hello"))))
		assert.Equal(t, "42", reflectValueToString(reflect.ValueOf(intPtr(42))))
	})

	t.Run("nil interface value returns empty string", func(t *testing.T) {
		var i any
		// Take a reflect.Value of a nil any — derefForFilter must handle this
		// without panicking and report empty.
		assert.Equal(t, "", reflectValueToString(reflect.ValueOf(&i).Elem()))
	})

	t.Run("negative int is formatted with sign", func(t *testing.T) {
		assert.Equal(t, "-7", reflectValueToString(reflect.ValueOf(int64(-7))))
	})

	t.Run("uint64 maximum value uses unsigned formatting", func(t *testing.T) {
		// FormatUint must be used (not FormatInt) — otherwise this would
		// overflow to a negative number.
		const maxU = uint64(1<<63 + 1)
		assert.Equal(t, "9223372036854775809", reflectValueToString(reflect.ValueOf(maxU)))
	})

	t.Run("float prints without trailing zeros (FormatFloat 'f', -1)", func(t *testing.T) {
		assert.Equal(t, "1.5", reflectValueToString(reflect.ValueOf(1.5)))
		assert.Equal(t, "-2.25", reflectValueToString(reflect.ValueOf(-2.25)))
		// Not "1.000000"
		assert.Equal(t, "1", reflectValueToString(reflect.ValueOf(1.0)))
	})

	t.Run("complex types fall through to fmt.Sprint default arm", func(t *testing.T) {
		// Slice — pinned via fmt.Sprint format ([a b c]).
		assert.Equal(t, "[a b c]", reflectValueToString(reflect.ValueOf([]string{"a", "b", "c"})))

		// Struct — pinned via fmt.Sprint format ({field-values}).
		type point struct {
			X int
			Y int
		}
		assert.Equal(t, "{1 2}", reflectValueToString(reflect.ValueOf(point{X: 1, Y: 2})))
	})
}
