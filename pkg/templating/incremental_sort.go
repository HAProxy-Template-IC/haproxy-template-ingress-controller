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
	"reflect"
	"sort"
	"strings"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalSortCriterion struct {
	expression string
	descending bool
	exists     bool
	length     bool
}

type incrementalSortableItems struct {
	items      []any
	criteria   []incrementalSortCriterion
	cachedKeys [][]deterministicScalar
}

func incrementalSortByAdaptive() native.AdaptiveFunc {
	return native.AdaptiveFunc{
		Impl: func(env native.Env, slice, by any) any {
			rv, ok := sliceOf(slice)
			if !ok {
				return slice
			}
			criteria, isCriteria := asCriteria(by)
			if !isCriteria {
				incrementalStop(env, FilterSortBy, fmt.Errorf("comparator functions are unavailable; use string criteria"))
				return nil
			}
			result, err := incrementalSortByCriteria(rv, criteria)
			if err != nil {
				incrementalStop(env, FilterSortBy, err)
				return nil
			}
			return result
		},
		ReturnType: identityReturnType,
	}
}

func incrementalSortByCriteria(slice reflect.Value, rawCriteria []string) (any, error) {
	criteria := make([]incrementalSortCriterion, len(rawCriteria))
	for i, raw := range rawCriteria {
		criterion, err := parseIncrementalSortCriterion(raw)
		if err != nil {
			return nil, err
		}
		criteria[i] = criterion
	}

	items := make([]any, slice.Len())
	for i := range slice.Len() {
		items[i] = slice.Index(i).Interface()
	}
	sortable := &incrementalSortableItems{items: items, criteria: criteria}
	if err := sortable.precomputeKeys(); err != nil {
		return nil, err
	}
	sort.Stable(sortable)

	result := reflect.MakeSlice(slice.Type(), 0, len(items))
	for _, item := range items {
		value := reflect.ValueOf(item)
		if !value.IsValid() {
			value = reflect.Zero(slice.Type().Elem())
		}
		result = reflect.Append(result, value)
	}
	return result.Interface(), nil
}

func parseIncrementalSortCriterion(raw string) (incrementalSortCriterion, error) {
	parts := strings.Split(raw, ":")
	criterion := incrementalSortCriterion{expression: strings.TrimSpace(parts[0])}
	for _, rawModifier := range parts[1:] {
		modifier := strings.TrimSpace(rawModifier)
		switch modifier {
		case sortModifierDesc:
			criterion.descending = true
		case sortModifierExists:
			criterion.exists = true
		default:
			return incrementalSortCriterion{}, fmt.Errorf("criterion %q has unknown modifier %q", raw, modifier)
		}
	}
	if strings.Contains(criterion.expression, " | length") {
		criterion.length = true
		criterion.expression = strings.TrimSpace(strings.ReplaceAll(criterion.expression, " | length", ""))
	}
	return criterion, nil
}

func (s *incrementalSortableItems) precomputeKeys() error {
	s.cachedKeys = make([][]deterministicScalar, len(s.items))
	for itemIndex, item := range s.items {
		s.cachedKeys[itemIndex] = make([]deterministicScalar, len(s.criteria))
		for criterionIndex, criterion := range s.criteria {
			value, _, err := incrementalEvaluateExpression(item, criterion.expression)
			if err != nil {
				return fmt.Errorf("criterion %q at index %d: %w", criterion.expression, itemIndex, err)
			}
			if criterion.exists {
				value = value != nil && !isNilValue(value)
			} else if criterion.length {
				length, err := incrementalLength(value)
				if err != nil {
					return fmt.Errorf("criterion %q at index %d: %w", criterion.expression, itemIndex, err)
				}
				value = length
			}
			scalar, err := deterministicScalarOf(value)
			if err != nil {
				return fmt.Errorf("criterion %q at index %d: %w", criterion.expression, itemIndex, err)
			}
			s.cachedKeys[itemIndex][criterionIndex] = scalar
		}
	}
	return nil
}

func incrementalLength(value any) (int, error) {
	if value == nil {
		return 0, nil
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return rv.Len(), nil
	default:
		return 0, fmt.Errorf("length is unavailable for %T", value)
	}
}

func (s *incrementalSortableItems) Len() int {
	return len(s.items)
}

