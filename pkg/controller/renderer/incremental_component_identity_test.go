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

package renderer

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalComponentsEqualMatchesDeepEqual(t *testing.T) {
	activationPath, err := templating.CompileExistenceJSONPath("metadata.name")
	require.NoError(t, err)
	otherActivationPath, err := templating.CompileExistenceJSONPath("metadata.namespace")
	require.NoError(t, err)
	base := incrementalComponent{
		name:               "component",
		entryPoint:         "library/component",
		source:             "routes",
		root:               "route-root",
		group:              "routing",
		consumes:           []string{"published-a", "published-b"},
		optionalConsumes:   []string{"optional"},
		activationPaths:    []templating.ExistenceJSONPath{activationPath},
		resourceProjection: true,
		deriveResource:     true,
		recordEvent:        true,
		backendPlan:        true,
		publishValue:       true,
		statusPatch:        true,
	}
	components := []incrementalComponent{
		{},
		base,
		base,
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.name = "other" }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.entryPoint = "other" }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.source = "other" }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.root = "other" }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.group = "other" }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.consumes = nil }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.consumes = []string{} }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.consumes = []string{"published-b", "published-a"} }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.optionalConsumes = nil }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.optionalConsumes = []string{} }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.optionalConsumes = []string{"other"} }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.activationPaths = nil }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.resourceProjection = false }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.activationPaths = []templating.ExistenceJSONPath{} }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) {
			value.activationPaths = []templating.ExistenceJSONPath{otherActivationPath}
		}),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.deriveResource = false }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.recordEvent = false }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.backendPlan = false }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.publishValue = false }),
		withIncrementalComponentChange(&base, func(value *incrementalComponent) { value.statusPatch = false }),
	}
	for leftIndex, left := range components {
		for rightIndex, right := range components {
			require.Equal(
				t,
				reflect.DeepEqual(left, right),
				incrementalComponentsEqual(&left, &right),
				"components %d and %d",
				leftIndex,
				rightIndex,
			)
		}
	}
}

func withIncrementalComponentChange(
	base *incrementalComponent,
	change func(*incrementalComponent),
) incrementalComponent {
	changed := *base
	change(&changed)
	return changed
}

var incrementalComponentEqualSink bool

func BenchmarkIncrementalComponentsEqual(b *testing.B) {
	path, err := templating.CompileExistenceJSONPath("metadata.annotations['example.test/key']")
	require.NoError(b, err)
	left := incrementalComponent{
		name: "component", entryPoint: "library/component", source: "routes", root: "route-root", group: "routing",
		consumes: []string{"a", "b"}, optionalConsumes: []string{"c"},
		activationPaths: []templating.ExistenceJSONPath{path}, deriveResource: true,
		recordEvent: true, backendPlan: true, publishValue: true, statusPatch: true,
	}
	right := left
	b.Run("exact", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			incrementalComponentEqualSink = incrementalComponentsEqual(&left, &right)
		}
	})
	b.Run("reflect", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			incrementalComponentEqualSink = reflect.DeepEqual(left, right)
		}
	})
}
