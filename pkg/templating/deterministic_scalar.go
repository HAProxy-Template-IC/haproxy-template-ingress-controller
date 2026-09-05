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
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

type deterministicScalarKind uint8

const (
	deterministicNilScalar deterministicScalarKind = iota
	deterministicBoolScalar
	deterministicSignedScalar
	deterministicUnsignedScalar
	deterministicFloatScalar
	deterministicStringScalar
)

type deterministicScalar struct {
	kind     deterministicScalarKind
	text     string
	boolean  bool
	signed   int64
	unsigned uint64
	floating float64
}

type deterministicScalarKey struct {
	kind     deterministicScalarKind
	boolean  bool
	signed   int64
	unsigned uint64
	float    uint64
	text     string
}

func deterministicScalarOf(value any) (deterministicScalar, error) {
	if value == nil {
		return deterministicScalar{kind: deterministicNilScalar}, nil
	}
	return deterministicScalarOfValue(reflect.ValueOf(value))
}

func deterministicScalarOfValue(rv reflect.Value) (deterministicScalar, error) {
	for rv.IsValid() && rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return deterministicScalar{kind: deterministicNilScalar}, nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return deterministicScalar{kind: deterministicNilScalar}, nil
	}
	if rv.Kind() == reflect.Pointer {
		if !deterministicPointerScalarKind(rv.Type().Elem().Kind()) {
			return deterministicScalar{}, deterministicScalarValueTypeError(rv)
		}
		if rv.IsNil() {
			return deterministicScalar{kind: deterministicNilScalar}, nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.String:
		text := rv.String()
		return deterministicScalar{kind: deterministicStringScalar, text: text}, nil
	case reflect.Bool:
		value := rv.Bool()
		return deterministicScalar{
			kind:    deterministicBoolScalar,
			text:    strconv.FormatBool(value),
			boolean: value,
		}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value := rv.Int()
		return deterministicScalar{
			kind:   deterministicSignedScalar,
			text:   strconv.FormatInt(value, 10),
			signed: value,
		}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value := rv.Uint()
		return deterministicScalar{
			kind:     deterministicUnsignedScalar,
			text:     strconv.FormatUint(value, 10),
			unsigned: value,
		}, nil
	case reflect.Float32, reflect.Float64:
		value := rv.Float()
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return deterministicScalar{}, fmt.Errorf("non-finite %s has no deterministic scalar representation", rv.Kind())
		}
		if value == 0 {
			value = 0
		}
		return deterministicScalar{
			kind:     deterministicFloatScalar,
			text:     strconv.FormatFloat(value, 'f', -1, rv.Type().Bits()),
			floating: value,
		}, nil
	default:
		return deterministicScalar{}, deterministicScalarValueTypeError(rv)
	}
}

func deterministicPointerScalarKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func deterministicScalarValueTypeError(value reflect.Value) error {
	return fmt.Errorf("value of type %s has no deterministic scalar representation", value.Type())
}

func mustDeterministicScalarText(function string, value any) string {
	scalar := mustDeterministicScalar(function, value)
	return scalar.text
}

func mustDeterministicScalar(function string, value any) deterministicScalar {
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		panic(fmt.Errorf("%s: %w", function, err))
	}
	return scalar
}

func (s deterministicScalar) key() deterministicScalarKey {
	key := deterministicScalarKey{kind: s.kind}
	switch s.kind {
	case deterministicBoolScalar:
		key.boolean = s.boolean
	case deterministicSignedScalar:
		key.signed = s.signed
	case deterministicUnsignedScalar:
		key.unsigned = s.unsigned
	case deterministicFloatScalar:
		key.float = math.Float64bits(s.floating)
	case deterministicStringScalar:
		key.text = s.text
	}
	return key
}

func compareDeterministicScalars(left, right deterministicScalar) int {
	if left.kind == deterministicNilScalar || right.kind == deterministicNilScalar {
		if left.kind == right.kind {
			return 0
		}
		if left.kind == deterministicNilScalar {
			return 1
		}
		return -1
	}

	if deterministicNumericScalar(left.kind) && deterministicNumericScalar(right.kind) {
		if compared := compareDeterministicNumbers(left, right); compared != 0 {
			return compared
		}
		return compareDeterministicScalarKinds(left.kind, right.kind)
	}
	if left.kind != right.kind {
		return compareDeterministicScalarKinds(left.kind, right.kind)
	}

	switch left.kind {
	case deterministicBoolScalar:
		switch {
		case left.boolean == right.boolean:
			return 0
		case !left.boolean:
			return -1
		default:
			return 1
		}
	case deterministicStringScalar:
		return strings.Compare(left.text, right.text)
	default:
		return 0
	}
}

func deterministicNumericScalar(kind deterministicScalarKind) bool {
	return kind == deterministicSignedScalar || kind == deterministicUnsignedScalar || kind == deterministicFloatScalar
}

func compareDeterministicScalarKinds(left, right deterministicScalarKind) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareDeterministicNumbers(left, right deterministicScalar) int {
	switch {
	case left.kind == deterministicSignedScalar && right.kind == deterministicSignedScalar:
		return compareInt64(left.signed, right.signed)
	case left.kind == deterministicUnsignedScalar && right.kind == deterministicUnsignedScalar:
		return compareUint64(left.unsigned, right.unsigned)
	case left.kind == deterministicFloatScalar && right.kind == deterministicFloatScalar:
		return compareFloat64(left.floating, right.floating)
	case left.kind == deterministicSignedScalar && right.kind == deterministicUnsignedScalar:
		return compareSignedUnsigned(left.signed, right.unsigned)
	case left.kind == deterministicUnsignedScalar && right.kind == deterministicSignedScalar:
		return -compareSignedUnsigned(right.signed, left.unsigned)
	default:
		return deterministicNumericRat(left).Cmp(deterministicNumericRat(right))
	}
}

func compareInt64(left, right int64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareUint64(left, right uint64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareFloat64(left, right float64) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func compareSignedUnsigned(signed int64, unsigned uint64) int {
	if signed < 0 {
		return -1
	}
	return compareUint64(uint64(signed), unsigned)
}

func deterministicNumericRat(value deterministicScalar) *big.Rat {
	switch value.kind {
	case deterministicSignedScalar:
		return new(big.Rat).SetInt64(value.signed)
	case deterministicUnsignedScalar:
		return new(big.Rat).SetInt(new(big.Int).SetUint64(value.unsigned))
	case deterministicFloatScalar:
		return new(big.Rat).SetFloat64(value.floating)
	default:
		panic("templating: non-numeric deterministic scalar")
	}
}

func rememberDeterministicDisplay(
	seen map[string]deterministicScalarKey,
	scalar deterministicScalar,
) error {
	key := scalar.key()
	if previous, ok := seen[scalar.text]; ok && previous != key {
		return fmt.Errorf("distinct scalar keys both render as %q", scalar.text)
	}
	seen[scalar.text] = key
	return nil
}
