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

package schemafetcher

import (
	"strings"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// fieldProbeMaxDepth bounds the schema walk against pathological
// $ref cycles. Real K8s schemas are shallow (a dot path has one
// property hop per segment plus a few ref/allOf indirections), so
// the limit only fires on malformed input.
const fieldProbeMaxDepth = 64

// SchemaHasField reports whether the OpenAPI v3 schema contains the
// dot-separated field path (e.g. "spec.rules.filters.cors"). The walk
// is purely structural and resource-agnostic — the path comes from
// configuration, the schema from the cluster or a schema directory:
//
//   - Array levels are descended transparently: "spec.rules.filters"
//     matches "spec.properties.rules.items.properties.filters".
//   - `allOf` branches and `$ref` entries (resolved through the
//     supplied components map, aggregated-OpenAPI style) are followed.
//   - A subtree annotated `x-kubernetes-preserve-unknown-fields: true`,
//     or one accepting free-form `additionalProperties`, is treated as
//     containing ANY field: the apiserver would persist the field
//     there, so it must not count as absent.
//
// components may be nil (CRD-shaped schemas inline everything); an
// unresolvable $ref then simply doesn't contribute properties.
func SchemaHasField(sch *spec.Schema, components map[string]spec.Schema, dotPath string) bool {
	if sch == nil {
		return false
	}
	segments := strings.Split(dotPath, ".")
	current := []*spec.Schema{sch}
	for _, segment := range segments {
		if segment == "" {
			return false
		}
		next, wildcard := lookupProperty(current, components, segment)
		if wildcard {
			return true
		}
		if len(next) == 0 {
			return false
		}
		current = next
	}
	return true
}

// lookupProperty resolves the given property name against every
// candidate schema (descending into arrays, refs, and allOf branches
// first). It returns the schemas the property maps to, or wildcard
// true when any candidate accepts arbitrary fields at this level.
func lookupProperty(candidates []*spec.Schema, components map[string]spec.Schema, name string) (next []*spec.Schema, wildcard bool) {
	for _, cand := range candidates {
		flattened, wild := flatten(cand, components, 0)
		if wild {
			return nil, true
		}
		for _, s := range flattened {
			if prop, ok := s.Properties[name]; ok {
				next = append(next, &prop)
				continue
			}
			// A typed additionalProperties map contains every key;
			// the segment resolves to the value schema.
			if len(s.Properties) == 0 && s.AdditionalProperties != nil {
				switch {
				case s.AdditionalProperties.Schema != nil:
					next = append(next, s.AdditionalProperties.Schema)
				case s.AdditionalProperties.Allows:
					return nil, true
				}
			}
		}
	}
	return next, false
}

// flatten normalises a schema node into the list of object schemas
// whose Properties are directly inspectable: it descends through
// array items, resolves $refs via the components map, and expands
// allOf branches. wildcard is true when the node (or any node it
// expands to) is a preserve-unknown-fields subtree.
func flatten(sch *spec.Schema, components map[string]spec.Schema, depth int) (out []*spec.Schema, wildcard bool) {
	switch {
	case sch == nil || depth > fieldProbeMaxDepth:
		return nil, false
	case hasPreserveUnknownFields(sch):
		return nil, true
	case sch.Ref.String() != "":
		return flattenRef(sch.Ref.String(), components, depth)
	case sch.Items != nil && (sch.Items.Schema != nil || len(sch.Items.Schemas) > 0):
		return flattenItems(sch.Items, components, depth)
	case len(sch.AllOf) > 0:
		return flattenAllOf(sch, components, depth)
	default:
		return []*spec.Schema{sch}, false
	}
}

// flattenRef resolves a $ref through the components map. CRD schemas never
// carry refs (components is nil then — the ref contributes nothing);
// aggregated OpenAPI v3 wraps shared types this way.
func flattenRef(ref string, components map[string]spec.Schema, depth int) ([]*spec.Schema, bool) {
	const prefix = "#/components/schemas/"
	if components != nil && strings.HasPrefix(ref, prefix) {
		if target, ok := components[ref[len(prefix):]]; ok {
			return flatten(&target, components, depth+1)
		}
	}
	return nil, false
}

// flattenItems descends into an array's item schema(s) transparently.
func flattenItems(items *spec.SchemaOrArray, components map[string]spec.Schema, depth int) (out []*spec.Schema, wildcard bool) {
	if items.Schema != nil {
		return flatten(items.Schema, components, depth+1)
	}
	for i := range items.Schemas {
		flat, wild := flatten(&items.Schemas[i], components, depth+1)
		if wild {
			return nil, true
		}
		out = append(out, flat...)
	}
	return out, false
}

// flattenAllOf expands allOf branches: every branch may contribute
// properties (K8s uses the single-element `allOf: [$ref: X]` form;
// multi-element is a conjunction, so a field present in any branch is
// present).
func flattenAllOf(sch *spec.Schema, components map[string]spec.Schema, depth int) (out []*spec.Schema, wildcard bool) {
	out = append(out, sch)
	for i := range sch.AllOf {
		flat, wild := flatten(&sch.AllOf[i], components, depth+1)
		if wild {
			return nil, true
		}
		out = append(out, flat...)
	}
	return out, false
}

// hasPreserveUnknownFields reports whether the schema is annotated
// with `x-kubernetes-preserve-unknown-fields: true`. Both bool true
// and string "true" appear in the wild (same tolerance as
// pkg/k8s/typegen's hasPreserveUnknown).
func hasPreserveUnknownFields(sch *spec.Schema) bool {
	v, ok := sch.Extensions["x-kubernetes-preserve-unknown-fields"]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true"
	default:
		return false
	}
}
