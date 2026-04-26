# pkg/controller/validation

Three-phase HAProxy configuration validator with per-instance result caching.

## Overview

`ValidationService` runs the rendered HAProxy config through:

1. **Syntax** — `client-native` parser via `pkg/dataplane/parser`.
2. **OpenAPI schema** — version-specific Dataplane API schema check via `pkg/generated`.
3. **Semantic** — actual `haproxy -c` invocation. Each call writes auxiliary files into its own per-call `os.MkdirTemp` and rewrites the rendered config's `default-path origin` to point at it, so callers do *not* contend on shared paths. The serialisation that does exist sits one layer down in `pkg/dataplane.haproxyCheckMutex` — it serialises the `haproxy -c` binary invocation itself because concurrent runs interfere with each other even with isolated temp directories.

The result of a successful validation is cached per-instance keyed by a content checksum of the config + auxiliary files. Identical content (the common case during drift-prevention cycles) skips all three phases and returns the cached `*parser.StructuredConfig` immediately. Failures are never cached — a failed validation always retries on the next call.

The service is consumed by `pkg/controller/pipeline.Pipeline.Execute`, which is in turn driven by both the leader-side reconciler (`pkg/controller/reconciler.Coordinator`) and the webhook-side proposal validator (`pkg/controller/proposalvalidator`). Per-instance caching keeps the webhook from evicting the main pipeline's cache.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

svc := validation.NewValidationService(&validation.ValidationServiceConfig{
    Logger:            logger,
    Version:           &dataplane.Version{Major: 3, Minor: 2}, // schema selector; nil = v3.0
    SkipDNSValidation: false,                                  // true for runtime, false for webhook
    BaseDir:           "/etc/haproxy",                         // production default-path origin
    MapsDir:           "maps",                                 // relative names match RenderService
    SSLCertsDir:       "ssl",
    GeneralDir:        "general",
})

// One-shot: hashes content internally and returns *ValidationResult
result := svc.Validate(ctx, haproxyConfig, auxFiles)

// Pipeline-friendly: pass the checksum the renderer already computed,
// so we don't hash the same config twice per reconciliation.
result = svc.ValidateWithChecksum(ctx, haproxyConfig, auxFiles, checksum)
// result.Valid, result.Phase, result.Error, result.ParsedConfig (for downstream Sync)
```

`SkipDNSValidation` is the only field whose right value depends on the caller: the leader pipeline runs in **permissive** mode (true) so a temporarily-unresolvable backend hostname doesn't cause cascading reconciliation failures; the webhook runs in **strict** mode (false) so admission catches typos in service names before they reach production.

## Caching Semantics

| Call | Behaviour |
|------|-----------|
| First call, or content checksum changes | Run all three phases; cache `ParsedConfig` if successful |
| Repeat call with the same checksum | Return cached `ParsedConfig` immediately, skip all phases |
| Failure (any phase) | Return error, leave cache untouched (next call retries) |

The cache lives on the `*ValidationService` instance. Constructing a new service (e.g. between iterations) clears it implicitly.

## See Also

- [`pkg/dataplane`](../../dataplane/) — `client-native` parser + the underlying three-phase validation primitives this service composes
- [`pkg/controller/pipeline`](../pipeline/) — the only consumer; threads the content checksum from the renderer through to here
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — webhook caller (strict mode)
- [`pkg/controller/reconciler`](../reconciler/) — leader caller (permissive mode)

## License

Apache-2.0 — see root `LICENSE`.
