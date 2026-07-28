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
	"strings"
	"testing"
)

func src(origin string, tests map[string]ValidationTest) ValidationTestSource {
	return ValidationTestSource{Origin: origin, Tests: tests}
}

// unionCase drives both union tables. The runner lives here rather than being
// repeated per table so each test stays a list of cases.
type unionCase struct {
	name    string
	sources []ValidationTestSource
	wantErr string
	check   func(t *testing.T, got map[string]ValidationTest)
}

func runUnionCases(t *testing.T, cases []unionCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UnionValidationTests(tt.sources)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got none (union=%#v)", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func TestUnionValidationTests(t *testing.T) {
	tests := []unionCase{
		{
			name:    "no sources yields an empty map, not nil",
			sources: nil,
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				if got == nil {
					t.Fatal("got nil map; callers range over this and a nil map hides the zero-test case")
				}
				if len(got) != 0 {
					t.Fatalf("got %d tests, want 0", len(got))
				}
			},
		},
		{
			name: "tests from separate sources combine",
			sources: []ValidationTestSource{
				src("config", map[string]ValidationTest{"a": {Description: "A"}}),
				src("tests-obj", map[string]ValidationTest{"b": {Description: "B"}}),
			},
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				if len(got) != 2 || got["a"].Description != "A" || got["b"].Description != "B" {
					t.Fatalf("got %#v", got)
				}
			},
		},
		{
			name: "duplicate name across sources is an error naming both",
			sources: []ValidationTestSource{
				src("HAProxyTemplateConfig/cfg", map[string]ValidationTest{"dup": {Description: "first"}}),
				src("HAProxyValidationTests/extra", map[string]ValidationTest{"dup": {Description: "second"}}),
			},
			wantErr: "defined by both HAProxyTemplateConfig/cfg and HAProxyValidationTests/extra",
		},
		{
			name: "duplicate within one source is not a collision",
			sources: []ValidationTestSource{
				src("config", map[string]ValidationTest{"a": {Description: "A"}, "b": {Description: "B"}}),
			},
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				if len(got) != 2 {
					t.Fatalf("got %d tests, want 2", len(got))
				}
			},
		},
	}

	runUnionCases(t, tests)
}

