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
	"encoding"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"

	"gitlab.com/haproxy-haptic/scriggo/native"
	"gopkg.in/yaml.v3"
)

const incrementalSerializationMaxDepth = 256

var (
	jsonMarshalerType       = reflect.TypeFor[json.Marshaler]()
	textMarshalerType       = reflect.TypeFor[encoding.TextMarshaler]()
	yamlMarshalerType       = reflect.TypeFor[yaml.Marshaler]()
	stringerType            = reflect.TypeFor[fmt.Stringer]()
	formatterType           = reflect.TypeFor[fmt.Formatter]()
	errorType               = reflect.TypeFor[error]()
	textFragmentType        = reflect.TypeFor[native.TextFragment]()
	envStringerType         = reflect.TypeFor[native.EnvStringer]()
	htmlStringerType        = reflect.TypeFor[native.HTMLStringer]()
	htmlEnvStringerType     = reflect.TypeFor[native.HTMLEnvStringer]()
	cssStringerType         = reflect.TypeFor[native.CSSStringer]()
	cssEnvStringerType      = reflect.TypeFor[native.CSSEnvStringer]()
	jsStringerType          = reflect.TypeFor[native.JSStringer]()
	jsEnvStringerType       = reflect.TypeFor[native.JSEnvStringer]()
	jsonStringerType        = reflect.TypeFor[native.JSONStringer]()
	jsonEnvStringerType     = reflect.TypeFor[native.JSONEnvStringer]()
	markdownStringerType    = reflect.TypeFor[native.MarkdownStringer]()
	markdownEnvStringerType = reflect.TypeFor[native.MarkdownEnvStringer]()
)

type incrementalSerializationVisit struct {
	typ     reflect.Type
	pointer uintptr
}

type incrementalSerializationCloneState struct {
	references         map[incrementalSerializationVisit]reflect.Value
	exportedFieldsOnly bool
}

type incrementalSerializationEqualityState struct {
	leftToRight map[incrementalSerializationVisit]incrementalSerializationVisit
	rightToLeft map[incrementalSerializationVisit]incrementalSerializationVisit
}

func validateIncrementalSerialization(value any) error {
	return validateIncrementalSerializationWithFields(value, false)
}

func validateIncrementalSerializationWithFields(value any, exportedFieldsOnly bool) error {
	return validateIncrementalSerializationValue(
		reflect.ValueOf(value),
		make(map[incrementalSerializationVisit]struct{}),
		0,
		exportedFieldsOnly,
	)
}

func cloneIncrementalSerialization(value any) (any, error) {
	return cloneIncrementalSerializationWithFields(value, false)
}

func cloneIncrementalExportedSerialization(value any) (any, error) {
	return cloneIncrementalSerializationWithFields(value, true)
}

func cloneIncrementalSerializationWithFields(value any, exportedFieldsOnly bool) (any, error) {
	if cloned, supported, err := clonePlainIncrementalSerialization(
		value,
		make(map[incrementalSerializationVisit]struct{}),
		0,
	); supported {
		return cloned, err
	}
	if err := validateIncrementalSerializationWithFields(value, exportedFieldsOnly); err != nil {
		return nil, err
	}
	cloned, err := cloneIncrementalSerializationValueWithState(
		reflect.ValueOf(value),
		&incrementalSerializationCloneState{
			references:         make(map[incrementalSerializationVisit]reflect.Value),
			exportedFieldsOnly: exportedFieldsOnly,
		},
		0,
	)
	if err != nil || !cloned.IsValid() {
		return nil, err
	}
	return cloned.Interface(), nil
}

func clonePlainIncrementalSerialization(
	value any,
	active map[incrementalSerializationVisit]struct{},
	depth int,
) (clone any, supported bool, err error) {
	if depth > incrementalSerializationMaxDepth {
		return nil, true, fmt.Errorf("value exceeds the incremental serialization depth limit")
	}
	switch typed := value.(type) {
	case nil, bool, string,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return typed, true, nil
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return nil, true, fmt.Errorf("non-finite float32 is unavailable in incremental templates")
		}
		return typed, true, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, true, fmt.Errorf("non-finite float64 is unavailable in incremental templates")
		}
		return typed, true, nil
	case map[string]any:
		return clonePlainIncrementalMap(typed, active, depth)
	case []any:
		return clonePlainIncrementalSlice(typed, active, depth)
	default:
		return nil, false, nil
	}
}

