# pkg/k8s/indexer

Extracts composite index keys from Kubernetes resources via JSONPath expressions, and (optionally) strips fields the controller doesn't care about to reduce memory pressure.

## Overview

`*Indexer` combines two concerns: turning a watched resource into the slice of strings the store uses as a lookup key, and removing noisy fields (e.g. `metadata.managedFields`) from the in-memory copy. Both phases are configured with JSONPath expressions, evaluated up-front for fail-fast validation.

This is what the controller wires when it consumes the CRD's `indexBy` and `watchedResourcesIgnoreFields`.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"

idx, err := indexer.New(indexer.Config{
    IndexBy: []string{
        "metadata.namespace",
        "metadata.name",
    },
    IgnoreFields: []string{
        "metadata.managedFields",
    },
})
if err != nil { /* invalid JSONPath -- fail at startup */ }

keys, err := idx.ExtractKeys(ingress)         // ["default", "my-ingress"]
err = idx.FilterFields(ingress)               // mutates in place
```

`ExtractKeys` returns the keys in the same order as the `IndexBy` slice, so a store can build a `default/my-ingress` composite key (or accept partial-prefix lookups against just `default`). `FilterFields` mutates the resource in place — feed it the same object you'll hand to the store afterwards.

## Helpers

- `NewJSONPathEvaluator(expr)` — parses a single expression up-front, lets callers cache it.
- `NewFieldSelectorMatcher(expr)` — used by watchers that take a `fieldSelector` and need to match resources locally.
- Errors are typed (`*IndexError` from `ExtractKeys`, `*FilterError` from `FilterFields`, `*JSONPathError` from `NewJSONPathEvaluator`) so callers can extract the expression / pattern that failed without string-matching the message. All three implement `Unwrap()` so `errors.Is` / `errors.As` walks through to the underlying cause.

## See Also

- [`pkg/k8s/watcher`](../watcher/) — drives the indexer for every event it dispatches
- [`pkg/k8s/store`](../store/) — consumes the keys produced here

## License

Apache-2.0 — see root `LICENSE`.