func (s *incrementalSortableItems) Less(left, right int) bool {
	for criterionIndex, criterion := range s.criteria {
		compared := compareDeterministicScalars(
			s.cachedKeys[left][criterionIndex],
			s.cachedKeys[right][criterionIndex],
		)
		if compared == 0 {
			continue
		}
		if criterion.descending {
			return compared > 0
		}
		return compared < 0
	}
	return false
}

func (s *incrementalSortableItems) Swap(left, right int) {
	s.items[left], s.items[right] = s.items[right], s.items[left]
	s.cachedKeys[left], s.cachedKeys[right] = s.cachedKeys[right], s.cachedKeys[left]
}

func incrementalEvaluateExpression(item any, expression string) (value any, found bool, err error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, false, nil
	}
	if expression == "$" {
		return item, true, nil
	}
	expression = strings.TrimPrefix(expression, "$.")
	current := item
	for _, segment := range strings.Split(expression, ".") {
		if current == nil {
			return nil, false, nil
		}
		if strings.HasPrefix(segment, "[") && strings.HasSuffix(segment, "]") {
			index, valid := incrementalParseInt(strings.TrimSuffix(strings.TrimPrefix(segment, "["), "]"))
			if !valid {
				return nil, false, nil
			}
			var indexFound bool
			current, indexFound = incrementalIndex(current, index)
			if !indexFound {
				return nil, false, nil
			}
			continue
		}
		next, nextFound, fieldErr := incrementalField(current, segment)
		if fieldErr != nil {
			return nil, false, fieldErr
		}
		if !nextFound {
			return nil, false, nil
		}
		current = next
	}
	return current, true, nil
}

func incrementalIndex(value any, index int) (result any, found bool) {
	rv := reflect.ValueOf(value)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, false
	}
	if index < 0 || index >= rv.Len() {
		return nil, false
	}
	return rv.Index(index).Interface(), true
}

func incrementalField(value any, fieldName string) (result any, found bool, err error) {
	if value == nil {
		return nil, false, nil
	}
	field, found, err := incrementalFieldValue(reflect.ValueOf(value), fieldName)
	if err != nil || !found {
		return nil, found, err
	}
	return field.Interface(), true, nil
}

func incrementalFieldValue(rv reflect.Value, fieldName string) (result reflect.Value, found bool, err error) {
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return reflect.Value{}, false, nil
		}
		rv = rv.Elem()
	}
	if !rv.IsValid() {
		return reflect.Value{}, false, nil
	}

	switch rv.Kind() {
	case reflect.Map:
		return incrementalMapFieldValue(rv, fieldName)
	case reflect.Struct:
		return incrementalStructFieldValue(rv, fieldName)
	default:
		return reflect.Value{}, false, nil
	}
}

func incrementalMapFieldValue(rv reflect.Value, fieldName string) (reflect.Value, bool, error) {
	if rv.Type().Key().Kind() != reflect.String {
		return reflect.Value{}, false, fmt.Errorf("cannot navigate map with key type %s", rv.Type().Key())
	}
	if rv.Type() == reflect.TypeFor[map[string]any]() {
		field, exists := rv.Interface().(map[string]any)[fieldName]
		if !exists {
			return reflect.Value{}, false, nil
		}
		if field == nil {
			return rv.MapIndex(reflect.ValueOf(fieldName)), true, nil
		}
		return reflect.ValueOf(field), true, nil
	}
	key := reflect.ValueOf(fieldName)
	if key.Type() != rv.Type().Key() {
		key = key.Convert(rv.Type().Key())
	}
	field := rv.MapIndex(key)
	if !field.IsValid() {
		return reflect.Value{}, false, nil
	}
	return field, true, nil
}

func incrementalStructFieldValue(rv reflect.Value, fieldName string) (reflect.Value, bool, error) {
	index, ok := structFieldIndex(rv.Type(), fieldName)
	if !ok {
		return reflect.Value{}, false, nil
	}
	field := rv.Field(index)
	if isStructFieldOmitempty(rv.Type(), index) && field.IsZero() {
		return reflect.Value{}, false, nil
	}
	if !field.CanInterface() {
		return reflect.Value{}, false, fmt.Errorf("field %q on %s is unavailable", fieldName, rv.Type())
	}
	return field, true, nil
}
