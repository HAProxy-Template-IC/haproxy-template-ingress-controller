# pkg/controller/validation

HAProxy configuration validator with per-call sandboxing.

## Overview

`ValidationService` writes the rendered HAProxy config and auxiliary files into
an isolated `os.MkdirTemp` tree, rewrites `default-path origin` to that tree,
and invokes `haproxy -c`. A cancellable gate bounds concurrent binary checks.

Every call executes HAProxy, including calls with byte-identical output. The
binary, executor, DNS state, and other runtime inputs can change between calls,
so a content checksum alone cannot carry a prior verdict forward. Future result
reuse requires an authenticated hermetic-environment root bound to the exact
config and auxiliary bytes.

The service is used by strict pipeline validation and the leader-side render
gate. Snapshot entry points authenticate and materialize their immutable input
before invoking the same check.

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

svc := validation.NewValidationService(&validation.ValidationServiceConfig{
    Logger:            logger,
    SkipDNSValidation: false,
    BaseDir:           "/etc/haproxy",
    MapsDir:           "maps",
    SSLCertsDir:       "ssl",
    GeneralDir:        "general",
    CheckGate:         dataplane.NewCheckGate(0),
})

// The checksum remains the render's downstream content identity. It never
// authorizes validation reuse.
checksum := dataplane.ComputeContentChecksum(haproxyConfig, auxFiles)
result := svc.ValidateWithChecksum(ctx, haproxyConfig, auxFiles, checksum)
// result.Valid, result.Phase, result.Error
```

`SkipDNSValidation` adds HAProxy's `-dr` flag. Runtime checks use it so a
temporarily unresolvable backend starts down instead of blocking convergence;
strict proposal validation can leave it false.

## Validation semantics

| Call | Behaviour |
|------|-----------|
| Any authenticated input, including an exact repeat | Materialize the files and run `haproxy -c` |
| HAProxy refusal | Return a semantic validation error |
| Cancellation before, during, or immediately after the check | Return the cancellation cause; never report success |
| Invalid snapshot authentication | Fail during setup without running HAProxy |

## See Also

- [`pkg/dataplane`](../../dataplane/) — the underlying `haproxy -c` execution path
- [`pkg/controller/pipeline`](../pipeline/) — strict proposal and load-gate caller
- [`pkg/controller/rendergate`](../rendergate/) — leader-side runtime caller
- [`pkg/controller/proposalvalidator`](../proposalvalidator/) — webhook and HTTP-store caller
- [`pkg/controller/reconciler`](../reconciler/) — leader caller

## License

Apache-2.0 — see root `LICENSE`.
