// Copyright 2025 Philipp Hossner
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

package config

import (
	"fmt"
	"sort"
)

// ValidationTestSource is one contributor of validation tests: the config's own
// inline `spec.validationTests`, or a HAProxyValidationTests object selected by
// it.
type ValidationTestSource struct {
	// Origin identifies the contributor in error messages. It is the only way an
	// operator learns which two objects collided, so it must name the object —
	// e.g. "HAProxyValidationTests/haptic-config-tests".
	Origin string

	Tests map[string]ValidationTest
}

// UnionValidationTests combines every source into the single map the test
// runner consumes.
//
// A test name may appear in only one source. Silently letting the last writer
// win would leave the losing definition's author believing an assertion runs
// that does not, so a collision is an error naming both sides.
//
// The reserved `_global` entry is the exception and must be unioned rather than
// rejected: it is a shared baseline that several template libraries each
// contribute part of, so "duplicate" is its normal state. Its fixtures
// accumulate; a field that cannot accumulate (a scalar two sources both set to
// different values) is still a collision.
//
// Sources are processed in the order given, which fixes the order of
// accumulated fixtures so a render is reproducible.
func UnionValidationTests(sources []ValidationTestSource) (map[string]ValidationTest, error) {
	union := make(map[string]ValidationTest)
	// Which source contributed each name, for the collision message.
	origin := make(map[string]string)

	for _, src := range sources {
		for _, name := range sortedTestNames(src.Tests) {
			test := src.Tests[name]

			if name == GlobalValidationTestName {
				merged, err := mergeGlobalBaseline(union[name], test, origin[name], src.Origin)
				if err != nil {
					return nil, err
				}
				union[name] = merged
				if origin[name] == "" {
					origin[name] = src.Origin
				}
				continue
			}

			if prev, dup := origin[name]; dup {
				return nil, fmt.Errorf(
					"validationTest %q is defined by both %s and %s: a test may be defined once, "+
						"otherwise one definition silently replaces the other and its assertions never run",
					name, prev, src.Origin)
			}
			origin[name] = src.Origin
			union[name] = test
		}
	}

	return union, nil
}

// mergeGlobalBaseline accumulates one source's `_global` contribution onto what
// earlier sources contributed.
func mergeGlobalBaseline(acc, add ValidationTest, accOrigin, addOrigin string) (ValidationTest, error) {
	if acc.Fixtures == nil && add.Fixtures != nil {
		acc.Fixtures = make(map[string][]any, len(add.Fixtures))
	}
	for _, kind := range sortedFixtureKinds(add.Fixtures) {
		acc.Fixtures[kind] = append(acc.Fixtures[kind], add.Fixtures[kind]...)
	}

	acc.HTTPFixtures = append(acc.HTTPFixtures, add.HTTPFixtures...)
	acc.Requires = appendUnique(acc.Requires, add.Requires)
	acc.RequiresFields = appendUnique(acc.RequiresFields, add.RequiresFields)

	if acc.Description == "" {
		acc.Description = add.Description
	}

	// Scalars and same-key map entries cannot accumulate: two different values
	// mean one baseline silently overrides the other, and every test in the
	// suite inherits whichever won.
	var err error
	if acc.CurrentConfig, err = mergeScalar(acc.CurrentConfig, add.CurrentConfig, "currentConfig", accOrigin, addOrigin); err != nil {
		return acc, err
	}
	if acc.MinHAProxyVersion, err = mergeScalar(acc.MinHAProxyVersion, add.MinHAProxyVersion, "minHAProxyVersion", accOrigin, addOrigin); err != nil {
		return acc, err
	}
	if acc.CurrentFiles, err = mergeStringMap(acc.CurrentFiles, add.CurrentFiles, "currentFiles", accOrigin, addOrigin); err != nil {
		return acc, err
	}
	if acc.ExtraContext, err = mergeAnyMap(acc.ExtraContext, add.ExtraContext, "extraContext", accOrigin, addOrigin); err != nil {
		return acc, err
	}

	// `_global` assertions are never executed — the runner treats the entry as a
	// baseline, not a test — so they are carried for completeness only.
	acc.Assertions = append(acc.Assertions, add.Assertions...)

	return acc, nil
}

func mergeScalar(acc, add, field, accOrigin, addOrigin string) (string, error) {
	switch {
	case add == "":
		return acc, nil
	case acc == "", acc == add:
		return add, nil
	default:
		return acc, globalConflict(field, "", accOrigin, addOrigin)
	}
}

func mergeStringMap(acc, add map[string]string, field, accOrigin, addOrigin string) (map[string]string, error) {
	if len(add) == 0 {
		return acc, nil
	}
	if acc == nil {
		acc = make(map[string]string, len(add))
	}
	for _, k := range sortedStringMapKeys(add) {
		if existing, ok := acc[k]; ok && existing != add[k] {
			return acc, globalConflict(field, k, accOrigin, addOrigin)
		}
		acc[k] = add[k]
	}
	return acc, nil
}

func mergeAnyMap(acc, add map[string]any, field, accOrigin, addOrigin string) (map[string]any, error) {
	if len(add) == 0 {
		return acc, nil
	}
	if acc == nil {
		acc = make(map[string]any, len(add))
	}
	for _, k := range sortedAnyMapKeys(add) {
		if existing, ok := acc[k]; ok && fmt.Sprintf("%v", existing) != fmt.Sprintf("%v", add[k]) {
			return acc, globalConflict(field, k, accOrigin, addOrigin)
		}
		acc[k] = add[k]
	}
	return acc, nil
}

func globalConflict(field, key, accOrigin, addOrigin string) error {
	where := field
	if key != "" {
		where = field + "." + key
	}
	if accOrigin == "" {
		accOrigin = "an earlier source"
	}
	return fmt.Errorf(
		"validationTests %s: %s is set to different values by %s and %s: "+
			"the baseline is shared by every test, so one value would silently override the other",
		GlobalValidationTestName, where, accOrigin, addOrigin)
}

func appendUnique(acc, add []string) []string {
	seen := make(map[string]bool, len(acc))
	for _, v := range acc {
		seen[v] = true
	}
	for _, v := range add {
		if !seen[v] {
			seen[v] = true
			acc = append(acc, v)
		}
	}
	return acc
}

// The map iteration order below is fixed so that accumulated fixtures — and
// therefore the rendered config a test asserts on — do not vary between runs.

func sortedTestNames(m map[string]ValidationTest) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedFixtureKinds(m map[string][]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