func clonePlainIncrementalMap(
	typed map[string]any,
	active map[incrementalSerializationVisit]struct{},
	depth int,
) (clone any, supported bool, err error) {
	if typed == nil {
		// A bare nil would box as an untyped nil interface, which
		// equalIncrementalSerialization treats as a different value.
		return map[string]any(nil), true, nil
	}
	reference := reflect.ValueOf(typed)
	if err := beginIncrementalSerializationReference(reference, active); err != nil {
		return nil, true, err
	}
	defer endIncrementalSerializationReference(reference, active)
	keys := make([]string, 0, len(typed))
	for key := range typed {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	cloned := make(map[string]any, len(typed))
	for _, key := range keys {
		item, supported, err := clonePlainIncrementalSerialization(typed[key], active, depth+2)
		if err != nil {
			return nil, true, fmt.Errorf("map key %q: %w", key, err)
		}
		if !supported {
			return nil, false, nil
		}
		cloned[key] = item
	}
	return cloned, true, nil
}

func clonePlainIncrementalSlice(
	typed []any,
	active map[incrementalSerializationVisit]struct{},
	depth int,
) (clone any, supported bool, err error) {
	if typed == nil {
		// A bare nil would box as an untyped nil interface, which
		// equalIncrementalSerialization treats as a different value.
		return []any(nil), true, nil
	}
	reference := reflect.ValueOf(typed)
	if err := beginIncrementalSerializationReference(reference, active); err != nil {
		return nil, true, err
	}
	defer endIncrementalSerializationReference(reference, active)
	cloned := make([]any, len(typed))
	for index := range typed {
		item, supported, err := clonePlainIncrementalSerialization(typed[index], active, depth+2)
		if err != nil {
			return nil, true, fmt.Errorf("index %d: %w", index, err)
		}
		if !supported {
			return nil, false, nil
		}
		cloned[index] = item
	}
	return cloned, true, nil
}

func equalIncrementalSerialization(left, right any) bool {
	return equalIncrementalSerializationValueWithState(
		reflect.ValueOf(left),
		reflect.ValueOf(right),
		&incrementalSerializationEqualityState{
			leftToRight: make(map[incrementalSerializationVisit]incrementalSerializationVisit),
			rightToLeft: make(map[incrementalSerializationVisit]incrementalSerializationVisit),
		},
		0,
	)
}

func equalIncrementalSerializationValueWithState(
	left reflect.Value,
	right reflect.Value,
	state *incrementalSerializationEqualityState,
	depth int,
) bool {
	if depth > incrementalSerializationMaxDepth || left.IsValid() != right.IsValid() {
		return false
	}
	if !left.IsValid() {
		return true
	}
	if left.Type() != right.Type() || left.Kind() != right.Kind() {
		return false
	}
	switch left.Kind() {
	case reflect.Interface:
		if left.IsNil() || right.IsNil() {
			return left.IsNil() == right.IsNil()
		}
		return equalIncrementalSerializationValueWithState(left.Elem(), right.Elem(), state, depth+1)
	case reflect.Pointer:
		return equalIncrementalSerializationPointerWithState(left, right, state, depth)
	case reflect.Slice:
		return equalIncrementalSerializationSliceWithState(left, right, state, depth)
	case reflect.Array:
		return equalIncrementalSerializationSequenceWithState(left, right, state, depth)
	case reflect.Map:
		return equalIncrementalSerializationMapWithState(left, right, state, depth)
	case reflect.Struct:
		return equalIncrementalSerializationStructWithState(left, right, state, depth)
	default:
		return equalIncrementalSerializationScalar(left, right)
	}
}

func equalIncrementalSerializationPointerWithState(
	left reflect.Value,
	right reflect.Value,
	state *incrementalSerializationEqualityState,
	depth int,
) bool {
	if left.IsNil() || right.IsNil() {
		return left.IsNil() == right.IsNil()
	}
	if !bindIncrementalSerializationReferences(left, right, state) {
		return false
	}
	return equalIncrementalSerializationValueWithState(left.Elem(), right.Elem(), state, depth+1)
}

func equalIncrementalSerializationSliceWithState(
	left reflect.Value,
	right reflect.Value,
	state *incrementalSerializationEqualityState,
	depth int,
) bool {
	if left.IsNil() || right.IsNil() {
		return left.IsNil() == right.IsNil()
	}
	if !bindIncrementalSerializationReferences(left, right, state) {
		return false
	}
	return equalIncrementalSerializationSequenceWithState(left, right, state, depth)
}

func equalIncrementalSerializationMapWithState(
	left reflect.Value,
	right reflect.Value,
	state *incrementalSerializationEqualityState,
	depth int,
) bool {
	if left.IsNil() || right.IsNil() {
		return left.IsNil() == right.IsNil()
	}
	if !bindIncrementalSerializationReferences(left, right, state) {
		return false
	}
	if left.Len() != right.Len() {
		return false
	}
	// map[string]any is the shape every rendered resource takes, and the
	// reflective path boxes three values per entry: the key, the lookup and the
	// element. Ranging both maps directly boxes none, because an element that
	// is already an interface needs no box.
	//
	// depth advances by two, not one: this skips the interface level the
	// reflective path would have descended, and the depth limit must count the
	// same either way. Nil elements follow the same rule the interface case
	// applies — equal only to another nil.
	if equal, handled := equalStringKeyedAnyMaps(left, right, state, depth); handled {
		return equal
	}
	iterator := left.MapRange()
	for iterator.Next() {
		rightValue := right.MapIndex(iterator.Key())
		if !rightValue.IsValid() ||
			!equalIncrementalSerializationValueWithState(iterator.Value(), rightValue, state, depth+1) {
			return false
		}
	}
	return true
}

// equalStringKeyedAnyMaps compares two map[string]any without boxing, and
// reports whether it was able to (handled). See the caller for why depth
// advances by two.
func equalStringKeyedAnyMaps(
	left, right reflect.Value,
	state *incrementalSerializationEqualityState,
	depth int,
) (equal, handled bool) {
	leftTyped, rightTyped, ok := stringKeyedAnyMapPair(left, right)
	if !ok {
		return false, false
	}
	for key, leftElement := range leftTyped {
		rightElement, present := rightTyped[key]
		if !present {
			return false, true
		}
		if leftElement == nil || rightElement == nil {
			if leftElement != nil || rightElement != nil {
				return false, true
			}
			continue
		}
		if !equalIncrementalSerializationValueWithState(
			reflect.ValueOf(leftElement), reflect.ValueOf(rightElement), state, depth+2) {
			return false, true
		}
	}
	return true, true
}

// stringKeyedAnyMapPair returns both sides as map[string]any when both are
// exactly that type. A named type with the same shape is refused: converting it
// would allocate the box this avoids.
func stringKeyedAnyMapPair(left, right reflect.Value) (leftMap, rightMap map[string]any, ok bool) {
	if left.Type() != stringKeyedAnyMapType || right.Type() != stringKeyedAnyMapType {
		return nil, nil, false
	}
	if !left.CanInterface() || !right.CanInterface() {
		return nil, nil, false
	}
	leftTyped, leftOK := left.Interface().(map[string]any)
	rightTyped, rightOK := right.Interface().(map[string]any)
	return leftTyped, rightTyped, leftOK && rightOK
}

func equalIncrementalSerializationStructWithState(
	left reflect.Value,
	right reflect.Value,
	state *incrementalSerializationEqualityState,
	depth int,
) bool {
	for index := range left.NumField() {
		if !left.Type().Field(index).IsExported() {
			continue
		}
		if !equalIncrementalSerializationValueWithState(left.Field(index), right.Field(index), state, depth+1) {
			return false
		}
	}
	return true
}

func equalIncrementalSerializationScalar(left, right reflect.Value) bool {
	switch left.Kind() {
	case reflect.Bool:
		return left.Bool() == right.Bool()
	case reflect.String:
		return left.String() == right.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return left.Int() == right.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return left.Uint() == right.Uint()
	case reflect.Float32:
		return math.Float32bits(float32(left.Float())) == math.Float32bits(float32(right.Float()))
	case reflect.Float64:
		return math.Float64bits(left.Float()) == math.Float64bits(right.Float())
	default:
		return false
	}
}

func equalIncrementalSerializationSequenceWithState(
	left reflect.Value,
	right reflect.Value,
	state *incrementalSerializationEqualityState,
	depth int,
) bool {
	if left.Len() != right.Len() {
		return false
	}
	for index := range left.Len() {
		if !equalIncrementalSerializationValueWithState(left.Index(index), right.Index(index), state, depth+1) {
			return false
		}
	}
	return true
}

func bindIncrementalSerializationReferences(
	left reflect.Value,
	right reflect.Value,
	state *incrementalSerializationEqualityState,
) bool {
	leftVisit := incrementalSerializationVisit{typ: left.Type(), pointer: left.Pointer()}
	rightVisit := incrementalSerializationVisit{typ: right.Type(), pointer: right.Pointer()}
	if existing, found := state.leftToRight[leftVisit]; found && existing != rightVisit {
		return false
	}
	if existing, found := state.rightToLeft[rightVisit]; found && existing != leftVisit {
		return false
	}
	state.leftToRight[leftVisit] = rightVisit
	state.rightToLeft[rightVisit] = leftVisit
	return true
}

func cloneIncrementalSerializationValue(value reflect.Value, depth int) (reflect.Value, error) {
	return cloneIncrementalSerializationValueWithState(
		value,
		&incrementalSerializationCloneState{
			references: make(map[incrementalSerializationVisit]reflect.Value),
		},
		depth,
	)
}

func cloneIncrementalSerializationValueWithState(
	value reflect.Value,
	state *incrementalSerializationCloneState,
	depth int,
) (reflect.Value, error) {
	if depth > incrementalSerializationMaxDepth {
		return reflect.Value{}, fmt.Errorf("value exceeds the incremental serialization depth limit")
	}
	if !value.IsValid() {
		return reflect.Value{}, nil
	}
	switch value.Kind() {
	case reflect.Interface:
		return cloneIncrementalInterfaceWithState(value, state, depth)
	case reflect.Pointer:
		return cloneIncrementalPointerWithState(value, state, depth)
	case reflect.Slice:
		return cloneIncrementalSliceWithState(value, state, depth)
	case reflect.Array:
		result := reflect.New(value.Type()).Elem()
		if err := cloneIncrementalSequenceIntoWithState(result, value, state, depth); err != nil {
			return reflect.Value{}, err
		}
		return result, nil
	case reflect.Map:
		return cloneIncrementalMapWithState(value, state, depth)
	case reflect.Struct:
		return cloneIncrementalStructWithState(value, state, depth)
	default:
		return value, nil
	}
}

func cloneIncrementalInterfaceWithState(
	value reflect.Value,
	state *incrementalSerializationCloneState,
	depth int,
) (reflect.Value, error) {
	if value.IsNil() {
		return reflect.Zero(value.Type()), nil
	}
	cloned, err := cloneIncrementalSerializationValueWithState(value.Elem(), state, depth+1)
	if err != nil {
		return reflect.Value{}, err
	}
	result := reflect.New(value.Type()).Elem()
	result.Set(cloned)
	return result, nil
}

func cloneIncrementalPointerWithState(
	value reflect.Value,
	state *incrementalSerializationCloneState,
	depth int,
) (reflect.Value, error) {
	if value.IsNil() {
		return reflect.Zero(value.Type()), nil
	}
	visit := incrementalSerializationVisit{typ: value.Type(), pointer: value.Pointer()}
	if cloned, found := state.references[visit]; found {
		return cloned, nil
	}
	result := reflect.New(value.Type().Elem())
	state.references[visit] = result
	cloned, err := cloneIncrementalSerializationValueWithState(value.Elem(), state, depth+1)
	if err != nil {
		return reflect.Value{}, err
	}
	result.Elem().Set(cloned)
	return result, nil
}

func cloneIncrementalSliceWithState(
	value reflect.Value,
	state *incrementalSerializationCloneState,
	depth int,
) (reflect.Value, error) {
	if value.IsNil() {
		return reflect.Zero(value.Type()), nil
	}
	visit := incrementalSerializationVisit{typ: value.Type(), pointer: value.Pointer()}
	if cloned, found := state.references[visit]; found {
		return cloned, nil
	}
	result := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
	state.references[visit] = result
	if err := cloneIncrementalSequenceIntoWithState(result, value, state, depth); err != nil {
		return reflect.Value{}, err
	}
	return result, nil
}

func cloneIncrementalSequenceIntoWithState(
	result reflect.Value,
	value reflect.Value,
	state *incrementalSerializationCloneState,
	depth int,
) error {
	for index := range value.Len() {
		cloned, err := cloneIncrementalSerializationValueWithState(value.Index(index), state, depth+1)
		if err != nil {
			return fmt.Errorf("index %d: %w", index, err)
		}
		result.Index(index).Set(cloned)
	}
	return nil
}

func cloneIncrementalMapWithState(
	value reflect.Value,
	state *incrementalSerializationCloneState,
	depth int,
) (reflect.Value, error) {
	if value.IsNil() {
		return reflect.Zero(value.Type()), nil
	}
	visit := incrementalSerializationVisit{typ: value.Type(), pointer: value.Pointer()}
	if cloned, found := state.references[visit]; found {
		return cloned, nil
	}
	result := reflect.MakeMapWithSize(value.Type(), value.Len())
	state.references[visit] = result
	iterator := value.MapRange()
	for iterator.Next() {
		cloned, err := cloneIncrementalSerializationValueWithState(iterator.Value(), state, depth+1)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("map key %q: %w", iterator.Key().String(), err)
		}
		result.SetMapIndex(iterator.Key(), cloned)
	}
	return result, nil
}

