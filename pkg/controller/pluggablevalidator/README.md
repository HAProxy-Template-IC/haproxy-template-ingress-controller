# pkg/controller/pluggablevalidator

Dispatches rendered files to external validator sidecars (for example the SPOA hub in `--validate-socket` mode) during admission.

## Overview

`Manager` fans a render's files out to every configured validator over a Unix domain socket and aggregates the replies into a `ValidationOutcome`. Its `Errors` deny admission; its `Warnings` flow through to `AdmissionResponse.Warnings`, so a soft diagnostic reaches the operator without blocking the apply. `Outcome.Result()` folds the two lists into the same `Result` value the wire protocol uses per response.

Results are cached by `CacheKey`, keyed per validator, file path, and content hash (plus the data files that travelled with the request), so repeated identical content skips the round-trip.

`Configured()` reports whether any validators are registered. `ValidateAll` is a no-op when none are, so callers don't have to pre-check — the webhook and `/healthz` use `Configured()` only to skip work they'd otherwise do around the call.

The wire protocol itself is documented in [`docs/development/validator-protocol.md`](../../../docs/development/validator-protocol.md).

## Quick Start

```go
mgr, err := pluggablevalidator.NewManager(logger, configs)
if err != nil {
    return err
}

outcome := mgr.ValidateAll(ctx, files)
if len(outcome.Errors) > 0 {
    // deny admission
}
```

## See Also

- [`pkg/controller/dryrunvalidator`](../dryrunvalidator/) — the production caller
- [Pluggable Validators](../../../docs/site/docs/operations/pluggable-validators.md) — operator-facing configuration

## License

Apache-2.0 — see root `LICENSE`.
