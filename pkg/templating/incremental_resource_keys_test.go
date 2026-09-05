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
	"reflect"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type incrementalResourceKeyStringer string

func (incrementalResourceKeyStringer) String() string {
	panic("resource key String method must not be called")
}

type incrementalResourceKeyPointerStringer string

func (*incrementalResourceKeyPointerStringer) String() string {
	panic("resource key String method must not be called")
}

type incrementalResourceKeyMarshaler string

func (incrementalResourceKeyMarshaler) MarshalJSON() ([]byte, error) {
	panic("resource key MarshalJSON method must not be called")
}

type incrementalResourceKeyTextMarshaler string

func (incrementalResourceKeyTextMarshaler) MarshalText() ([]byte, error) {
	panic("resource key MarshalText method must not be called")
}

type incrementalResourceKeyYAMLMarshaler string

func (incrementalResourceKeyYAMLMarshaler) MarshalYAML() (any, error) {
	panic("resource key MarshalYAML method must not be called")
}

type incrementalResourceKeyPointerMarshaler string

func (*incrementalResourceKeyPointerMarshaler) MarshalText() ([]byte, error) {
	panic("resource key MarshalText method must not be called")
}

func TestCanonicalIncrementalResourceKeysUsesScalarValues(t *testing.T) {
	keys, err := CanonicalIncrementalResourceKeys(
		nil,
		"value",
		true,
		int8(-8),
		int16(-16),
		int32(-32),
		int64(-64),
		uint(1),
		uint8(8),
		uint16(16),
		uint32(32),
		uint64(64),
		float32(1.25),
		float64(2.5),
	)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"", "value", "true", "-8", "-16", "-32", "-64",
		"1", "8", "16", "32", "64", "1.25", "2.5",
	}, keys)
}

func TestCanonicalIncrementalResourceKeyMatchesSliceNormalization(t *testing.T) {
	values := []any{nil, "value", true, int64(-64), uint64(64), float64(2.5)}
	want, err := CanonicalIncrementalResourceKeys(values...)
	require.NoError(t, err)
	got := make([]string, len(values))
	for index, value := range values {
		got[index], err = CanonicalIncrementalResourceKey(index, value)
		require.NoError(t, err)
	}
	assert.Equal(t, want, got)
	_, err = CanonicalIncrementalResourceKey(-1, "value")
	require.ErrorContains(t, err, "index -1 is invalid")
}

func TestCanonicalIncrementalResourceValueMatchesInterfaceCanonicalization(t *testing.T) {
	type namedString string
	type namedBool bool
	type namedInt int
	type namedInt8 int8
	type namedInt16 int16
	type namedInt32 int32
	type namedInt64 int64
	type namedUint uint
	type namedUint8 uint8
	type namedUint16 uint16
	type namedUint32 uint32
	type namedUint64 uint64
	type namedFloat32 float32
	type namedFloat64 float64

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nil", value: nil, want: ""},
		{name: "string", value: "value", want: "value"},
		{name: "named string", value: namedString("named"), want: "named"},
		{name: "bool", value: true, want: "true"},
		{name: "named bool", value: namedBool(false), want: "false"},
		{name: "int", value: int(-1), want: "-1"},
		{name: "named int", value: namedInt(-2), want: "-2"},
		{name: "int8", value: int8(-8), want: "-8"},
		{name: "named int8", value: namedInt8(-9), want: "-9"},
		{name: "int16", value: int16(-16), want: "-16"},
		{name: "named int16", value: namedInt16(-17), want: "-17"},
		{name: "int32", value: int32(-32), want: "-32"},
		{name: "named int32", value: namedInt32(-33), want: "-33"},
		{name: "int64", value: int64(-64), want: "-64"},
		{name: "named int64", value: namedInt64(-65), want: "-65"},
		{name: "uint", value: uint(1), want: "1"},
		{name: "named uint", value: namedUint(2), want: "2"},
		{name: "uint8", value: uint8(8), want: "8"},
		{name: "named uint8", value: namedUint8(9), want: "9"},
		{name: "uint16", value: uint16(16), want: "16"},
		{name: "named uint16", value: namedUint16(17), want: "17"},
		{name: "uint32", value: uint32(32), want: "32"},
		{name: "named uint32", value: namedUint32(33), want: "33"},
		{name: "uint64", value: uint64(64), want: "64"},
		{name: "named uint64", value: namedUint64(65), want: "65"},
		{name: "float32", value: float32(1.25), want: "1.25"},
		{name: "named float32", value: namedFloat32(2.5), want: "2.5"},
		{name: "float64", value: float64(3.75), want: "3.75"},
		{name: "named float64", value: namedFloat64(4.5), want: "4.5"},
		{name: "negative zero", value: math.Copysign(0, -1), want: "0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fromInterface, err := CanonicalIncrementalResourceKey(7, test.value)
			require.NoError(t, err)
			assert.Equal(t, test.want, fromInterface)

			direct := reflect.Value{}
			if test.value != nil {
				direct = reflect.ValueOf(test.value)
			}
			fromValue, err := CanonicalIncrementalResourceValue(7, direct)
			require.NoError(t, err)
			assert.Equal(t, fromInterface, fromValue)

			wrapped := test.value
			fromWrappedValue, err := CanonicalIncrementalResourceValue(7, reflect.ValueOf(&wrapped).Elem())
			require.NoError(t, err)
			assert.Equal(t, fromInterface, fromWrappedValue)
		})
	}
}

