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
)

// CanonicalIncrementalResourceKeys validates resource lookup keys without native coercion methods.
func CanonicalIncrementalResourceKeys(keys ...any) ([]string, error) {
	canonical := make([]string, len(keys))
	for index, key := range keys {
		value, err := CanonicalIncrementalResourceKey(index, key)
		if err != nil {
			return nil, err
		}
		canonical[index] = value
	}
	return canonical, nil
}

// CanonicalIncrementalResourceKey validates one indexed resource lookup key.
func CanonicalIncrementalResourceKey(index int, key any) (string, error) {
	if key == nil {
		return CanonicalIncrementalResourceValue(index, reflect.Value{})
	}
	return CanonicalIncrementalResourceValue(index, reflect.ValueOf(key))
}

// CanonicalIncrementalResourceValue validates one indexed resource lookup key held in a reflection value.
func CanonicalIncrementalResourceValue(index int, key reflect.Value) (string, error) {
	if index < 0 {
		return "", fmt.Errorf("resource lookup key index %d is invalid", index)
	}
	for key.IsValid() && key.Kind() == reflect.Interface {
		if key.IsNil() {
			key = reflect.Value{}
			break
		}
		key = key.Elem()
	}
	if key.IsValid() {
		typ := key.Type()
		if typ.Kind() == reflect.Pointer {
			return "", fmt.Errorf("resource lookup key %d: pointer type %s is unavailable", index, typ)
		}
		if err := rejectIncrementalNativeMethods(typ); err != nil {
			return "", fmt.Errorf("resource lookup key %d: %w", index, err)
		}
	}
	scalar, err := deterministicScalarOfValue(key)
	if err != nil {
		return "", fmt.Errorf("resource lookup key %d: %w", index, err)
	}
	return scalar.text, nil
}
