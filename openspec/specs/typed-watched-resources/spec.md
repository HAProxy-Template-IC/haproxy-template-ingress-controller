# typed-watched-resources Specification

## Purpose

Gives templates typed, dot-field access to any watched Kubernetes resource without build-time code generation: OpenAPI v3 schemas are fetched at runtime (live from the kube-apiserver or offline from a schema directory) and converted into reflect types that the template engine exposes per watched resource. The conversion is resource-agnostic — it operates uniformly on whatever schema arrives — and degrades to untyped access rather than failing, so charts mix typed and dig-based access freely.

## Requirements

### Requirement: Schema-to-Type Conversion Never Fails Structurally

The converter SHALL turn an OpenAPI v3 schema into a runtime reflect type and SHALL always return a non-nil type: unrepresentable shapes (multi-element allOf, schemas with no type and no $ref, multi-typed schemas, unknown type keywords, empty object schemas, arrays without an item schema) SHALL degrade to the empty interface (or a slice/map of it) rather than producing an error. Errors SHALL be reserved for an unresolvable $ref: an unsupported ref format, a ref with no components map registered, or a ref targeting an unknown component.

#### Scenario: Permissive schema degrades to any

- **WHEN** a schema carries neither a type keyword nor a $ref
- **THEN** conversion SHALL return the empty-interface type without error.

#### Scenario: Array without items degrades to slice of any

- **WHEN** an array schema has no item schema
- **THEN** conversion SHALL return a slice-of-any type without error.

#### Scenario: Unknown ref target errors

- **WHEN** a schema references a component name absent from the registered components map
- **THEN** conversion SHALL return an error naming the unresolvable reference.

### Requirement: Type Mapping Rules

The converter SHALL map schema types as follows: `string` to Go string; `integer` to int64 (K8s schemas do not reliably distinguish int32 from int64, and int64 matches what unstructured deserialization produces); `number` to float64; `boolean` to bool; `object` with properties to a generated struct; `array` to a slice of the converted item type. A schema annotated `x-kubernetes-preserve-unknown-fields` (boolean true or string "true") SHALL collapse to the empty interface so dig-based navigation still works at render time. A single-element `allOf` SHALL be unwrapped to its inner schema (the K8s aggregated-OpenAPI pattern for shared-type references); multi-element `allOf` SHALL degrade to any. An object with no properties and a schema-valued `additionalProperties` SHALL become a string-keyed map of the converted value type — checked before the boolean form, since the parser sets both — and `additionalProperties: true` SHALL become a string-keyed map of any. Multi-typed schemas SHALL use the first non-"null" type entry.

#### Scenario: Labels field becomes a typed map

- **WHEN** a property is an object with `additionalProperties` of type string
- **THEN** the generated field type SHALL be a map from string to string, not a map to any.

#### Scenario: Metadata allOf-ref unwrapped

- **WHEN** a property is expressed as a single-element allOf wrapping a $ref to a shared component
- **THEN** the converter SHALL resolve through the allOf to the referenced component's generated struct type.

#### Scenario: Preserve-unknown subtree collapses

- **WHEN** a property carries `x-kubernetes-preserve-unknown-fields: true`
- **THEN** the generated field type SHALL be the empty interface.

### Requirement: Recursion Depth Cap

Conversion SHALL cap recursion at a configurable maximum depth, defaulting to 32. Any subtree deeper than the cap SHALL degrade to the empty interface rather than recursing further — a schema deeper than the default cap is treated as probably unbounded.

#### Scenario: Over-deep subtree degrades

- **WHEN** a schema nests beyond the configured maximum depth
- **THEN** the portion beyond the cap SHALL convert to the empty interface and conversion SHALL still succeed.

### Requirement: Ref Cache and Pointer Identity

Resolved $ref results SHALL be cached by the bare ref string, so every occurrence of the same reference anywhere in a schema tree yields the identical reflect type (the template engine relies on pointer identity — duplicated equivalent types cause spurious assignability errors). The cache SHALL also terminate recursive references: a placeholder is installed before recursing so a second visit during resolution returns from the cache instead of recursing indefinitely. Because the cache is shared, an ignore-field pattern applied to one occurrence of a shared reference affects every occurrence.

#### Scenario: Shared component yields one type

- **WHEN** two properties in different subtrees reference the same schema component
- **THEN** both generated fields SHALL carry the identical reflect type instance.

### Requirement: Go Field Naming and Collision Degradation

JSON property names SHALL be lifted to exported Go field names by uppercasing the first rune and replacing every non-letter, non-digit rune with an underscore; an empty name yields `_`. Acronym preservation SHALL NOT be performed (`apiVersion` becomes `ApiVersion`, not `APIVersion`). Properties SHALL be iterated in sorted-name order so generated types are deterministic across restarts. If two JSON property names collapse to the same Go identifier, the whole object SHALL degrade to the empty interface rather than panicking on a duplicate struct field.

#### Scenario: No acronym dictionary

- **WHEN** a schema declares an `apiVersion` property
- **THEN** the generated field SHALL be named `ApiVersion`.

#### Scenario: Colliding names degrade the object

- **WHEN** a schema declares both `my-field` and `my_field` (which collapse to the same Go identifier)
- **THEN** the containing object SHALL convert to the empty interface.

### Requirement: Optional Fields Get omitempty and Tristate Pointers

