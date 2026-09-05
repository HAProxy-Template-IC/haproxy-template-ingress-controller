// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package typegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"unicode/utf8"
)

const immutableJSONMaxDepth = 256

type immutableJSONVisit struct {
	kind    reflect.Kind
	pointer uintptr
}

// MarshalImmutableJSON encodes the normalized Kubernetes JSON value surface.
func MarshalImmutableJSON(value any) ([]byte, error) {
	if err := validateImmutableJSON(value, make(map[immutableJSONVisit]struct{}), 0); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validateImmutableJSON(value any, active map[immutableJSONVisit]struct{}, depth int) error {
	if depth > immutableJSONMaxDepth {
		return errors.New("resource value exceeds the maximum depth")
	}
	switch typed := value.(type) {
	case nil, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return nil
	case string:
		if !utf8.ValidString(typed) {
			return errors.New("resource value contains an invalid UTF-8 string")
		}
		return nil
	case float32:
		return validateImmutableJSONFloat(float64(typed), "float32")
	case float64:
		return validateImmutableJSONFloat(typed, "float64")
	case map[string]any:
		return validateImmutableJSONMap(typed, active, depth)
	case []any:
		return validateImmutableJSONList(typed, active, depth)
	default:
		return fmt.Errorf("resource value type %T is unavailable", value)
	}
}

func validateImmutableJSONMap(
	value map[string]any,
	active map[immutableJSONVisit]struct{},
	depth int,
) error {
	if value == nil {
		return nil
	}
	visit, err := beginImmutableJSONVisit(value, active)
	if err != nil {
		return err
	}
	defer delete(active, visit)
	for key, item := range value {
		if !utf8.ValidString(key) {
			return errors.New("resource value contains an invalid UTF-8 map key")
		}
		if err := validateImmutableJSON(item, active, depth+1); err != nil {
			return fmt.Errorf("resource map key %q: %w", key, err)
		}
	}
	return nil
}

func validateImmutableJSONList(
	value []any,
	active map[immutableJSONVisit]struct{},
	depth int,
) error {
	if value == nil {
		return nil
	}
	visit, err := beginImmutableJSONVisit(value, active)
	if err != nil {
		return err
	}
	defer delete(active, visit)
	for index, item := range value {
		if err := validateImmutableJSON(item, active, depth+1); err != nil {
			return fmt.Errorf("resource list index %d: %w", index, err)
		}
	}
	return nil
}

func beginImmutableJSONVisit(
	value any,
	active map[immutableJSONVisit]struct{},
) (immutableJSONVisit, error) {
	reflected := reflect.ValueOf(value)
	visit := immutableJSONVisit{kind: reflected.Kind(), pointer: reflected.Pointer()}
	if _, exists := active[visit]; exists {
		return immutableJSONVisit{}, errors.New("resource value contains a reference cycle")
	}
	active[visit] = struct{}{}
	return visit, nil
}

func validateImmutableJSONFloat(value float64, kind string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("resource value contains a non-finite %s", kind)
	}
	return nil
}