func TestCanonicalIncrementalResourceValueMatchesRejectedInterfaceValues(t *testing.T) {
	type namedFloat32 float32
	type namedFloat64 float64

	scalar := int64(7)
	var nilScalar *int64
	var unsafePointer unsafe.Pointer
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "scalar pointer", value: &scalar, want: "pointer type *int64 is unavailable"},
		{name: "typed nil scalar pointer", value: nilScalar, want: "pointer type *int64 is unavailable"},
		{name: "value Stringer", value: incrementalResourceKeyStringer("value"), want: "implements fmt.Stringer"},
		{name: "pointer Stringer", value: incrementalResourceKeyPointerStringer("value"), want: "implements fmt.Stringer"},
		{name: "JSON marshaler", value: incrementalResourceKeyMarshaler("value"), want: "uses a custom marshaler"},
		{name: "text marshaler", value: incrementalResourceKeyTextMarshaler("value"), want: "uses a custom marshaler"},
		{name: "YAML marshaler", value: incrementalResourceKeyYAMLMarshaler("value"), want: "uses a custom marshaler"},
		{name: "pointer marshaler", value: incrementalResourceKeyPointerMarshaler("value"), want: "uses a custom marshaler"},
		{name: "array", value: [1]string{"value"}, want: "no deterministic scalar representation"},
		{name: "channel", value: make(chan int), want: "no deterministic scalar representation"},
		{name: "complex64", value: complex64(1 + 2i), want: "no deterministic scalar representation"},
		{name: "complex128", value: complex128(3 + 4i), want: "no deterministic scalar representation"},
		{name: "function", value: func() {}, want: "no deterministic scalar representation"},
		{name: "map", value: map[string]string{"key": "value"}, want: "no deterministic scalar representation"},
		{name: "slice", value: []string{"value"}, want: "no deterministic scalar representation"},
		{name: "struct", value: struct{}{}, want: "no deterministic scalar representation"},
		{name: "uintptr", value: uintptr(1), want: "no deterministic scalar representation"},
		{name: "unsafe pointer", value: unsafePointer, want: "no deterministic scalar representation"},
		{name: "float32 positive infinity", value: float32(math.Inf(1)), want: "non-finite float32"},
		{name: "float32 negative infinity", value: float32(math.Inf(-1)), want: "non-finite float32"},
		{name: "float32 NaN", value: float32(math.NaN()), want: "non-finite float32"},
		{name: "named float32 positive infinity", value: namedFloat32(math.Inf(1)), want: "non-finite float32"},
		{name: "named float32 negative infinity", value: namedFloat32(math.Inf(-1)), want: "non-finite float32"},
		{name: "named float32 NaN", value: namedFloat32(math.NaN()), want: "non-finite float32"},
		{name: "float64 positive infinity", value: math.Inf(1), want: "non-finite float64"},
		{name: "float64 negative infinity", value: math.Inf(-1), want: "non-finite float64"},
		{name: "float64 NaN", value: math.NaN(), want: "non-finite float64"},
		{name: "named float64 positive infinity", value: namedFloat64(math.Inf(1)), want: "non-finite float64"},
		{name: "named float64 negative infinity", value: namedFloat64(math.Inf(-1)), want: "non-finite float64"},
		{name: "named float64 NaN", value: namedFloat64(math.NaN()), want: "non-finite float64"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fromInterface, interfaceErr := CanonicalIncrementalResourceKey(11, test.value)
			require.ErrorContains(t, interfaceErr, test.want)

			fromValue, valueErr := CanonicalIncrementalResourceValue(11, reflect.ValueOf(test.value))
			assert.Equal(t, fromInterface, fromValue)
			require.EqualError(t, valueErr, interfaceErr.Error())

			wrapped := test.value
			fromWrappedValue, wrappedErr := CanonicalIncrementalResourceValue(11, reflect.ValueOf(&wrapped).Elem())
			assert.Equal(t, fromInterface, fromWrappedValue)
			require.EqualError(t, wrappedErr, interfaceErr.Error())
		})
	}
}

func TestCanonicalIncrementalResourceValueMatchesInvalidIndex(t *testing.T) {
	_, interfaceErr := CanonicalIncrementalResourceKey(-1, "value")
	require.Error(t, interfaceErr)
	_, valueErr := CanonicalIncrementalResourceValue(-1, reflect.ValueOf("value"))
	require.EqualError(t, valueErr, interfaceErr.Error())
}

func TestCanonicalIncrementalResourceKeysRejectsUnsafeValuesWithoutCallingMethods(t *testing.T) {
	calls := 0
	stringer := incrementalHTTPStringer{calls: &calls}
	marshaler := incrementalNativeCustomMarshaler{calls: &calls}
	scalar := int64(7)
	var unsafePointer unsafe.Pointer
	tests := map[string]struct {
		value any
		want  string
	}{
		"Stringer":       {value: stringer, want: "fmt.Stringer"},
		"custom marshal": {value: marshaler, want: "custom marshaler"},
		"scalar pointer": {value: &scalar, want: "pointer type"},
		"structured":     {value: struct{}{}, want: "no deterministic scalar representation"},
		"function":       {value: func() {}, want: "no deterministic scalar representation"},
		"channel":        {value: make(chan int), want: "no deterministic scalar representation"},
		"unsafe pointer": {value: unsafePointer, want: "no deterministic scalar representation"},
		"NaN":            {value: math.NaN(), want: "non-finite float"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := CanonicalIncrementalResourceKeys(test.value)
			require.ErrorContains(t, err, test.want)
		})
	}
	assert.Zero(t, calls)
}