func cloneIncrementalStructWithState(
	value reflect.Value,
	state *incrementalSerializationCloneState,
	depth int,
) (reflect.Value, error) {
	result := reflect.New(value.Type()).Elem()
	for index := range value.NumField() {
		fieldType := value.Type().Field(index)
		if !fieldType.IsExported() {
			if state.exportedFieldsOnly {
				continue
			}
			return reflect.Value{}, fmt.Errorf("field %s is unexported", fieldType.Name)
		}
		cloned, err := cloneIncrementalSerializationValueWithState(value.Field(index), state, depth+1)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("field %s: %w", fieldType.Name, err)
		}
		result.Field(index).Set(cloned)
	}
	return result, nil
}

func validateIncrementalSerializationValue(
	value reflect.Value,
	active map[incrementalSerializationVisit]struct{},
	depth int,
	exportedFieldsOnly bool,
) error {
	if depth > incrementalSerializationMaxDepth {
		return fmt.Errorf("value exceeds the incremental serialization depth limit")
	}
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return validateIncrementalSerializationValue(value.Elem(), active, depth+1, exportedFieldsOnly)
	}
	if incrementalSerializationUsesCustomMethod(value.Type()) {
		return fmt.Errorf("type %s uses a custom marshaler", value.Type())
	}

	switch value.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	case reflect.Float32, reflect.Float64:
		return validateIncrementalSerializationFloat(value)
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return validateIncrementalSerializationReference(value, active, depth, exportedFieldsOnly)
	case reflect.Slice, reflect.Array:
		return validateIncrementalSerializationSequence(value, active, depth, exportedFieldsOnly)
	case reflect.Map:
		return validateIncrementalSerializationMap(value, active, depth, exportedFieldsOnly)
	case reflect.Struct:
		return validateIncrementalSerializationStruct(value, active, depth, exportedFieldsOnly)
	default:
		return fmt.Errorf("type %s is unavailable in incremental serialization", value.Type())
	}
}

