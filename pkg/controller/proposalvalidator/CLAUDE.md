# Proposal validation

`Service` is the validation component; it has no event or lifecycle dependencies. `Component` is its event adapter and owns subscription, request correlation, response publication, and panic recovery.

Preserve the two policies: admission can accept unchanged invalid output after an exact baseline comparison; HTTP-content promotion rejects invalid output. Keep cancellation checks and the shared published-file snapshot across admission's proposed and baseline renders.

Production constructs separate pipelines for admission and HTTP-content validation. Keep that isolation when changing the wiring in `pkg/controller/webhook.go` and `pkg/controller/reconciliation.go`.

Admission render failures are checked once against fresh API-backed stores when configured. Preserve selectors, indexing, overlays, full output validation, cancellation, and the pinned published-file baseline; never mutate informer stores. Successful cached validation makes no additional API requests.
