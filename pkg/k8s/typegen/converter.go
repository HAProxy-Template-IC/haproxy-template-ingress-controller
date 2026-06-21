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

package typegen

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"k8s.io/client-go/util/jsonpath"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// DefaultMaxDepth is the recursion cap the [Converter] uses when no
// explicit MaxDepth is set on the receiver. Chosen to comfortably exceed
// every nested schema we've seen in the Kubernetes core API and the
// Gateway API CRDs (deepest path observed is ~7 — HTTPRouteSpec.Rules[].
// Matches[].Headers[]). Anything deeper than this is almost certainly a
// cycle we can't statically prove won't terminate, and falling back to
// any is the safe choice.
const DefaultMaxDepth = 32

// anyType is the canonical reflect.Type for an empty interface. Cached
// so the conversion fast-paths can return it without re-running
// reflect.TypeOf on every miss.
var anyType = reflect.TypeOf((*any)(nil)).Elem()

// preserveUnknownExt is the OpenAPI extension Kubernetes uses to mark
// subtrees whose shape is intentionally unconstrained (RawExtension,
// embedded objects, JSON-blob spec fields). Whenever we see it set to a
// truthy value on a Properties entry, the entry's generated type
// collapses to any so dig() can still navigate at render time.
const preserveUnknownExt = "x-kubernetes-preserve-unknown-fields"

// JSON Schema type keywords we recognise. Constants keep the converter
// switch readable and let lint enforce that we touch every supported
// type when we add new ones.
const (
	typeObject  = "object"
	typeArray   = "array"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
	typeNumber  = "number"
)

// Converter translates an OpenAPI v3 [spec.Schema] tree into a runtime
// [reflect.Type] via [reflect.StructOf]. Construct one per logical batch
// of schemas (e.g. one per controller boot) so the $ref cache stays
// scoped to a coherent set of definitions — sharing a converter across
// unrelated specs would muddle the cache keys and risk cross-spec ref
// hijacking on identically-named refs (e.g. /v1.ObjectMeta vs
// /v1beta1.ObjectMeta).
//
// The zero value is not usable; call [NewConverter] instead.
type Converter struct {
	// components is the spec's Components.Schemas map, used to resolve
	// $ref pointers like "#/components/schemas/io.k8s.api.networking.v1.Ingress".
	// nil is legal and means "no ref resolution" — useful for tests that
	// feed inline schemas only.
	components map[string]spec.Schema

	// typeCache memoises generated types by $ref string. Both protects
	// against quadratic blow-up on shared subschemas (every K8s object
	// references the same ObjectMeta, ListMeta, OwnerReference, ...) and
	// makes recursive refs terminate — the second visit returns the
	// cached type rather than recursing.
	typeCache map[string]reflect.Type

	// MaxDepth caps how deep Convert will recurse before returning any.
	// Defaults to [DefaultMaxDepth] when unset. Exposed mainly so tests
	// can force a shallow cap and verify the fallback path.
	MaxDepth int

	// IgnoreFields lists JSONPath patterns whose targets should be
	// dropped from the generated type. Mirrors the format of
	// HAProxyTemplateConfig.spec.watchedResourcesIgnoreFields and
	// reuses the [k8s.io/client-go/util/jsonpath] parser so the
	// runtime field filter (pkg/k8s/indexer) and this converter
	// agree on which patterns are well-formed and how their
	// segments break down. Mismatch between the two would let
	// templates compile against schema-declared fields that the
	// watcher strips before storage — reliably zero at render time
	// with no diagnostic.
	//
	// The converter doesn't disambiguate by pattern *syntax* —
	// `metadata.annotations.k` and `metadata.annotations['k']`
	// parse to the same FieldNode chain in K8s JSONPath, by
	// design. Instead the SCHEMA WALK decides what stripping is
	// possible: ignoreSet entries are checked only at the
	// per-property iteration in convertObject. Map-key patterns
	// (whose target sits inside an `additionalProperties` subtree)
	// never see that iteration because the converter doesn't
	// recurse into a map's value space; the runtime filter still
	// removes the key, the typed shape stays intact. Plain
	// whole-property patterns DO match an iteration and strip the
	// field. Array-index / wildcard / filter / recursive patterns
	// don't survive parseDottedJSONPath at all — they couldn't
	// strip a typed shape even in principle.
	//
	// Examples:
	//   "metadata.managedFields"     → Metadata struct has no ManagedFields
	//   "metadata.annotations['k']"  → Annotations stays map[string]string
	//   "spec.rules[0].host"         → no type change (ArrayNode)
	//
	// CAVEAT for $ref-shared subschemas:
	//
	// When a schema component is referenced from multiple property
	// paths (e.g. ObjectMeta referenced at both `metadata` and
	// `template.metadata`), the converter resolves the ref only
	// once and caches the resulting type. The ignore-pattern
	// matching uses the FIRST path visited, which is determined by
	// the converter's deterministic alphabetical iteration over
	// sibling properties. A pattern like `template.metadata.
	// managedFields` would silently no-op when `metadata` comes
	// first alphabetically: the ObjectMeta type gets resolved at
	// the `metadata` path (which doesn't match the pattern), is
	// cached, and the later `template.metadata` visit hits the
	// cache and inherits the un-stripped type.
	//
	// Use the alphabetically-first path when authoring an ignore
	// pattern targeting a $ref-shared schema. For the K8s standard
	// case — ObjectMeta referenced once per resource at the
	// `metadata` path — this is automatically satisfied and the
	// canonical pattern `metadata.managedFields` works as expected.
	//
	// Set this BEFORE calling Convert. Mutating after a Convert call
	// is undefined; build a new Converter instead.
	IgnoreFields []string

	// ignoreSet is the set form of IgnoreFields, computed on demand.
	// Lazy so callers can keep mutating IgnoreFields until the first
	// Convert (typical bootstrap shape: merge global + per-resource
	// patterns into the slice, then convert).
	ignoreSet map[string]struct{}
}

