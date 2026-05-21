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

// Package typebootstrap orchestrates the typed-watched-resources
// pipeline at controller startup. It's the only place that imports
// from all three sides:
//
//   - pkg/k8s/schemafetcher  — fetches schemas from the cluster
//   - pkg/k8s/typegen        — translates schemas into reflect.Type
//   - pkg/templating         — receives the resulting types as engine
//     globals
//
// Rule 1 of arch-go.yml ("controller can import everything") covers
// this; no other package may do the same coupling.
//
// # The pipeline
//
// For each watched resource the operator declared in
// HAProxyTemplateConfig.spec.watchedResources, the bootstrap:
//
//  1. Resolves the (Group, Version, Resources-plural) triple to a
//     full GVK with Kind via a [GVKResolver] (the production wiring
//     backs this with a RESTMapper).
//  2. Calls [schemafetcher.Fetcher].Fetch to obtain the resource's
//     OpenAPI v3 schema.
//  3. Hands the schema to a [typegen.Converter] (configured with
//     the merged global + per-resource IgnoreFields list) to
//     produce a reflect.Type.
//  4. Stores the resulting type under the resource's user-defined
//     name (the same name templates use to reach
//     `resources.<name>.List()`).
//
// The bootstrap is fail-closed: any single resource whose schema
// can't be fetched or converted aborts the run with a hard error
// naming the failing resource. Template authors using typed
// access (gw.Spec.X, route.Status.Y) need the guarantee that
// every declared watched resource resolved to its real schema;
// the previous fail-open-to-envelope path produced silently
// broken render states with no automatic recovery. Result.Errors
// still records the per-resource cause for debug surfaces.
//
// # The handoff to templating
//
// [BuildEngineDeclarations] turns the typed-resource map into the
// shape pkg/templating's NewScriggoWithDeclarations expects —
// `map[string]any` of typed-nil pointers. Templates compile against
// those declared types; the actual values arrive at render time
// from the StoreWrapper (Phase 5, not built yet).
//
// # Why a Resource list rather than the CRD config directly
//
// typebootstrap deliberately doesn't import the v1alpha1 config
// CRD types. The caller (pkg/controller's runIteration) translates
// HAProxyTemplateConfig.spec.watchedResources into the package's
// own [Resource] slice. This keeps typebootstrap testable without
// the apis package and lets the input shape evolve independently
// of the CRD schema's churn.
package typebootstrap
