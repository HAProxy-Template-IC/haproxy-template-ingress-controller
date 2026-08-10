# pkg/k8s/typegen

Converts OpenAPI v3 schemas into `reflect.Type` values so the template engine can declare typed watched-resource globals.

## Overview

`Converter.Convert` walks one schema and materialises the matching Go type — a `reflect.StructOf` struct for objects — resolving `$ref` pointers against the components map handed to `NewConverter`. Shapes it can't represent degrade to `interface{}` rather than erroring. `IgnoreFields` takes dotted JSONPath patterns (for example `metadata.managedFields`) and prunes those subtrees before conversion; it's read lazily, so callers can keep appending until the first `Convert`.

`GoFieldName` is the canonical JSON-property → exported-Go-identifier rule: uppercase rune 0, leave the rest alone, and deliberately no acronym dictionary — so `apiVersion` becomes `ApiVersion`, not `APIVersion`. It's exported because `pkg/controller/typebootstrap` has to compose the same names independently; the two must not drift.

`WrapInto` populates a generated type from an unstructured object.

See [ADR-0010 — Typed Watched Resources](../../../docs/adr/0010-typed-watched-resources.md) for the design rationale.

## Quick Start

```go
// components: map[string]spec.Schema for $ref resolution; nil when the schema is inline-only.
conv := typegen.NewConverter(components)
conv.IgnoreFields = []string{"metadata.managedFields"}

typ, err := conv.Convert(&schema)
if err != nil {
    return err
}

val, err := typegen.WrapInto(unstructuredObject, typ)
```

## See Also

- [`pkg/k8s/schemafetcher`](../schemafetcher/) — supplies the schemas this package converts
- [`pkg/templating`](../../templating/) — consumes the generated types as Scriggo declarations
- [`pkg/controller/typebootstrap`](../../controller/typebootstrap/) — builds the per-resource store declarations

## License

Apache-2.0 — see root `LICENSE`.