// NewConverter builds a [Converter] whose $ref resolution targets the
// supplied components map (typically [spec3.OpenAPI].Components.Schemas
// converted to the v2-shaped spec.Schema via the adapter in
// pkg/k8s/typegen/adapters.go). Pass nil when callers only feed
// inline schemas with no $ref pointers.
func NewConverter(components map[string]spec.Schema) *Converter {
	return &Converter{
		components: components,
		typeCache:  map[string]reflect.Type{},
	}
}

// Convert returns the [reflect.Type] that corresponds to schema. The
// returned type is always non-nil — degraded subtrees and unrepresentable
// shapes (oneOf without a common shape, schemas with no type and no
// $ref, etc.) collapse to interface{} rather than producing an error.
// Errors are reserved for genuine structural problems: a $ref that
// can't be resolved against the supplied components, or an array
// schema with no Items.
func (c *Converter) Convert(schema *spec.Schema) (reflect.Type, error) {
	c.compileIgnoreSet()
	return c.convert(schema, "", 0)
}

// compileIgnoreSet folds IgnoreFields into a set of plain dotted paths
// for O(1) lookup during conversion. Idempotent; safe to call on every
// Convert because the result is cached on the receiver.
//
// Pattern parsing goes through k8s.io/client-go/util/jsonpath — the
// same library pkg/k8s/indexer/jsonpath.go uses for the runtime field
// filter. Reusing it means typegen and the field filter agree on which
// patterns are well-formed and how their segments break down, so an
// operator's WatchedResourcesIgnoreFields entry can't be interpreted
// one way at storage time and a different way at type-generation time.
//
// Patterns whose AST contains anything other than FieldNodes (array
// indices, filters, wildcards, recursive descent, …) target VALUES
// inside runtime containers, not struct fields, so they can't strip
// type properties. They're silently dropped from the ignoreSet; the
// runtime FieldFilter still applies them, just no compile-time benefit.
// Listing them in IgnoreFields when calling typegen is harmless.
func (c *Converter) compileIgnoreSet() {
	if c.ignoreSet != nil {
		return
	}
	c.ignoreSet = make(map[string]struct{}, len(c.IgnoreFields))
	for _, raw := range c.IgnoreFields {
		path, ok := parseDottedJSONPath(raw)
		if !ok {
			continue
		}
		c.ignoreSet[path] = struct{}{}
	}
}

