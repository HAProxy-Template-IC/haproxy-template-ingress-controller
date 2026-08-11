# pkg/controller/webhook

The webhook adapter resolves watched-resource rules to GroupVersionKinds and
routes AdmissionReview requests from `pkg/webhook.Server` through the
synchronous dry-run validation pipeline.

## Lifecycle

The controller creates one process-owned HTTPS listener and passes it through
`Config.Server`. Each iteration atomically installs a complete validator
generation. Teardown replaces it with an empty fail-closed generation and waits
for active requests before releasing iteration dependencies. The listener stays
bound for the next iteration.

When `Config.Server` is nil, the component creates, serves, shuts down, and
joins its own server. Tests use this owned mode with fixed certificate bytes.

## Configuration

| Field | Purpose |
|---|---|
| `Server` | Process-owned server to adopt. Omit only when the component should own the listener. |
| `Port`, `Path` | Owned-server address settings; defaults are `9443` and `/validate`. |
| `CertDir` | Directory containing mounted `tls.crt` and `tls.key` for reloadable certificates. |
| `CertPEM`, `KeyPEM` | Fixed certificate bytes used when `CertDir` is empty. |
| `Rules` | Watched resources to validate, normally from `ExtractWebhookRules`. |
| `DryRunValidator` | Synchronous `ValidateDirect` implementation; required when `Rules` isn't empty. |
| `ResourceAdmissionTimeout` | Inner validation deadline, default `9s`. |
| `OnGenerationRetired` | Releases state captured by an adopted server's retired table. |

`restMapper` resolves each rule's GroupVersionResource to a GroupVersionKind.
The optional metrics recorder receives request duration and allow, deny, or
unregistered outcomes.

## Validation flow

`Start` resolves all rules and installs their validators as one generation. A
request then passes structural checks and runs `DryRunValidator.ValidateDirect`
against selector-aware overlays for every configured alias of the request GVR. The returned warnings are
included in the AdmissionResponse.

Missing validators and canceled iteration validators deny with status 503. The
chart's watched-resource rules use `failurePolicy: Fail` and a 10-second timeout,
one second longer than the controller deadline. The chart doesn't create a
`HAProxyTemplateConfig` webhook; complete config sets are validated through the
ADR-0016 preflight and load gates.

See [`pkg/webhook`](../../webhook/),
[`pkg/controller/dryrunvalidator`](../dryrunvalidator/), and
[`pkg/controller/proposalvalidator`](../proposalvalidator/).
