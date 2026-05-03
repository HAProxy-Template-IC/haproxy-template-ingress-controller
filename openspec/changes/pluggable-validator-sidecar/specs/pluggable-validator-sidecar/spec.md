# Pluggable Validator Sidecar

A sidecar-based validation pipeline. The controller dispatches rendered plugin TOML to one or more validator sidecars (running `haproxy-spoa-hub --validate-socket` or any conforming implementation) over a unix socket using a length-prefixed JSON wire protocol; sidecars return line-numbered diagnostics that the admission webhook surfaces to the operator.

This capability defines the wire protocol, the controller-side client, the result cache, the CRD field shape, and the event integration points. It does NOT define the chart-side sidecar wiring (separate capability `pluggable-validator-chart`) nor the webhook-side diagnostic surfacing (separate capability `pluggable-validator-webhook-wiring`).

## ADDED Requirements

### Requirement: Validators CRD Field

The `HAProxyTemplateConfig` resource SHALL accept an optional `spec.validators` array. Each entry SHALL declare a `name` (required, RFC 1123 label, unique across the array), a `socketPath` (required, absolute filesystem path), a `plugins` array (optional, list of `[plugins.params.<name>]` subtree names this validator handles, defaulting to the empty list which means "validate the whole hub TOML"), and a `timeoutMs` (optional, positive integer in milliseconds, defaulting to 5000).

The empty `validators` array (or absent field) SHALL leave the controller's behaviour unchanged from before this capability — no validator sidecar is consulted. This preserves backward compatibility for operators who do not opt in.

#### Scenario: HAProxyTemplateConfig with validators field omitted

WHEN an HAProxyTemplateConfig is admitted with no `spec.validators` field
THEN the controller SHALL behave identically to the pre-feature behaviour: no `PluggableValidationRequest` events are published.

#### Scenario: HAProxyTemplateConfig with one validator

WHEN an HAProxyTemplateConfig is admitted with `spec.validators` containing one entry
THEN the controller SHALL create one validator-client instance bound to the entry's socket path with the entry's timeout. No sockets are opened until a validation request is dispatched (lazy connection).

#### Scenario: HAProxyTemplateConfig with duplicate validator names

WHEN an HAProxyTemplateConfig is admitted with two `spec.validators` entries sharing the same `name`
THEN the CRD's OpenAPI schema SHALL reject the resource at admission time with a clear duplicate-name diagnostic. The controller MUST NOT see this resource.

#### Scenario: timeoutMs out of range

WHEN an HAProxyTemplateConfig declares `timeoutMs: 0` or `timeoutMs: -1` on a validator entry
THEN the CRD's OpenAPI schema SHALL reject the resource as invalid (`timeoutMs MUST be > 0`).

### Requirement: Wire Protocol Framing

The wire format between the controller and a validator sidecar SHALL be length-prefixed JSON:

```text
| 4 bytes (big-endian, unsigned) | N bytes        |
|        length = N              |  JSON payload  |
```

Length is an unsigned 32-bit big-endian integer. The JSON payload SHALL be valid UTF-8 with no BOM. Maximum frame size is 1 MiB by default; sizes exceeding the limit SHALL be rejected by the producer (the controller MUST NOT send oversized frames; an oversized response causes the client to return a synthetic frame-too-large error).

Each accepted connection serves exactly one request-response cycle. The client opens the socket, writes one request frame, reads one response frame, closes. Persistent / multiplexed connections are out of scope.

#### Scenario: Round-trip a small request

WHEN the client encodes a request with one `files` entry containing a 200-byte TOML and writes it to the socket
THEN the receiving server SHALL read 4 bytes for length, then `length` bytes for the JSON, then have exactly the bytes the client encoded — no trailing data.

#### Scenario: Reject oversized request before send

WHEN the client is asked to encode a request whose JSON exceeds `MaxFrameSize`
THEN the encoder SHALL return an error and SHALL NOT write any bytes to the socket.

#### Scenario: Reject oversized response on receive

WHEN the server returns a response frame with `length > MaxFrameSize`
THEN the client SHALL close the connection without reading the body and return a synthetic error-severity `Diagnostic` with `path: ""` and a message identifying the size violation.

### Requirement: Request Schema

A request SHALL be a JSON object with the following fields:

- `protocol_version` (integer, required): currently `1`.
- `files` (array, required, non-empty): each entry SHALL have `path` (string, the operator-facing identifier echoed in diagnostics) and `content` (string, the file's UTF-8 text).

The validator MUST NOT open `path` from disk — it processes `content` directly. `path` is only an identifier echoed back in diagnostics.

#### Scenario: Request with empty files array

WHEN a request would be encoded with `files: []`
THEN the encoder SHALL return an error before sending — empty arrays are rejected client-side.

#### Scenario: Request with unsupported protocol_version

WHEN a server receives `protocol_version: 2`
THEN the server SHALL respond with a single error-severity diagnostic `path: "" line: 0 column: 0 message: "protocol version 2 not supported (max: 1)"` and close the connection.

### Requirement: Response Schema

A response SHALL be a JSON object with the following fields:

- `protocol_version` (integer, always present): `1` in this version.
- `result` (string, always present): one of `"valid"`, `"warning"`, `"error"`. Computed as: `"error"` if `errors` is non-empty; else `"warning"` if `warnings` is non-empty; else `"valid"`.
- `warnings` (array, always present, possibly empty): list of `Diagnostic` objects with `severity = Warning`.
- `errors` (array, always present, possibly empty): list of `Diagnostic` objects with `severity = Error`.

A `Diagnostic` SHALL have `path`, `line` (1-based, `0` for unknown), `column` (1-based, `0` for unknown), and `message` (human-readable, self-explanatory in `kubectl apply` context). Protocol-level diagnostics (frame errors, version mismatch, missing fields, timeout, plugin panic) use `path: ""`.

#### Scenario: Response field consistency

WHEN the response carries 0 warnings and 0 errors
THEN `result` SHALL equal `"valid"`.

WHEN the response carries 0 warnings and 1+ errors
THEN `result` SHALL equal `"error"`.

WHEN the response carries 1+ warnings and 0 errors
THEN `result` SHALL equal `"warning"`.

#### Scenario: Diagnostic line for file-level error

WHEN the validator reports a problem that has no specific source line (e.g., "directives field is required")
THEN the `Diagnostic` SHALL set `line: 0 column: 0`.

### Requirement: Result Cache

The controller SHALL maintain a process-local LRU cache of validator responses keyed by `sha256(validator-name || request-content)`. Cache hits skip the socket round-trip and return the cached `Response` byte-for-byte. Default capacity SHALL be 256 entries. Eviction SHALL be by least-recently-used insertion order. The cache is process-local and re-warms after restart.

#### Scenario: Repeat request returns cached response

WHEN the controller calls `Validate(ctx, content)` for a `(validator, content)` pair already in the cache
THEN the cache SHALL return the cached `Response` without opening the socket.

#### Scenario: Different content produces a cache miss

WHEN the controller calls `Validate(ctx, content)` for a `(validator, content)` pair where `content` differs even by one byte from a previously-cached entry
THEN the cache SHALL miss and the request SHALL go over the socket.

#### Scenario: Different validator produces a cache miss

WHEN the controller calls `Validate(ctx, content)` for the same `content` but a different `validator-name` than a cached entry
THEN the cache SHALL miss. Validators with the same content key MUST NOT share cache entries.

#### Scenario: Capacity-bounded eviction

WHEN the cache reaches its capacity and a new entry is inserted
THEN the least-recently-used entry SHALL be evicted before the new entry is recorded.

### Requirement: Event Adapter

The controller SHALL provide a `pluggablevalidator.Component` event adapter that wraps the cache and clients. The component SHALL subscribe to `events.PluggableValidationRequest`, dispatch via the cache + client chain, and publish a `events.PluggableValidationResponse` carrying the resulting `Response`. On client error, the published response SHALL have `result: "error"` and a single error-severity diagnostic identifying the failure (validator name, error class — `connection refused`, `timeout`, `protocol error`, `plugin panic`).

The component SHALL register itself on the EventBus during construction (per the repo's "subscribe in New, not in Start" convention to avoid startup races) and consume only events naming a `validator` field present in its known-validators set.

#### Scenario: Request for unknown validator

WHEN the bus emits a `PluggableValidationRequest` whose `validator` field does not match any of the component's configured validators
THEN the component SHALL ignore the event (no response published). The publishing component is responsible for filtering before publishing — this is a misconfiguration guard, not a routing layer.

#### Scenario: Validator socket unreachable

WHEN the component receives a `PluggableValidationRequest` for a known validator whose socket is unreachable (file missing, permission denied, connection refused)
THEN the component SHALL publish a `PluggableValidationResponse` with `result: "error"`, `errors` containing one `Diagnostic` with `path: ""`, `line: 0`, `column: 0`, and a message naming the validator + the failure class. No exception SHALL propagate.

### Requirement: Client Health Check

The component SHALL expose `Healthy() (ok bool, failures []string)` summarising the writability of every configured validator socket. Each socket SHALL be checked by `os.Stat` (path exists), mode-test (`mode & os.ModeSocket != 0`), and `os.OpenFile(path, os.O_WRONLY, 0)` followed by close. Each check SHALL complete in under 1ms in the happy path. Failed checks SHALL appear in `failures` as `"<validator-name>: <reason>"`. The `ok` boolean SHALL be `false` if any failure is recorded.

The `Healthy()` callable SHALL be exposed for the next change (`pluggable-validator-webhook-wiring`) to inject into the introspection server's `/healthz` health-checker callback. This change does NOT itself wire `/healthz`; it only provides the callable.

#### Scenario: All sockets writable

WHEN every configured socket exists, is a unix socket, and accepts an `O_WRONLY` open
THEN `Healthy()` SHALL return `(true, nil)`.

#### Scenario: One socket missing

WHEN one of the configured sockets does not exist as a file
THEN `Healthy()` SHALL return `(false, ["<validator-name>: socket file does not exist"])`.

#### Scenario: One socket exists but is a regular file

WHEN one of the configured paths exists but is a regular file (not a socket)
THEN `Healthy()` SHALL return `(false, ["<validator-name>: path is not a unix socket"])`.

### Requirement: Authoritative Wire-Protocol Document

The repository SHALL host the authoritative wire-protocol document at `docs/development/validator-protocol.md`. The hub-side spec at `haproxy-spoa-hub/specs/004-validate-mode/contracts/validate-socket-protocol.md` SHALL be reduced to a one-line pointer in a follow-up MR; the disclaimer in that file already names HAPTIC as the long-term owner.

The HAPTIC-side document SHALL describe: framing, connection lifecycle, request schema, response schema, error responses, versioning rules, and a worked example. It SHALL include HAPTIC-side framing (caller, LRU cache, fail-closed semantics) that the hub-side spec does not.

#### Scenario: Hub-side pointer points at the HAPTIC URL

WHEN a developer follows the link in the hub-side spec
THEN they SHALL land at `docs/development/validator-protocol.md` in this repository, which carries the canonical contract.