// parseDottedJSONPath runs the supplied IgnoreFields entry through the
// client-go JSONPath parser and reports it as a plain dotted path only
// if every segment is a [jsonpath.FieldNode]. The returned path is the
// dot-joined sequence of FieldNode values, matching the path strings
// the converter threads through convert() — so a successful return is
// ready to feed straight into the ignoreSet lookup.
//
// jsonpath.Parse expects its input wrapped in {…} delimiters; mirror
// pkg/k8s/indexer/jsonpath.go's wrapping so the library sees the
// expression in the same shape both call sites use.
func parseDottedJSONPath(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	wrapped := "{." + strings.TrimPrefix(raw, ".") + "}"
	parser, err := jsonpath.Parse("typegen-ignore", wrapped)
	if err != nil {
		return "", false
	}
	// The library wraps the parsed content in two ListNodes: the
	// outermost Root and a per-{...} sub-list. Drill in once.
	if parser.Root == nil || len(parser.Root.Nodes) == 0 {
		return "", false
	}
	inner, ok := parser.Root.Nodes[0].(*jsonpath.ListNode)
	if !ok {
		return "", false
	}
	segments := make([]string, 0, len(inner.Nodes))
	for _, n := range inner.Nodes {
		field, ok := n.(*jsonpath.FieldNode)
		if !ok {
			// Anything else — ArrayNode, FilterNode, WildcardNode,
			// RecursiveNode, IdentifierNode — disqualifies the
			// pattern from type-level stripping. The runtime field
			// filter still handles it; type generation just leaves
			// the shape alone.
			return "", false
		}
		segments = append(segments, field.Value)
	}
	if len(segments) == 0 {
		return "", false
	}
	return strings.Join(segments, "."), true
}

func (c *Converter) maxDepth() int {
	if c.MaxDepth > 0 {
		return c.MaxDepth
	}
	return DefaultMaxDepth
}

