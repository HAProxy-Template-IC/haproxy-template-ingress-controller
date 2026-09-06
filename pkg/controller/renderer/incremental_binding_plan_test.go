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

func TestIncrementalBindingPlanRejectsMultipleActiveDeriveOwners(t *testing.T) {
	tests := map[string]struct {
		first        incrementalComponent
		firstBinding incrementalBinding
		second       incrementalComponent
		secondBind   incrementalBinding
	}{
		"static sources": {
			first:        incrementalComponent{name: "first", source: "routes", deriveResource: true},
			firstBinding: staticIncrementalBinding("first", "routes"),
			second:       incrementalComponent{name: "second", source: "routes", deriveResource: true},
			secondBind:   staticIncrementalBinding("second", "routes"),
		},
		"static and dynamic sources": {
			first:        incrementalComponent{name: "static", source: "routes", deriveResource: true},
			firstBinding: staticIncrementalBinding("static", "routes"),
			second:       incrementalComponent{name: "dynamic", deriveResource: true},
			secondBind:   incrementalBinding{component: "dynamic", source: "routes", props: []byte("{}")},
		},
		"dynamic sources": {
			first:        incrementalComponent{name: "first-dynamic", deriveResource: true},
			firstBinding: incrementalBinding{component: "first-dynamic", source: "routes", props: []byte("{}")},
			second:       incrementalComponent{name: "second-dynamic", deriveResource: true},
			secondBind:   incrementalBinding{component: "second-dynamic", source: "routes", props: []byte("{}")},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			plan := newIncrementalBindingPlan()
			require.NoError(t, plan.addComponentBindings(&test.first, []incrementalBinding{test.firstBinding}))
			err := plan.addComponentBindings(&test.second, []incrementalBinding{test.secondBind})
			require.ErrorContains(t, err, `watched resource "routes" has multiple active deriveResource components`)
		})
	}
}

func TestEqualIncrementalBindingPlanMatchesDeepEqual(t *testing.T) {
	activation, err := templating.CompileExistenceJSONPath("spec.rules")
	require.NoError(t, err)
	component := incrementalComponent{
		name: "backend", entryPoint: "backend.cfg", source: "routes", root: "haproxy.cfg", group: "backends",
		consumes: []string{"services"}, optionalConsumes: []string{"endpoints"},
		activationPaths: []templating.ExistenceJSONPath{activation}, resourceProjection: true, publishValue: true,
	}
	plan := newIncrementalBindingPlan()
	plan.bindings = []incrementalBinding{{
		component: "backend", source: "routes", props: []byte(`{"cell":"a"}`),
		projection: &incrementalResourceProjection{Cell: "a", Key: "k", Keys: []string{"k"}, digest: "d", identity: "i"},
	}}
	plan.byComponent["backend"] = plan.bindings
	plan.bySource["routes"] = []incrementalComponent{component}
	plan.projectionSources["routes"] = struct{}{}
	plan.props["backend"] = []byte(`{}`)
	plan.owners["routes"] = component

	sealed := cloneIncrementalBindingPlan(plan)
	require.True(t, reflect.DeepEqual(plan, sealed))
	require.True(t, equalIncrementalBindingPlan(plan, sealed))

	mutations := map[string]func(*incrementalBindingPlan){
		"binding props":        func(p *incrementalBindingPlan) { p.bindings[0].props[2] = 'x' },
		"binding projection":   func(p *incrementalBindingPlan) { p.bindings[0].projection.Keys[0] = "x" },
		"component consumes":   func(p *incrementalBindingPlan) { p.bySource["routes"][0].consumes[0] = "x" },
		"component activation": func(p *incrementalBindingPlan) { p.bySource["routes"][0].activationPaths = nil },
		"projection source":    func(p *incrementalBindingPlan) { p.projectionSources["x"] = struct{}{} },
		"props":                func(p *incrementalBindingPlan) { p.props["backend"] = nil },
		"owner":                func(p *incrementalBindingPlan) { delete(p.owners, "routes") },
		"nil versus empty":     func(p *incrementalBindingPlan) { p.bindings[0].projection.Keys = []string{} },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := cloneIncrementalBindingPlan(plan)
			mutate(mutated)
			if name == "nil versus empty" {
				sealed.bindings[0].projection.Keys = nil
				defer func() { sealed.bindings[0].projection.Keys = []string{"k"} }()
			}
			require.Equal(t, reflect.DeepEqual(mutated, sealed), equalIncrementalBindingPlan(mutated, sealed))
			require.False(t, equalIncrementalBindingPlan(mutated, sealed))
		})
	}
}

// Unkeyed literals stop compiling when a field is added, so the typed
// comparators cannot silently skip a new field.
var (
	_ = incrementalBindingPlan{nil, nil, nil, nil, nil, nil}
	_ = incrementalBinding{"", "", nil, nil}
	_ = incrementalResourceProjection{"", "", nil, "", "", ""}
	_ = incrementalComponent{"", "", "", "", "", nil, nil, nil, false, false, false, false, false, false}
)
