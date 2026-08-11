# pkg/controller/validation

Three-phase HAProxy configuration validator with per-instance result caching.

## Overview

`ValidationService` runs the rendered HAProxy config through:

1. **Syntax** — `client-native` parser via `pkg/dataplane/parser`.
2. **OpenAPI schema** — version-specific Dataplane API schema check via `pkg/generated`.
3. **Semantic** — actual `haproxy -c` invocation. Each call writes auxiliary files into its own per-call `os.MkdirTemp` and rewrites the rendered config's `default-path origin` to point at it, so callers don't contend on shared paths. A cancellable gate serialises the binary invocation because concurrent runs interfere even with isolated temp directories.

The result of a successful validation is cached per-instance keyed by a content checksum of the config + auxiliary files. Identical content (the common case during drift-prevention cycles) skips all three phases and returns the cached `*parser.StructuredConfig` immediately. Cancellation terminates a running or queued binary check and is never cached as success.

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
    SkipSemanticValidation: false,                             // true skips `haproxy -c`
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

`SkipDNSValidation` is true in the shared controller pipeline so a temporarily unresolvable backend hostname doesn't cause cascading reconciliation failures. `SkipSemanticValidation` exists for offline callers with a stronger replacement gate; the controller pipeline leaves it false and runs `haproxy -c` for every changed render. Content-checksum caching makes identical drift-prevention renders return without repeating any phase.

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
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — webhook and HTTP-store caller
- [`pkg/controller/reconciler`](../reconciler/) — leader caller

## License

Apache-2.0 — see root `LICENSE`.