// convert is the recursive workhorse. depth tracks how many levels we
// recursed without resolving a $ref through the cache — every level past
// the cap returns any. Hitting the cap on a constructive schema means
// the schema is genuinely deeper than DefaultMaxDepth, which we treat
// as "probably unbounded" rather than "extend the cap".
//
// path is the dotted JSON path leading TO this schema from the root
// (empty at the root call, "metadata" inside the metadata property,
// "metadata.labels" inside its labels sub-property, …). It's used
// exclusively for IgnoreFields matching; type identity doesn't depend
// on it.
func (c *Converter) convert(schema *spec.Schema, path string, depth int) (reflect.Type, error) {
	if schema == nil {
		return anyType, nil
	}
	if depth > c.maxDepth() {
		return anyType, nil
	}

	// $ref: resolve via components, with cache.
	//
	// The cache key is the bare $ref string (no path component). Two
	// occurrences of "#/components/schemas/.../ObjectMeta" anywhere in
	// the schema tree must yield the same reflect.Type — Scriggo uses
	// pointer identity in places, and silent duplication of equivalent
	// types causes confusing "X is not assignable to X" errors. Note
	// this means IgnoreFields applied to one occurrence of a shared
	// ref WILL affect every other occurrence — that's the right
	// behaviour for "metadata.managedFields" (ObjectMeta is referenced
	// uniformly across resources) and the only behaviour that
	// preserves type identity. Callers wanting per-occurrence stripping
	// would need to deep-copy components, which we don't do here.
	if ref := schema.Ref.String(); ref != "" {
		if cached, ok := c.typeCache[ref]; ok {
			return cached, nil
		}
		target, err := c.resolveRef(ref)
		if err != nil {
			return nil, err
		}
		// Insert a placeholder for the ref before recursing so that
		// any second visit during the recursion sees the cache hit
		// and bails to any — matches the doc-comment promise that
		// "the second visit returns the cached type". For now we use
		// anyType as the placeholder; a future refinement could
		// install a *struct{} sentinel and patch it after recursion,
		// but K8s schemas don't actually recurse through refs (the
		// RawExtension case is preserve-unknown, which short-circuits
		// before we hit the ref), so the simpler scheme suffices.
		c.typeCache[ref] = anyType
		t, err := c.convert(target, path, depth+1)
		if err != nil {
			return nil, err
		}
		c.typeCache[ref] = t
		return t, nil
	}

	// x-kubernetes-preserve-unknown-fields: the subtree is intentionally
	// unconstrained. Match Kubernetes' own runtime behaviour by emitting
	// any so dig() / digstr() still navigate at render time.
	if hasPreserveUnknown(schema) {
		return anyType, nil
	}

	// `allOf: [{ $ref: ... }]` — the K8s aggregated OpenAPI v3
	// canonical pattern for attaching defaults (or just suppressing
	// "schema has no type" warnings) to a shared-type reference.
	// Every `metadata: ObjectMeta` field, every Time, every shared
	// sub-resource enum on a built-in resource takes this shape:
	//
	//   metadata:
	//     allOf:
	//     - $ref: "#/components/schemas/.../ObjectMeta"
	//     default: {}
	//
	// Without this handler the schema falls through to the `case ""`
	// branch and degrades to any, which collapses `gw.Metadata` to
	// interface{} in chart templates — the bug Phase 11 exists to
	// fix. We unwrap the single-element allOf to its lone inner
	// schema and recurse, which lands in the standard $ref-handling
	// path above on the next call.
	//
	// Multi-element allOf (used for schema composition: "must match
	// shape A AND shape B") is not handled — Go can't represent
	// the intersection without flattening, and K8s doesn't use
	// multi-element allOf for any watched-resource type today.
	// Those degrade to any.
	if len(schema.AllOf) == 1 {
		return c.convert(&schema.AllOf[0], path, depth+1)
	}

	// Schemas with a `type` keyword. K8s schemas usually have exactly
	// one entry but the OpenAPI grammar allows an array. We pick the
	// first non-"null" entry; multi-typed schemas (extremely rare in
	// the watched-resource set) degrade to any.
	t := primaryType(schema)
	switch t {
	case typeObject:
		return c.convertObject(schema, path, depth)
	case typeArray:
		return c.convertArray(schema, path, depth)
	case typeString:
		return reflect.TypeOf(""), nil
	case typeInteger:
		// K8s schemas don't reliably distinguish int32 from int64.
		// int64 covers both because every int32 fits, and it matches
		// what client-go's unstructured.Unstructured produces when
		// it deserialises integer fields out of an Unstructured tree.
		return reflect.TypeOf(int64(0)), nil
	case typeBoolean:
		return reflect.TypeOf(false), nil
	case typeNumber:
		return reflect.TypeOf(float64(0)), nil
	case "":
		// No `type` and no `$ref`. This is legal in OpenAPI and shows
		// up for either (a) an object with only AdditionalProperties
		// set (a free-form map) or (b) a permissive schema that
		// accepts any shape. Both degrade to any.
		return anyType, nil
	default:
		// Future / unknown type keyword. Don't fail — degrade.
		return anyType, nil
	}
}