func validateIncrementalSerializationFloat(value reflect.Value) error {
	floating := value.Float()
	if math.IsNaN(floating) || math.IsInf(floating, 0) {
		return fmt.Errorf("non-finite %s is unavailable in incremental templates", value.Kind())
	}
	return nil
}

func validateIncrementalSerializationSequence(
	value reflect.Value,
	active map[incrementalSerializationVisit]struct{},
	depth int,
	exportedFieldsOnly bool,
) error {
	if value.Kind() == reflect.Slice {
		if value.IsNil() {
			return nil
		}
		if err := beginIncrementalSerializationReference(value, active); err != nil {
			return err
		}
		defer endIncrementalSerializationReference(value, active)
	}
	for i := range value.Len() {
		if err := validateIncrementalSerializationValue(
			value.Index(i), active, depth+1, exportedFieldsOnly,
		); err != nil {
			return fmt.Errorf("index %d: %w", i, err)
		}
	}
	return nil
}

func validateIncrementalSerializationStruct(
	value reflect.Value,
	active map[incrementalSerializationVisit]struct{},
	depth int,
	exportedFieldsOnly bool,
) error {
	for i := range value.NumField() {
		fieldType := value.Type().Field(i)
		if !fieldType.IsExported() {
			if exportedFieldsOnly {
				continue
			}
			return fmt.Errorf("field %s is unexported", fieldType.Name)
		}
		if err := validateIncrementalSerializationValue(
			value.Field(i), active, depth+1, exportedFieldsOnly,
		); err != nil {
			return fmt.Errorf("field %s: %w", fieldType.Name, err)
		}
	}
	return nil
}

