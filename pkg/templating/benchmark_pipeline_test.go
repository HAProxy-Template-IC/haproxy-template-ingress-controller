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

package templating

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// benchFixture mirrors the pod-names workload: EndpointSlices carrying
// endpoints that each carry addresses. 500 endpoints is the scale the
// bundled `map-pod-names-500-endpoints` snippet is named for.
func benchFixture(slices, perSlice, addrs int) []pipelineSlice {
	out := make([]pipelineSlice, 0, slices)
	for s := range slices {
		eps := make([]pipelineEP, 0, perSlice)
		for e := range perSlice {
			a := make([]string, 0, addrs)
			for i := range addrs {
				a = append(a, fmt.Sprintf("10.%d.%d.%d", s%250, e%250, i%250))
			}
			name := fmt.Sprintf("pod-%d-%d", s, e)
			if e%10 == 0 {
				name = "" // the empty-targetRef case the snippet filters out
			}
			eps = append(eps, pipelineEP{TargetRef: pipelineRef{Name: name}, Addresses: a, Ready: true})
		}
		out = append(out, pipelineSlice{Endpoints: eps})
	}
	return out
}

// The hand-written loop this replaced: VM bytecode, no reflection.
const benchLoopTemplate = `{%%
  var seen = map[string]bool{}
  var lines []string
  for _, s := range eps {
    for _, e := range s.Endpoints {
      if e.TargetRef.Name == "" { continue }
      for _, addr := range e.Addresses {
        if seen[addr] { continue }
        seen[addr] = true
        lines = append(lines, addr + " " + e.TargetRef.Name)
      }
    }
  }
%%}{{ len(lines) }}`

// The pipeline equivalent: one reflect.Call per element per stage.
const benchPipelineTemplate = `{%%
  var lines = eps |
    flat_map(func(s Slice) []EP { return s.Endpoints }) |
    reject(func(e EP) bool { return e.TargetRef.Name == "" }) |
    flat_map(func(e EP) []string { return e.Addresses }) |
    unique()
%%}{{ len(lines) }}`

// benchNativePipelineTemplate is the same work as benchPipelineTemplate but
// hoists the closures into variables, which makes the chain unlowerable
// (compile-time lowering requires literal closures). It measures what an
// author pays when their spelling misses the optimisation.
const benchNativePipelineTemplate = `{%%
  var f = func(s Slice) []EP { return s.Endpoints }
  var g = func(e EP) bool { return e.TargetRef.Name == "" }
  var h = func(e EP) []string { return e.Addresses }
  var lines = eps | flat_map(f) | reject(g) | flat_map(h) | unique()
%%}{{ len(lines) }}`

func benchRender(b *testing.B, tpl string, data []pipelineSlice) {
	b.Helper()
	engine, err := New(map[string]string{"t": tpl}, &Options{
		Declarations: map[string]any{
			"eps":   (*[]pipelineSlice)(nil),
			"Slice": reflect.TypeOf(pipelineSlice{}),
			"EP":    reflect.TypeOf(pipelineEP{}),
		},
	})
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	ctx := context.Background()
	vars := map[string]any{"eps": data}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := engine.Render(ctx, "t", vars); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}

func BenchmarkPipelineVsLoop(b *testing.B) {
	sizes := []struct {
		name                    string
		slices, perSlice, addrs int
	}{
		{"50endpoints", 10, 5, 2},
		{"500endpoints", 50, 10, 2},
		{"5000endpoints", 250, 20, 2},
	}
	for _, s := range sizes {
		data := benchFixture(s.slices, s.perSlice, s.addrs)
		b.Run(s.name+"/loop", func(b *testing.B) { benchRender(b, benchLoopTemplate, data) })
		b.Run(s.name+"/pipeline", func(b *testing.B) { benchRender(b, benchPipelineTemplate, data) })
		b.Run(s.name+"/pipeline-native", func(b *testing.B) { benchRender(b, benchNativePipelineTemplate, data) })
	}
}

// BenchmarkPipelineStages isolates per-stage cost so a regression can be
// attributed to one helper rather than to the chain as a whole.
func BenchmarkPipelineStages(b *testing.B) {
	data := benchFixture(50, 10, 2)
	stages := map[string]string{
		"flat_map": `{%% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) %%}{{ len(out) }}`,
		"reject": `{%% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
			`reject(func(e EP) bool { return e.TargetRef.Name == "" }) %%}{{ len(out) }}`,
		"map": `{%% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
			`map(func(e EP) string { return e.TargetRef.Name }) %%}{{ len(out) }}`,
		"unique_by_closure": `{%% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
			`unique_by(func(e EP) string { return e.TargetRef.Name }) %%}{{ len(out) }}`,
		"unique_by_path": `{%% var out = eps | flat_map(func(s Slice) []EP { return s.Endpoints }) | ` +
			`unique_by("targetRef.name") %%}{{ len(out) }}`,
	}
	for name, tpl := range stages {
		b.Run(name, func(b *testing.B) { benchRender(b, tpl, data) })
	}
}