func (c *Converter) convertObject(schema *spec.Schema, path string, depth int) (reflect.Type, error) {
	// An object schema with no Properties and only AdditionalProperties
	// is a free-form map. Cover both shapes:
	//   1. additionalProperties: { ... schema }  → map[string]<schema>
	//   2. additionalProperties: true            → map[string]any
	//
	// kube-openapi's SchemaOrBool unmarshaller sets *both* Allows=true
	// AND Schema=&{...} when the JSON form is the object variant — so
	// Schema!=nil must be checked first, otherwise a typed map (every
	// labels / annotations field in K8s) silently degrades to
	// map[string]any. See validation/spec/swagger.go's
	// SchemaOrBool.UnmarshalJSON.
	//
	// AdditionalProperties subtrees don't get an extra path segment —
	// IgnoreFields would have to use bracketed syntax to remove
	// specific map keys, which we don't honour here. Pass the parent
	// path through unchanged so any whole-map ignore would have hit
	// at the property level above this.
	if len(schema.Properties) == 0 && schema.AdditionalProperties != nil {
		ap := schema.AdditionalProperties
		if ap.Schema != nil {
			valType, err := c.convert(ap.Schema, path, depth+1)
			if err != nil {
				return nil, err
			}
			return reflect.MapOf(reflect.TypeOf(""), valType), nil
		}
		if ap.Allows {
			// `true` form (no schema): arbitrary keys, arbitrary values.
			return reflect.MapOf(reflect.TypeOf(""), anyType), nil
		}
	}

	// Empty object schema: degrade to any rather than emitting an
	// empty struct, because callers expect to do something with the
	// value (Scriggo on an empty struct can't dot-access anything).
	if len(schema.Properties) == 0 {
		return anyType, nil
	}

	// Iterate in sorted-key order so the generated type's field order
	// is deterministic across boots. reflect.StructOf compares fields
	// by position when building struct identity; deterministic order
	// keeps cache lookups behaving sanely if a future refactor wants
	// to dedup identical schemas.
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	fields, degraded, err := c.collectObjectFields(schema, names, path, depth)
	if err != nil {
		return nil, err
	}
	if degraded {
		// Caller-visible signal that some per-property condition
		// (Go-name collision today; future: other reflect.StructOf
		// constraints) means we can't represent the object as a
		// typed struct. Templates fall back to dig() against the
		// any value at render time.
		return anyType, nil
	}
	// Every Property was stripped by IgnoreFields. Returning an
	// empty struct would be technically valid but useless — Scriggo
	// can't dot-access anything on it, and the chart-side intent of
	// "this whole subtree is gone" is better expressed as any (which
	// also keeps dig() back-compat working at render time).
	if len(fields) == 0 {
		return anyType, nil
	}
	return reflect.StructOf(fields), nil
}