func validateIncrementalSerializationReference(
	value reflect.Value,
	active map[incrementalSerializationVisit]struct{},
	depth int,
	exportedFieldsOnly bool,
) error {
	if err := beginIncrementalSerializationReference(value, active); err != nil {
		return err
	}
	defer endIncrementalSerializationReference(value, active)
	return validateIncrementalSerializationValue(value.Elem(), active, depth+1, exportedFieldsOnly)
}

func validateIncrementalSerializationMap(
	value reflect.Value,
	active map[incrementalSerializationVisit]struct{},
	depth int,
	exportedFieldsOnly bool,
) error {
	if value.IsNil() {
		return nil
	}
	if value.Type().Key().Kind() != reflect.String {
		return fmt.Errorf("map key type %s is unavailable in incremental serialization", value.Type().Key())
	}
	if incrementalSerializationUsesCustomMethod(value.Type().Key()) {
		return fmt.Errorf("map key type %s uses a custom marshaler", value.Type().Key())
	}
	if err := beginIncrementalSerializationReference(value, active); err != nil {
		return err
	}
	defer endIncrementalSerializationReference(value, active)

	keys := make([]string, 0, value.Len())
	for _, key := range value.MapKeys() {
		keys = append(keys, key.String())
	}
	slices.Sort(keys)
	for _, key := range keys {
		mapKey := reflect.ValueOf(key)
		if mapKey.Type() != value.Type().Key() {
			mapKey = mapKey.Convert(value.Type().Key())
		}
		if err := validateIncrementalSerializationValue(
			value.MapIndex(mapKey), active, depth+1, exportedFieldsOnly,
		); err != nil {
			return fmt.Errorf("map key %q: %w", key, err)
		}
	}
	return nil
}

