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

package typebootstrap

import (
	"reflect"
)

// BuildEngineDeclarations turns a Bootstrap [Result] into the
// `additionalDeclarations map[string]any` shape that
// templating.NewScriggoWithDeclarations expects.
//
// The package-doc convention for typed runtime variables is a
// typed-nil pointer (see pkg/templating/globals.go's buildScriggoGlobals
// where every runtime variable like pathResolver, fileRegistry, etc.
// is declared as `(*T)(nil)`). Scriggo reads the *type* off that nil
// pointer at compile time and pairs it with the actual value the
// caller passes via the render context. We follow the same pattern
// for typed-watched-resource globals — there's a slice of pointers
// to the generated type per watched resource, declared as
// (*[]*Generated)(nil) here, populated by the StoreWrapper at render
// time (Phase 5, not built yet).
//
// The slice-of-pointers shape mirrors what
// pkg/k8s/typegen.WrapSlice produces from the StoreWrapper's
// snapshot. Keeping the declared shape in lockstep with the
// runtime-produced shape is what lets templates write
//
//	{%- for _, gw := range resources.gateways.List() %}
//	  {{ gw.Metadata.Namespace }}
//	{%- end %}
//
// against the typed view.
//
// Resources that failed bootstrap (present in result.Errors but
// absent from result.Types) are skipped — they fall back to the
// generic `resources["<name>"]` map-based access that the existing
// ResourceStore interface already provides. The chart still renders
// for those; the typed shortcut just isn't available.
//
// The returned map can be merged with other domain-specific
// declarations by the caller before being handed to the engine.
// Bootstrap doesn't claim ownership of the whole declarations map
// — it only contributes its own typed-resource entries.
func BuildEngineDeclarations(result *Result) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(result.Types))
	for name, t := range result.Types {
		// Declared shape: *[]*Generated.
		//   Outer *  — Scriggo's typed-nil-pointer convention for
		//              runtime variables (see globals.go).
		//   []*     — slice of pointers-to-Generated; what
		//              WrapSlice produces at snapshot-load time.
		//   *Gen    — pointer-to-Generated so range loops can
		//              dot-access fields via field promotion;
		//              templates write `gw.Metadata.Name` directly
		//              without dereference syntax.
		sliceType := reflect.SliceOf(reflect.PointerTo(t))
		ptrType := reflect.PointerTo(sliceType)
		out[name] = reflect.Zero(ptrType).Interface()
	}
	return out
}