func TestUnionValidationTests_GlobalBaseline(t *testing.T) {
	tests := []unionCase{
		{
			name: "_global fixtures accumulate instead of replacing",
			sources: []ValidationTestSource{
				src("base", map[string]ValidationTest{GlobalValidationTestName: {
					Fixtures: map[string][]any{"services": {"svc-base"}},
				}}),
				src("ssl", map[string]ValidationTest{GlobalValidationTestName: {
					Fixtures: map[string][]any{"secrets": {"sec-ssl"}},
				}}),
				src("extra", map[string]ValidationTest{GlobalValidationTestName: {
					Fixtures: map[string][]any{"services": {"svc-extra"}},
				}}),
			},
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				g := got[GlobalValidationTestName]
				if len(g.Fixtures["services"]) != 2 {
					t.Fatalf("services fixtures = %v, want both base and extra — a replace here silently drops a library's baseline", g.Fixtures["services"])
				}
				if len(g.Fixtures["secrets"]) != 1 {
					t.Fatalf("secrets fixtures = %v, want ssl's", g.Fixtures["secrets"])
				}
			},
		},
		{
			name: "_global is never reported as a duplicate",
			sources: []ValidationTestSource{
				src("a", map[string]ValidationTest{GlobalValidationTestName: {Description: "x"}}),
				src("b", map[string]ValidationTest{GlobalValidationTestName: {Description: "y"}}),
			},
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				if _, ok := got[GlobalValidationTestName]; !ok {
					t.Fatal("_global missing")
				}
			},
		},
		{
			name: "_global requires and requiresFields union without duplicates",
			sources: []ValidationTestSource{
				src("a", map[string]ValidationTest{GlobalValidationTestName: {
					Requires: []string{"gateways", "ingresses"}, RequiresFields: []string{"spec.x"},
				}}),
				src("b", map[string]ValidationTest{GlobalValidationTestName: {
					Requires: []string{"ingresses", "httproutes"}, RequiresFields: []string{"spec.x", "spec.y"},
				}}),
			},
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				g := got[GlobalValidationTestName]
				if len(g.Requires) != 3 {
					t.Fatalf("requires = %v, want 3 unique", g.Requires)
				}
				if len(g.RequiresFields) != 2 {
					t.Fatalf("requiresFields = %v, want 2 unique", g.RequiresFields)
				}
			},
		},
		{
			name: "_global extraContext merges disjoint keys",
			sources: []ValidationTestSource{
				src("a", map[string]ValidationTest{GlobalValidationTestName: {ExtraContext: map[string]any{"x": 1}}}),
				src("b", map[string]ValidationTest{GlobalValidationTestName: {ExtraContext: map[string]any{"y": 2}}}),
			},
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				g := got[GlobalValidationTestName]
				if len(g.ExtraContext) != 2 {
					t.Fatalf("extraContext = %v, want both keys", g.ExtraContext)
				}
			},
		},
		{
			name: "_global extraContext conflict on the same key is an error",
			sources: []ValidationTestSource{
				src("a", map[string]ValidationTest{GlobalValidationTestName: {ExtraContext: map[string]any{"mode": "http"}}}),
				src("b", map[string]ValidationTest{GlobalValidationTestName: {ExtraContext: map[string]any{"mode": "tcp"}}}),
			},
			wantErr: "extraContext.mode is set to different values",
		},
		{
			name: "_global identical scalar from two sources is not a conflict",
			sources: []ValidationTestSource{
				src("a", map[string]ValidationTest{GlobalValidationTestName: {CurrentConfig: "global\n"}}),
				src("b", map[string]ValidationTest{GlobalValidationTestName: {CurrentConfig: "global\n"}}),
			},
			check: func(t *testing.T, got map[string]ValidationTest) {
				t.Helper()
				if got[GlobalValidationTestName].CurrentConfig != "global\n" {
					t.Fatal("identical values should merge silently")
				}
			},
		},
		{
			name: "_global currentConfig conflict is an error",
			sources: []ValidationTestSource{
				src("a", map[string]ValidationTest{GlobalValidationTestName: {CurrentConfig: "one"}}),
				src("b", map[string]ValidationTest{GlobalValidationTestName: {CurrentConfig: "two"}}),
			},
			wantErr: "currentConfig is set to different values",
		},
		{
			name: "_global currentFiles conflict on the same filename is an error",
			sources: []ValidationTestSource{
				src("a", map[string]ValidationTest{GlobalValidationTestName: {CurrentFiles: map[string]string{"f": "1"}}}),
				src("b", map[string]ValidationTest{GlobalValidationTestName: {CurrentFiles: map[string]string{"f": "2"}}}),
			},
			wantErr: "currentFiles.f is set to different values",
		},
	}

	runUnionCases(t, tests)
}

// The runner reads _global once, so accumulated fixtures must land in a stable
// order or the rendered config a test asserts on varies between runs.
func TestUnionValidationTests_FixtureOrderIsDeterministic(t *testing.T) {
	build := func() []ValidationTestSource {
		return []ValidationTestSource{
			src("a", map[string]ValidationTest{GlobalValidationTestName: {
				Fixtures: map[string][]any{"services": {"a1", "a2"}, "secrets": {"as"}},
			}}),
			src("b", map[string]ValidationTest{GlobalValidationTestName: {
				Fixtures: map[string][]any{"services": {"b1"}, "secrets": {"bs"}},
			}}),
		}
	}

	first, err := UnionValidationTests(build())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := first[GlobalValidationTestName].Fixtures["services"]

	for i := 0; i < 50; i++ {
		got, err := UnionValidationTests(build())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		gotServices := got[GlobalValidationTestName].Fixtures["services"]
		if len(gotServices) != len(want) {
			t.Fatalf("run %d: length changed: %v vs %v", i, gotServices, want)
		}
		for j := range want {
			if gotServices[j] != want[j] {
				t.Fatalf("run %d: order changed at %d: %v vs %v", i, j, gotServices, want)
			}
		}
	}
}

// Accumulating into a source's own map would corrupt the caller's object, which
// for the live path is a cached informer item shared with every other consumer.
func TestUnionValidationTests_DoesNotMutateSources(t *testing.T) {
	a := map[string]ValidationTest{GlobalValidationTestName: {
		Fixtures: map[string][]any{"services": {"only-a"}},
	}}
	b := map[string]ValidationTest{GlobalValidationTestName: {
		Fixtures: map[string][]any{"services": {"only-b"}},
	}}

	if _, err := UnionValidationTests([]ValidationTestSource{src("a", a), src("b", b)}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := len(a[GlobalValidationTestName].Fixtures["services"]); got != 1 {
		t.Fatalf("source a was mutated: services now has %d entries, want 1", got)
	}
	if got := len(b[GlobalValidationTestName].Fixtures["services"]); got != 1 {
		t.Fatalf("source b was mutated: services now has %d entries, want 1", got)
	}
}
