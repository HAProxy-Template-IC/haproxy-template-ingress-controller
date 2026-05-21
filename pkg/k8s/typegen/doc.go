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

// Package typegen translates OpenAPI v3 schemas into runtime [reflect.Type]
// values that Scriggo templates can consume with full compile-time field
// safety.
//
// The K8s ecosystem already exposes schemas for every resource we watch —
// core types via the cluster's OpenAPI v3 endpoint, CRDs via their own
// .spec.versions[].schema.openAPIV3Schema. None of the existing tooling
// (client-gen, controller-gen, openapi-gen) generates Go types at runtime
// though — they all run at code-generation time. This package fills that
// gap.
//
// # The translation rules
//
//   - object  → reflect.StructOf with one StructField per Properties entry;
//     field Name is the JSON name capitalised so reflect can
//     see it (Go's exported-identifier rule), plus a
//     `json:"<original-name>"` tag so encoding/json can still
//     unmarshal an unstructured map into the generated type.
//   - array   → reflect.SliceOf the element schema's generated type.
//   - string  → reflect.TypeOf("").
//   - integer → reflect.TypeOf(int64(0)). K8s schemas don't distinguish
//     int32 from int64 reliably; int64 covers both and matches
//     what unstructured.Unstructured produces.
//   - boolean → reflect.TypeOf(false).
//   - number  → reflect.TypeOf(float64(0)).
//   - $ref    → resolved against the spec's Components.Schemas, with the
//     resulting type memoised so recursive refs terminate.
//   - allOf / oneOf / anyOf, schema with no type, schema with
//     x-kubernetes-preserve-unknown-fields=true, or AdditionalProperties
//     pointing to a true-bool (free-form map) all degrade to interface{}
//     (any). Templates fall back to dig() for those subtrees.
//
// # Why types and not raw maps
//
// Scriggo type-checks field access against registered types at template
// compile time. A typo in `gw.Metadata.Naamespace` is caught when the
// controller boots, not at the next reconcile when the rendered output is
// missing a frontend block. The map-based shape we have today (every K8s
// object as map[string]any) can't be type-checked because Scriggo rejects
// field access on `any` — that's why HAPTIC templates universally use
// dig(), and why we ended up shipping digstr/digint/digbool to collapse
// the boilerplate. Generating real Go types from OpenAPI lets templates
// drop the navigation helpers entirely for the typed envelope, while
// still keeping dig() working on the Spec/Status subtrees (which are
// resource-specific and may still carry preserve-unknown subtrees).
//
// # Cycle handling
//
// Real K8s schemas mostly aren't recursive — RawExtension is the usual
// suspect and it's already preserve-unknown so it degrades to any. We
// still cap recursion depth defensively, returning interface{} when the
// limit is hit. The depth limit only kicks in when a schema $ref-chains
// further than [DefaultMaxDepth] without resolving — a real cycle would
// hit this. Constructive schemas terminate before it because the type
// cache is keyed by $ref and the second visit returns the cached type.
//
// # What this package does NOT do
//
// Schema fetching lives separately in pkg/k8s/schemafetcher — the
// fetcher does cluster I/O so it belongs on the K8s integration
// layer, not under templating (which is meant to stay pure; see
// arch-go.yml Rule 7).
// Wrapping a map[string]any into an instance of a generated type lives in
// pkg/k8s/typegen.WrapInto (this package, separate file). The
// controller-side bootstrap that glues fetcher+converter+Scriggo together
// lives in pkg/controller. The package itself has no I/O.
package typegen