func beginIncrementalSerializationReference(
	value reflect.Value,
	active map[incrementalSerializationVisit]struct{},
) error {
	visit := incrementalSerializationVisit{typ: value.Type(), pointer: value.Pointer()}
	if _, exists := active[visit]; exists {
		return fmt.Errorf("type %s contains a reference cycle", value.Type())
	}
	active[visit] = struct{}{}
	return nil
}

func endIncrementalSerializationReference(
	value reflect.Value,
	active map[incrementalSerializationVisit]struct{},
) {
	delete(active, incrementalSerializationVisit{typ: value.Type(), pointer: value.Pointer()})
}

func incrementalSerializationUsesCustomMethod(typ reflect.Type) bool {
	if incrementalSerializationTypeUsesCustomMethod(typ) {
		return true
	}
	return typ.Kind() != reflect.Pointer &&
		incrementalSerializationTypeUsesCustomMethod(reflect.PointerTo(typ))
}

func incrementalSerializationTypeUsesCustomMethod(typ reflect.Type) bool {
	return typ.Implements(jsonMarshalerType) || typ.Implements(textMarshalerType) ||
		typ.Implements(yamlMarshalerType) || typ.Implements(stringerType) ||
		typ.Implements(formatterType) || typ.Implements(errorType) ||
		typ.Implements(textFragmentType) || typ.Implements(envStringerType) ||
		typ.Implements(htmlStringerType) || typ.Implements(htmlEnvStringerType) ||
		typ.Implements(cssStringerType) || typ.Implements(cssEnvStringerType) ||
		typ.Implements(jsStringerType) || typ.Implements(jsEnvStringerType) ||
		typ.Implements(jsonStringerType) || typ.Implements(jsonEnvStringerType) ||
		typ.Implements(markdownStringerType) || typ.Implements(markdownEnvStringerType)
}
