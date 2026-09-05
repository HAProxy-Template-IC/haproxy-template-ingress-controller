# pkg/controller/pluggablevalidator

Dispatches rendered files to external validator sidecars (for example the SPOA hub in `--validate-socket` mode) before publication or deployment.

## Overview

`Manager` implements the pipeline's rendered-output validator stage. It fans every successful render's files out to the configured validators over Unix domain sockets and aggregates the replies into a `ValidationOutcome`. Errors fail the pipeline before publication or deployment. Admission requests also return warnings through `AdmissionResponse.Warnings`; reconciliation records warning counts in its validation event and logs.

Every matching file is checked on every pipeline invocation. Protocol version 1
doesn't identify the validator process or implementation generation, so a
content-keyed verdict could survive a sidecar restart and validate against the
wrong implementation. A response whose `result` disagrees with its diagnostics
is a protocol failure and blocks the pipeline.

Future result reuse requires a new protocol version that exposes an
authenticated hermetic-environment root at a linearizable validation point.
That root must cover the validator runtime, executable, configuration, and
dependencies. A socket inode, process ID, connection-liveness probe, or
self-declared implementation version can't close the replacement race.

`Configured()` reports whether any validators are registered. `ValidateAll` is a no-op when none are, so callers don't have to pre-check; `/healthz` uses `Configured()` to omit the check when the feature is not configured.

The wire protocol itself is documented in [`docs/development/validator-protocol.md`](../../../docs/development/validator-protocol.md).

## Quick Start

```go
mgr, err := pluggablevalidator.NewManager(logger, configs)
if err != nil {
    return err
}

outcome := mgr.ValidateAll(ctx, files)
if outcome.Err != nil {
    return outcome.Err
}
if len(outcome.Errors) > 0 {
    return errors.New("rendered output rejected")
}
```

Cancellation makes the outcome an error even when every completed validator
returned `valid`. Active socket I/O is interrupted and the connection is
discarded. A partial fan-out is not a validation verdict.

## See Also

- [`pkg/controller/pipeline`](../pipeline/) — the production integration point
- [Pluggable Validators](../../../docs/site/docs/operations/pluggable-validators.md) — operator-facing configuration

## License

Apache-2.0 — see root `LICENSE`.
