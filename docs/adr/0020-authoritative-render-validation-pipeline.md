# ADR-0020: One authoritative validation pipeline for rendered output

## Status

Accepted 2026-08-10. Supersedes ADR-0016's strict-first/fast-later validation
implementation; its other decisions stand.

## Context

Validation depended on the trigger path. Watched-resource admission and HTTP
promotion used full semantic validation, while reconciliation ran `haproxy -c`
only on the first render of an iteration. Pluggable validators ran only from
the admission adapter. Deletes, HTTP refreshes, startup races, and ordinary
resource changes could therefore produce output that had never passed every
configured gate.

The first-render shortcut also treated validation as a property of an
iteration. It is a property of the rendered config and all auxiliary files.
Those files can change without restarting the iteration.

## Decision

One pipeline owns render validation. Leader reconciliation, watched-resource
admission, and HTTP-store promotion all call it with their respective store
views. For every changed output it runs, in order:

1. client-native syntax parsing;
2. version-specific OpenAPI schema validation;
3. `haproxy -c` semantic validation; and
4. every configured rendered-output validator against the complete file set.

No success event is published before all stages pass. External-validator
errors use pipeline phase `external`; warnings remain on the successful result
and reach admission responses or reconciliation observability.

Cancellation is not a verdict. The pipeline checks its authority between stages
and before returning success, and an interrupted external-validator fan-out
fails closed. A leader whose term context has ended discards the pipeline result
without publishing either success or failure events from the retired term.
The template boundary converts cancellation panics to `RenderTimeoutError`;
fatal template errors still panic while the render context remains active.

The pipeline caches successful results by a checksum over the config and
auxiliary files. Identical content may reuse that verdict. A failed result is
never cached.

Proposal admission has one narrow recovery exception: if the live baseline is
already invalid, the proposal may proceed only when both renders complete and
their output checksums are identical. A render error or any changed invalid
output is denied.

## Consequences

- Every route into publication and deployment enforces the same validation
  surface.
- Pluggable validators cover deletes, HTTP refreshes, config changes, startup,
  and ordinary reconciliation, not only admission requests.
- Changed output pays the semantic and external-validator cost. Content-cache
  hits avoid repeated work for drift checks and no-op renders.
- The Dataplane API's validation remains a second gate, not justification for
  skipping the controller gate.