Every generated field SHALL carry a json struct tag with the original property name. Properties absent from the schema's `required` list SHALL additionally carry `omitempty`, so absent optional fields normalise to nil at render time, matching untyped-map semantics for the universal dig-plus-fallback template pattern. Optional numeric and boolean scalar fields SHALL be pointer-wrapped so templates can distinguish "absent" (nil pointer) from "explicitly zero" (non-nil pointer to a zero value). Optional string fields and complex shapes (structs, maps, slices) SHALL remain non-pointer: an empty string or empty collection reads back as absent, which is the semantics chart fallbacks rely on.

#### Scenario: Explicit zero distinguishable from absent

- **WHEN** an optional integer property (e.g. a route weight) is explicitly set to 0 in the source object
- **THEN** the populated field SHALL be a non-nil pointer to 0, distinguishable from a missing property whose pointer is nil.

#### Scenario: Optional strings stay non-pointer

- **WHEN** an optional string property is converted
- **THEN** the generated field type SHALL be a plain string with an omitempty tag.

### Requirement: IgnoreFields Strips Whole Properties Only

The converter SHALL accept ignore-field patterns in the same JSONPath dialect the runtime field filter uses, and SHALL strip a generated field only when the pattern parses to a plain dotted chain of field segments matching a property path. Patterns containing array indices, wildcards, filters, or recursive descent SHALL be silently excluded from type-level stripping (the runtime field filter still applies them). Map-key patterns targeting values inside an additionalProperties subtree SHALL leave the typed map shape intact. When every property of an object is stripped, the object SHALL convert to the empty interface rather than an empty struct. For a schema component referenced from multiple property paths, stripping is decided at the first path visited under the deterministic sorted iteration — patterns targeting a $ref-shared subschema must use the alphabetically-first referencing path.

#### Scenario: Whole-property pattern strips the field

- **WHEN** the ignore list contains `metadata.managedFields`
- **THEN** the generated metadata struct SHALL have no ManagedFields field.

#### Scenario: Map-key pattern leaves the type intact

- **WHEN** the ignore list contains a bracketed annotation-key pattern under `metadata.annotations`
- **THEN** the generated annotations field SHALL remain a string-keyed map type.

### Requirement: Wrapping Unstructured Objects into Generated Types

Populating an instance of a generated type from an unstructured object SHALL round-trip through JSON: marshal the unstructured map, then unmarshal into a new addressable instance of the generated type, driven by the json tags every generated field carries. On any unmarshal error, wrapping SHALL return a zero value plus the error; callers on the controller hot path log and skip the malformed resource rather than failing the whole reconcile.

#### Scenario: Malformed resource is skippable

- **WHEN** an unstructured object cannot be unmarshalled into the generated type
- **THEN** the wrap SHALL return an error (not panic) so the caller can skip that single resource.

### Requirement: Schema Fetcher Contract

Schemas SHALL come from a runtime schema source implementing a fetcher interface keyed by GroupVersionKind, never from build-time code generation. Fetch SHALL return the schema plus the components map needed to resolve any $ref entries it contains (CRD-backed sources return nil components because CRDs inline every shared shape). Returning a nil schema without an error SHALL be forbidden — failures surface as a schema-not-available error wrapping the underlying cause. Implementations MUST be safe for concurrent use, because controller bootstrap fans out across watched resources in parallel. A NotFound predicate SHALL distinguish "no schema exists for this GVK" from transient failures (network errors, timeouts) that a caller might retry.

#### Scenario: Nil-schema-without-error forbidden

- **WHEN** a fetcher cannot produce a schema for a GVK
- **THEN** it SHALL return a schema-not-available error rather than a nil schema with a nil error.

#### Scenario: NotFound discrimination

- **WHEN** a fetch fails because the GVK simply has no schema (as opposed to a network timeout)
- **THEN** the NotFound predicate SHALL return true for the returned error and false for the timeout case.

### Requirement: Schema Sources

Three fetcher implementations SHALL be provided. The cluster fetcher resolves against a live cluster: it tries the CRD path first (the CRD list is fetched at most once and cached; a context-cancelled fetch is evicted rather than poisoning the cache) and falls back to the aggregated OpenAPI v3 endpoint per GroupVersion, which covers built-in resources and any registered CRD. The directory fetcher is the offline counterpart, loading a directory of files in two freely mixed shapes: full CustomResourceDefinition wire format (schema taken from the served version, plural recorded for offline GVK resolution) and bare OpenAPI v3 schemas carrying the group-version-kind extension (no plural, nil components). The map fetcher serves pre-populated schemas from memory for tests. There SHALL be no embedded fallback schema in the binary.

#### Scenario: CRD list fetched once

- **WHEN** the cluster fetcher resolves schemas for many watched resources during one bootstrap
- **THEN** the CRD list SHALL be fetched at most once and reused across all resolutions.

#### Scenario: Directory accepts both file shapes

- **WHEN** a schema directory contains a CRD-wrapped file and a bare OpenAPI v3 schema file with the group-version-kind extension
- **THEN** the directory fetcher SHALL serve schemas for both.

#### Scenario: No embedded fallback

- **WHEN** no schema source can produce a schema for a GVK
- **THEN** the fetch SHALL fail — the binary SHALL NOT substitute a bundled schema.

### Requirement: Fail-Closed Bootstrap

Controller bootstrap SHALL be fail-closed on schema resolution: a schema-not-available error for any watched resource SHALL surface as a hard iteration-startup error, so the operator gets a clear signal to investigate RBAC, CRD installation, or apiserver health rather than running with silently degraded typed access.

#### Scenario: Missing schema aborts startup

- **WHEN** schema resolution fails for one watched resource during bootstrap
- **THEN** the iteration SHALL fail to start with an error naming the affected GroupVersionKind.