// collectObjectFields walks the named properties of an object schema
// in sorted-name order, applies IgnoreFields stripping, and returns
// the resulting reflect.StructField slice. The `degraded` return
// signals that the caller should treat the object as `any` because
// reflect.StructOf would reject the field set (currently only
// GoFieldName collisions; see the inline note).
//
// Extracted from convertObject to keep that function under the
// cognitive-complexity budget — the per-property body has too many
// branches (ignore check, propSchema fetch, recurse, name-collision
// check, append) to live inline alongside the additionalProperties
// and empty-Properties early returns.
func (c *Converter) collectObjectFields(schema *spec.Schema, names []string, path string, depth int) ([]reflect.StructField, bool, error) {
	fields := make([]reflect.StructField, 0, len(names))
	// requiredSet flags which JSON names appear in the schema's
	// `required` list. Non-required fields get a `,omitempty` json
	// tag so digStructField can normalise "field absent in source"
	// to nil at render time, matching the untyped-map semantics
	// every existing dig|fallback chart pattern relies on.
	// Without this, the typed shape returns the type's zero value
	// (`""`, `0`, `false`) for unpopulated optional fields, fallback
	// doesn't fire (its input isn't nil), and chart logic that
	// branches on "absent vs present" silently misbehaves.
	requiredSet := make(map[string]struct{}, len(schema.Required))
	for _, r := range schema.Required {
		requiredSet[r] = struct{}{}
	}
	// seenGoNames detects collisions where multiple JSON property
	// names produce the same Go identifier under GoFieldName's
	// capitalise-and-sanitise rule (e.g. "my-field" and "my_field"
	// both become "My_field"; "name" and "Name" both become
	// "Name"). Without this check, reflect.StructOf would panic
	// on the duplicate field.
	seenGoNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		childPath := name
		if path != "" {
			childPath = path + "." + name
		}
		if _, ignored := c.ignoreSet[childPath]; ignored {
			continue
		}
		propSchema := schema.Properties[name]
		propType, err := c.convert(&propSchema, childPath, depth+1)
		if err != nil {
			return nil, false, fmt.Errorf("property %q: %w", name, err)
		}
		goName := GoFieldName(name)
		if _, dup := seenGoNames[goName]; dup {
			// Hostile / unusual schema (a CRD defining both
			// "name" and "Name", or using hyphens / underscores
			// that collapse to the same Go identifier). Degrade
			// to any rather than panic — the chart still renders
			// for the affected resource through dig().
			return nil, true, nil
		}
		seenGoNames[goName] = struct{}{}
		// %q both for required and optional paths so property names
		// containing double quotes or backslashes (legal in CRDs even
		// if unlikely in K8s core APIs) produce well-formed struct
		// tags. The previous %s form for the optional path could emit
		// malformed tags that broke JSON marshalling AND the
		// isStructFieldOmitempty check.
		_, isRequired := requiredSet[name]
		tag := fmt.Sprintf(`json:%q`, name)
		if !isRequired {
			tag = fmt.Sprintf(`json:%q`, name+",omitempty")
			// Tristate fix (#52): wrap optional scalar types in
			// pointers so the chart's dig|fallback pattern can
			// distinguish "absent" (nil pointer) from "explicitly
			// zero" (non-nil pointer to zero value). The classic
			// breakage was the Gateway-API HTTPRouteWeight
			// conformance test: a backendRef with weight=0 means
			// "exclude this backend" per spec, but the chart's
			// `dig(backendRef, "weight") | fallback(1)` couldn't
			// tell it apart from a missing weight and defaulted
			// to 1 — v3 backend got 1 entry in the weighted-
			// multi-backend.map when it should have got 0.
			// json.Unmarshal handles pointer types natively: missing
			// key → nil pointer, explicit value → non-nil pointer.
			// digStructField dereferences automatically so the chart
			// keeps seeing plain int64/bool/float64 values.
			//
			// String fields stay non-pointer because the chart pattern
			// `dig(x, "namespace") | fallback(gwNs)` wants "" to read
			// back as nil (so the fallback substitutes the parent
			// namespace). digStructField's existing omitempty + IsZero
			// rule covers that case. Numeric/bool fields can't share
			// that rule because 0 / false are legitimate explicit values.
			// Complex shapes (struct, map, slice) keep their non-pointer
			// type — IsZero on those means "no nested data" which is
			// the same semantic as "absent" for chart purposes.
			if needsTristatePointer(propType) {
				propType = reflect.PointerTo(propType)
			}
		}
		fields = append(fields, reflect.StructField{
			Name: goName,
			Type: propType,
			Tag:  reflect.StructTag(tag),
		})
	}
	return fields, false, nil
}

func (c *Converter) convertArray(schema *spec.Schema, path string, depth int) (reflect.Type, error) {
	if schema.Items == nil || schema.Items.Schema == nil {
		// Array with no item schema or with the tuple-typed form
		// (Items.Schemas, used for fixed-arity tuples — not something
		// K8s emits). Degrade to []any so callers can still range.
		return reflect.SliceOf(anyType), nil
	}
	// Element path is the array's path with no index segment —
	// IgnoreFields can't strip a specific element type from the
	// shape (Go has no "this slice but with elements of stripped-T"),
	// so element-level stripping happens via the SAME path the array
	// itself sits at. A pattern like "spec.listeners" strips the
	// listeners slice entirely; "spec.listeners.name" would strip the
	// Name field from EACH Listener element.
	elemType, err := c.convert(schema.Items.Schema, path, depth+1)
	if err != nil {
		return nil, err
	}
	return reflect.SliceOf(elemType), nil
}

