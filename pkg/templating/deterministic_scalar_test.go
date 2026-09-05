// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"math"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type deterministicNamedString string
type deterministicNamedInt int32

func deterministicTestPointer[T any](value T) *T {
	return &value
}

func TestDeterministicScalarAcceptsValueScalarsAndPointers(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nil", value: nil, want: ""},
		{name: "string", value: "value", want: "value"},
		{name: "named string", value: deterministicNamedString("value"), want: "value"},
		{name: "bool", value: true, want: "true"},
		{name: "int", value: int(-1), want: "-1"},
		{name: "int8", value: int8(-2), want: "-2"},
		{name: "int16", value: int16(-3), want: "-3"},
		{name: "int32", value: int32(-4), want: "-4"},
		{name: "int64", value: int64(-5), want: "-5"},
		{name: "named int", value: deterministicNamedInt(-6), want: "-6"},
		{name: "uint", value: uint(1), want: "1"},
		{name: "uint8", value: uint8(2), want: "2"},
		{name: "uint16", value: uint16(3), want: "3"},
		{name: "uint32", value: uint32(4), want: "4"},
		{name: "uint64", value: uint64(5), want: "5"},
		{name: "float32", value: float32(1.5), want: "1.5"},
		{name: "float64", value: 2.5, want: "2.5"},
		{name: "string pointer", value: deterministicTestPointer("value"), want: "value"},
		{name: "bool pointer", value: deterministicTestPointer(true), want: "true"},
		{name: "int pointer", value: deterministicTestPointer(int(-1)), want: "-1"},
		{name: "int8 pointer", value: deterministicTestPointer(int8(-2)), want: "-2"},
		{name: "int16 pointer", value: deterministicTestPointer(int16(-3)), want: "-3"},
		{name: "int32 pointer", value: deterministicTestPointer(int32(-4)), want: "-4"},
		{name: "int64 pointer", value: deterministicTestPointer(int64(-5)), want: "-5"},
		{name: "uint pointer", value: deterministicTestPointer(uint(1)), want: "1"},
		{name: "uint8 pointer", value: deterministicTestPointer(uint8(2)), want: "2"},
		{name: "uint16 pointer", value: deterministicTestPointer(uint16(3)), want: "3"},
		{name: "uint32 pointer", value: deterministicTestPointer(uint32(4)), want: "4"},
		{name: "uint64 pointer", value: deterministicTestPointer(uint64(5)), want: "5"},
		{name: "float32 pointer", value: deterministicTestPointer(float32(1.5)), want: "1.5"},
		{name: "float64 pointer", value: deterministicTestPointer(2.5), want: "2.5"},
		{name: "nil scalar pointer", value: (*int64)(nil), want: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scalar, err := deterministicScalarOf(test.value)
			require.NoError(t, err)
			assert.Equal(t, test.want, scalar.text)
		})
	}
}

func TestDeterministicScalarRejectsIdentityAndNonFiniteValues(t *testing.T) {
	var unsafePointer unsafe.Pointer
	var function func()
	var channel chan int
	integer := 1
	integerPointer := &integer
	tests := []struct {
		name  string
		value any
	}{
		{name: "struct", value: struct{ Value string }{Value: "value"}},
		{name: "struct pointer", value: &struct{ Value string }{Value: "value"}},
		{name: "nil struct pointer", value: (*struct{})(nil)},
		{name: "pointer to pointer", value: &integerPointer},
		{name: "map", value: map[string]any{"value": 1}},
		{name: "slice", value: []int{1}},
		{name: "function", value: function},
		{name: "channel", value: channel},
		{name: "unsafe pointer", value: unsafePointer},
		{name: "uintptr", value: uintptr(1)},
		{name: "complex", value: complex(1, 2)},
		{name: "NaN", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := deterministicScalarOf(test.value)
			require.Error(t, err)
		})
	}
}

func TestDeterministicScalarKeysIgnoreAllocationIdentity(t *testing.T) {
	left := deterministicTestPointer(int64(42))
	right := deterministicTestPointer(int64(42))
	require.NotSame(t, left, right)

	leftScalar, err := deterministicScalarOf(left)
	require.NoError(t, err)
	rightScalar, err := deterministicScalarOf(right)
	require.NoError(t, err)

	assert.Equal(t, leftScalar.key(), rightScalar.key())
}

func TestDeterministicScalarComparisonDoesNotLoseIntegerPrecision(t *testing.T) {
	floatScalar, err := deterministicScalarOf(float64(1 << 53))
	require.NoError(t, err)
	uintScalar, err := deterministicScalarOf(uint64(1<<53) + 1)
	require.NoError(t, err)
	assert.Negative(t, compareDeterministicScalars(floatScalar, uintScalar))

	maxSigned, err := deterministicScalarOf(int64(math.MaxInt64))
	require.NoError(t, err)
	maxUnsigned, err := deterministicScalarOf(uint64(math.MaxUint64))
	require.NoError(t, err)
	assert.Negative(t, compareDeterministicScalars(maxSigned, maxUnsigned))
}
