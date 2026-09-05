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
	"slices"
)

var incrementalStringerType = reflect.TypeFor[fmt.Stringer]()

const incrementalTypeField = "type"

var incrementalHTTPOptionKeys = map[string]struct{}{
	"interval": {},
	"delay":    {},
	"timeout":  {},
	"retries":  {},
	"critical": {},
}

var incrementalHTTPAuthKeys = map[string]struct{}{
	incrementalTypeField: {},
	"username":           {},
	"password":           {},
	"token":              {},
	"headers":            {},
}

// CanonicalIncrementalHTTPArgs validates and detaches arguments before a tracked HTTP fetch.
func CanonicalIncrementalHTTPArgs(args ...any) ([]any, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("http.Fetch requires 1 to 3 arguments, got %d", len(args))
	}
	url, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("http.Fetch: url must be a plain string, got %T", args[0])
	}
	canonical := make([]any, len(args))
	canonical[0] = url
	if len(args) >= 2 {
		if args[1] != nil {
			options, err := canonicalIncrementalHTTPOptions(args[1])
			if err != nil {
				return nil, err
			}
			canonical[1] = options
		}
	}
	if len(args) == 3 {
		if args[2] != nil {
			auth, err := canonicalIncrementalHTTPAuth(args[2])
			if err != nil {
				return nil, err
			}
			canonical[2] = auth
		}
	}
	return canonical, nil
}

func canonicalIncrementalHTTPOptions(value any) (map[string]any, error) {
	options, err := canonicalIncrementalHTTPMap("options", value, incrementalHTTPOptionKeys)
	if err != nil {
		return nil, err
	}
	if _, interval := options["interval"]; interval {
		if _, delay := options["delay"]; delay {
			return nil, fmt.Errorf("http.Fetch: set either %q or %q, not both", "interval", "delay")
		}
	}
	for key, option := range options {
		var canonical any
		switch key {
		case "interval", "delay", "timeout":
			plain, ok := option.(string)
			if !ok {
				return nil, incrementalHTTPScalarError("option", key, "a plain duration string", option)
			}
			canonical = plain
		case "retries":
			retries, err := canonicalIncrementalHTTPRetries(option)
			if err != nil {
				return nil, err
			}
			canonical = retries
		case "critical":
			critical, ok := option.(bool)
			if !ok {
				return nil, incrementalHTTPScalarError("option", key, "a bool", option)
			}
			canonical = critical
		default:
			panic("templating: unknown incremental HTTP option")
		}
		options[key] = canonical
	}
	return options, nil
}

func canonicalIncrementalHTTPRetries(value any) (int, error) {
	if err := rejectIncrementalHTTPNativeType(value); err != nil {
		return 0, fmt.Errorf("http.Fetch: option %q: %w", "retries", err)
	}
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		return 0, fmt.Errorf("http.Fetch: option %q: %w", "retries", err)
	}
	switch scalar.kind {
	case deterministicSignedScalar, deterministicUnsignedScalar, deterministicFloatScalar:
	default:
		return 0, incrementalHTTPScalarError("option", "retries", "a finite number", value)
	}
	retries, err := deterministicScalarInt(scalar)
	if err != nil {
		return 0, fmt.Errorf("http.Fetch: option %q: %w", "retries", err)
	}
	return retries, nil
}

func canonicalIncrementalHTTPAuth(value any) (map[string]any, error) {
	auth, err := canonicalIncrementalHTTPMap("auth", value, incrementalHTTPAuthKeys)
	if err != nil {
		return nil, err
	}
	for key, field := range auth {
		if key == "headers" {
			headers, headerErr := canonicalIncrementalHTTPHeaders(field)
			if headerErr != nil {
				return nil, headerErr
			}
			auth[key] = headers
			continue
		}
		plain, ok := field.(string)
		if !ok {
			return nil, incrementalHTTPScalarError("auth field", key, "a plain string", field)
		}
		auth[key] = plain
	}
	return auth, nil
}

func canonicalIncrementalHTTPHeaders(value any) (map[string]any, error) {
	headers, err := canonicalIncrementalHTTPMap("auth headers", value, nil)
	if err != nil {
		return nil, err
	}
	for key, header := range headers {
		plain, ok := header.(string)
		if !ok {
			return nil, incrementalHTTPScalarError("header", key, "a plain string", header)
		}
		headers[key] = plain
	}
	return headers, nil
}

func canonicalIncrementalHTTPMap(
	name string,
	value any,
	allowed map[string]struct{},
) (map[string]any, error) {
	if err := rejectIncrementalHTTPNativeType(value); err != nil {
		return nil, fmt.Errorf("http.Fetch: %s: %w", name, err)
	}
	mapValue := reflect.ValueOf(value)
	if mapValue.Kind() != reflect.Map || mapValue.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("http.Fetch: %s must be a string-keyed map, got %T", name, value)
	}
	if err := rejectIncrementalNativeMethods(mapValue.Type().Key()); err != nil {
		return nil, fmt.Errorf("http.Fetch: %s key: %w", name, err)
	}
	keys := make([]string, 0, mapValue.Len())
	for _, key := range mapValue.MapKeys() {
		keys = append(keys, key.String())
	}
	slices.Sort(keys)
	canonical := make(map[string]any, len(keys))
	for _, key := range keys {
		if allowed != nil {
			if _, ok := allowed[key]; !ok {
				return nil, fmt.Errorf("http.Fetch: unknown %s key %q", name, key)
			}
		}
		mapKey := reflect.ValueOf(key)
		if mapKey.Type() != mapValue.Type().Key() {
			mapKey = mapKey.Convert(mapValue.Type().Key())
		}
		field := mapValue.MapIndex(mapKey)
		if field.Kind() == reflect.Interface && field.IsNil() {
			canonical[key] = nil
			continue
		}
		canonical[key] = field.Interface()
	}
	return canonical, nil
}

func incrementalHTTPScalarError(kind, key, expected string, value any) error {
	if err := rejectIncrementalHTTPNativeType(value); err != nil {
		return fmt.Errorf("http.Fetch: %s %q: %w", kind, key, err)
	}
	return fmt.Errorf("http.Fetch: %s %q must be %s, got %T", kind, key, expected, value)
}

func rejectIncrementalHTTPNativeType(value any) error {
	if value == nil {
		return nil
	}
	return rejectIncrementalNativeMethods(reflect.TypeOf(value))
}

func rejectIncrementalNativeMethods(typ reflect.Type) error {
	if typ.Implements(incrementalStringerType) ||
		typ.Kind() != reflect.Pointer && reflect.PointerTo(typ).Implements(incrementalStringerType) {
		return fmt.Errorf("type %s implements fmt.Stringer", typ)
	}
	if incrementalSerializationUsesCustomMethod(typ) {
		return fmt.Errorf("type %s uses a custom marshaler", typ)
	}
	return nil
}