// resolveRef parses a JSON-pointer-flavoured $ref string and returns the
// referenced schema. Only the OpenAPI v3 form
// "#/components/schemas/<name>" is supported (every K8s OpenAPI ref takes
// this shape). Anything else returns an error rather than silently
// degrading — a malformed ref usually points at a deeper bug than a
// "missing field" log line at render time would surface.
func (c *Converter) resolveRef(ref string) (*spec.Schema, error) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return nil, fmt.Errorf("typegen: unsupported $ref %q (only %q-prefixed refs are resolved)", ref, prefix)
	}
	name := ref[len(prefix):]
	if c.components == nil {
		return nil, fmt.Errorf("typegen: $ref %q cannot be resolved (no components map registered)", ref)
	}
	target, ok := c.components[name]
	if !ok {
		return nil, fmt.Errorf("typegen: $ref %q targets unknown schema %q", ref, name)
	}
	return &target, nil
}

// primaryType picks a single type token from schema.Type. K8s schemas
// either set Type to one element or leave it empty (the latter when only
// $ref / oneOf / additionalProperties carry shape information).
func primaryType(schema *spec.Schema) string {
	for _, t := range schema.Type {
		if t != "null" {
			return t
		}
	}
	return ""
}

// hasPreserveUnknown reports whether the schema is annotated with
// `x-kubernetes-preserve-unknown-fields: true`. The annotation lives in
// schema.Extensions which is a free-form map[string]any populated from
// the JSON parse; both bool true and string "true" appear in the wild,
// so we accept either.
func hasPreserveUnknown(schema *spec.Schema) bool {
	v, ok := schema.Extensions[preserveUnknownExt]
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

// needsTristatePointer reports whether t is a scalar Kind that needs
// pointer-wrapping when used as an optional struct field, so the chart's
// `dig | fallback` pattern can distinguish "absent in source" from
// "explicitly set to zero". Only numeric and boolean Kinds qualify —
// string fields read back through digStructField's existing omitempty +
// IsZero rule (chart wants "" to read as nil so fallback substitutes a
// parent value, e.g. the namespace inheritance case); complex shapes
// (struct, map, slice) reuse the same IsZero rule because "empty
// collection" semantically equals "absent" for chart consumers.
func needsTristatePointer(t reflect.Type) bool {
	if t == nil || t.Kind() == reflect.Pointer {
		return false
	}
	switch t.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// GoFieldName lifts a JSON property name into an exported Go identifier
// suitable for [reflect.StructField.Name]. The transformation is the
// minimum needed for Scriggo's compile-time field lookup to find the
// field by its capitalised name:
//
//   - first rune is uppercased (Scriggo and Go reflection both require
//     an exported field for outside-package access; reflect.StructOf
//     panics on lowercase first letters);
//   - non-letter / non-digit runes are replaced with '_' so the result
//     is a valid Go identifier (K8s fields are normally already
//     identifier-shaped — apiVersion, allowedListeners — but tolerating
//     '/' / '.' / '-' costs nothing and protects against schemas we
//     haven't seen);
//   - an empty input yields "_" because reflect.StructOf panics on
//     empty Name.
//
// Acronym preservation (e.g. apiVersion → APIVersion) is deliberately
// NOT performed. The cost is one extra rule for template authors to
// internalise; the win is no acronym dictionary to maintain. So:
// `apiVersion` → `ApiVersion`, `tlsConfig` → `TlsConfig`. Templates
// write `gw.ApiVersion`.
//
// It is exported because callers outside this package (e.g.
// pkg/controller/typebootstrap) need the same identifier rule to compose
// the `resources` struct's Go field names from watched-resource keys.
func GoFieldName(name string) string {
	if name == "" {
		return "_"
	}
	var b strings.Builder
	b.Grow(len(name))
	for i, r := range name {
		switch {
		case i == 0:
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
