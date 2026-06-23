# Pluggable Validator Sidecar

A sidecar-based validation pipeline. The controller dispatches rendered files to one or more validator sidecars over a unix socket using a length-prefixed JSON wire protocol; sidecars return line-numbered diagnostics that the admission webhook surfaces to the operator.

The validator on the other side of the socket is opaque to the controller — it speaks the protocol and returns diagnostics. This capability defines the wire protocol, controller-side glob routing, the per-(validator, file, content) result cache, persistent keep-alive connections with an adaptive pool, parallel dispatch, the CRD field shape, and the wire-protocol document. It does NOT define the chart-side sidecar wiring (separate capability `pluggable-validator-chart`) nor the webhook-side diagnostic surfacing (separate capability `pluggable-validator-webhook-wiring`).

## ADDED Requirements

### Requirement: Validators CRD Field

The `HAProxyTemplateConfig` resource SHALL accept an optional `spec.validators` array. Each entry SHALL declare:

- `name` (required, RFC 1123 label, unique across the array).
- `socketPath` (required, absolute filesystem path to the validator's Unix domain socket inside the controller pod).
- `files` (required, non-empty list of glob patterns following Go `path/filepath.Match` semantics; absolute paths only). Patterns are matched against rendered file paths to decide which files to send to this validator.
- `timeoutMs` (optional, positive integer milliseconds, range 1–60000, default 5000). Per-call deadline covering one (file, validator) round-trip.
- `maxConnections` (optional, positive integer, range 1–32, default 4). Cap on the controller's adaptive connection pool to this validator.

The empty `validators` array (or absent field) SHALL leave the controller's behaviour unchanged from before this capability — no validator sidecar is consulted.

#### Scenario: HAProxyTemplateConfig with validators field omitted

WHEN an HAProxyTemplateConfig is admitted with no `spec.validators` field
THEN the controller SHALL behave identically to the pre-feature behaviour: no validator socket is consulted.

#### Scenario: HAProxyTemplateConfig with one validator

WHEN an HAProxyTemplateConfig is admitted with `spec.validators` containing one entry whose `files` glob matches a rendered file
THEN the controller SHALL forward that file to the validator's socket and surface the response in the admission webhook outcome.

#### Scenario: HAProxyTemplateConfig with empty files list

WHEN a `spec.validators[i].files` is empty
THEN the CRD's OpenAPI schema SHALL reject the resource at admission time. A validator with no globs would never be consulted, so the configuration is meaningless.

#### Scenario: Bad glob syntax

WHEN a `spec.validators[i].files[j]` contains malformed glob syntax (e.g. unclosed `[`)
THEN the controller's Manager construction SHALL fail and the iteration SHALL surface the validator's name + the offending pattern in the error message.

### Requirement: Glob-Based File Routing

For each rendered file produced by the dry-run, the controller SHALL match the file's path against each validator's `files` glob list. A file matching at least one of a validator's globs SHALL be sent to that validator. A file matching multiple validators' globs SHALL be sent to each matching validator independently. A file matching no validator's globs SHALL NOT be sent to any sidecar.

The controller MUST treat the validator program as opaque — routing decisions are made entirely controller-side from the configured globs; the validator is not consulted about which files it wants.

#### Scenario: File matching one validator's glob

GIVEN validator `v1` configured with `files: ["/etc/x/*.toml"]`
AND a rendered file at path `/etc/x/config.toml`
WHEN ValidateAll runs
THEN `v1` SHALL receive a request frame containing exactly that file.

#### Scenario: File matching no validator's glob

GIVEN validator `v1` configured with `files: ["/etc/x/*.toml"]`
AND a rendered file at path `/etc/y/other.yaml`
WHEN ValidateAll runs
THEN no validator SHALL receive that file.

#### Scenario: File matching multiple validators' globs

GIVEN validators `v1` and `v2` both configured with `files: ["/etc/x/*.toml"]`
AND a rendered file at path `/etc/x/config.toml`
WHEN ValidateAll runs
THEN both `v1` and `v2` SHALL receive the file in independent request frames; their diagnostics SHALL be aggregated.

### Requirement: Wire Protocol Framing

The wire format between the controller and a validator sidecar SHALL be length-prefixed JSON:

```text
| 4 bytes (big-endian, unsigned) | N bytes        |
|        length = N              |  JSON payload  |
```

Length is an unsigned 32-bit big-endian integer. The JSON payload SHALL be valid UTF-8 with no BOM. Maximum frame size is 1 MiB by default.

#### Scenario: Frame decoded by its length prefix

WHEN a peer reads a frame
THEN it SHALL read exactly 4 bytes as the big-endian unsigned length N, then read exactly N bytes as the UTF-8 JSON payload, and a declared length exceeding the 1 MiB maximum SHALL be rejected as a framing error that closes the connection.

### Requirement: Persistent Keep-Alive Connections

Connections between the controller and a validator SHALL be persistent. A controller opens a connection to a validator on first demand and reuses it for many subsequent request-response cycles. The validator MUST honor the keep-alive contract:

- Each connection serves an unbounded number of sequential request-response cycles.
- Within one connection, frames are strictly ordered (no interleaving, no correlation IDs).
- The validator MAY close idle connections after a server-side timeout (recommended ≥ 30 s); the controller MUST tolerate this with a transparent reconnect-and-retry on the next request.
- Either side MAY close at inter-frame boundaries; mid-frame close is a protocol violation.
- Application-level errors (malformed JSON, validation timeout, protocol-version mismatch) SHALL keep the connection open for the next frame; only framing failures close it.

The validator MUST handle multiple concurrent connections from the same controller. Implementations that only accept one connection at a time degrade the controller's pool to serial behaviour but do not break correctness.

#### Scenario: Multiple requests on one connection

WHEN the controller writes request *k* on a connection, reads response *k*, then writes request *k+1* on the same connection
THEN the validator SHALL respond to request *k+1* without closing the connection, in arrival order.

#### Scenario: Server idle-close on first reuse

WHEN the controller's pool returns a connection that the validator has since idle-closed
AND the controller writes a request frame on it
THEN the controller SHALL detect the closed write/read, transparently reconnect, retry the request once, and surface the response to the caller as if no failure occurred.

### Requirement: Adaptive Connection Pool

The controller SHALL maintain a per-validator connection pool with the following adaptive shape:

- The pool starts empty (no connections open).
- On `Validate`, the pool prefers a free idle connection if any. If the pool is empty AND has headroom, it dials a new connection and adds it to in-flight count.
- The pool size is bounded by `spec.validators[i].maxConnections` (default 4). Acquires that find no free connection and no headroom block briefly until one is released.
- Connections idle past 30 s are closed and replaced lazily on next acquire.
- Connections that error on read/write are discarded (not returned to the pool); the next acquire opens a replacement.

#### Scenario: Pool grows on contention up to MaxConnections

GIVEN a validator configured with `maxConnections: 4`
AND four concurrent calls to ValidateAll all matching files for this validator
WHEN the calls run
THEN the controller SHALL open up to 4 connections, each call gets one, and no caller blocks past the per-call timeout.

#### Scenario: Pool shrinks on idleness

GIVEN a validator with multiple open connections
WHEN no request has been issued for ≥ 30 s on a particular connection
THEN that connection SHALL be closed on next acquire and replaced lazily.

### Requirement: Request Schema

A request SHALL be a JSON object with the following fields:

- `protocol_version` (integer, required): currently `1`.
- `files` (array, required, non-empty): each entry SHALL have `path` (string, the operator-facing identifier echoed in diagnostics) and `content` (string, the file's UTF-8 text).

The validator MUST NOT open `path` from disk — it processes `content` directly. `path` is only an identifier echoed back in diagnostics.

#### Scenario: Wire-format request

WHEN the controller encodes a request with one `files` entry containing a 200-byte payload and writes it to the socket
THEN the receiving server SHALL read 4 bytes for length, then `length` bytes for the JSON, then have exactly the bytes the controller encoded — no trailing data.

### Requirement: Response Schema and Three-Result Semantics

A response SHALL be a JSON object with the following fields:

- `protocol_version` (integer, always present): `1` in this version.
- `result` (string, always present): one of `"valid"`, `"warning"`, `"error"`. Computed as: `"error"` if `errors` is non-empty; else `"warning"` if `warnings` is non-empty; else `"valid"`.
- `warnings` (array, always present, possibly empty): list of `Diagnostic` objects with `severity = Warning`.
- `errors` (array, always present, possibly empty): list of `Diagnostic` objects with `severity = Error`.

The webhook caller maps the three results to admission outcomes:

- `valid` → admission allowed; no message.
- `warning` → admission allowed; warnings populated in `AdmissionResponse.Warnings` so `kubectl apply` prints them as soft warnings; resource admitted unchanged.
- `error` → admission denied; errors formatted as the denial reason; warnings appended for context.

#### Scenario: Warning result allows admission

WHEN a validator returns `result: "warning"` with one warning diagnostic
THEN the webhook SHALL admit the resource AND populate `AdmissionResponse.Warnings` with the diagnostic message AND not deny.

### Requirement: Result Cache

The controller SHALL maintain a process-local LRU cache of validator responses keyed by `(validator-name, path, sha256(content))`. Cache hits skip the socket round-trip and return the cached `Response` byte-for-byte. Default capacity SHALL be 256 entries. Eviction SHALL be by least-recently-used insertion order.

The cache SHALL NOT memoise transport-level (synthetic) failures; only real validator responses (including warning- and error-severity ones) SHALL be cached. The wire-protocol contract requires validator output to be a pure function of its input, so caching real responses is correct.

#### Scenario: Repeat call returns cached response

WHEN the controller calls `ValidateAll` for a (validator, path, content) tuple already in the cache
THEN the cache SHALL return the cached `Response` without opening the socket.

#### Scenario: Different content produces a cache miss

WHEN the controller calls `ValidateAll` for a tuple where `content` differs even by one byte from a previously-cached entry
THEN the cache SHALL miss and the request SHALL go over the socket.

#### Scenario: Different validator produces a cache miss

WHEN the controller calls `ValidateAll` for the same `(path, content)` but a different `validator-name`
THEN the cache SHALL miss. Validators with the same content key MUST NOT share cache entries.

#### Scenario: Synthetic ProtocolError responses NOT cached

GIVEN a validator whose socket is unreachable
WHEN ValidateAll calls it twice with identical content
THEN both calls SHALL hit the network (or fail to dial); the synthetic error response SHALL NOT be cached.

### Requirement: Parallel Dispatch

The controller SHALL dispatch `(validator, file)` round-trips in parallel rather than serially. The maximum number of concurrent in-flight dispatch tasks SHALL be capped (default 16) to avoid pathological goroutine counts on extremely large renders. Per-validator connection-pool ceilings further throttle within-validator concurrency.

#### Scenario: Three slow validators run in parallel

GIVEN three validators each with a 200 ms response delay configured with the same glob
AND a single rendered file that matches all three
WHEN ValidateAll runs
THEN total wall-clock latency SHALL be ~200 ms + dispatch overhead, NOT 3 × 200 ms.

#### Scenario: Diagnostics sorted deterministically

WHEN ValidateAll completes with multiple diagnostics from concurrent dispatch
THEN the returned outcome's `Warnings` and `Errors` slices SHALL be sorted by `(path, line, column, message)` so output is stable across runs.

### Requirement: Manager Health Check

The Manager SHALL expose `Healthy() (ok bool, failures []string)` summarising the writability of every configured validator socket. Each socket SHALL be checked by `os.Stat` (path exists), mode-test (`mode & os.ModeSocket != 0`), and a non-blocking unix dial. Each check SHALL complete in under 1 ms in the happy path. Failed checks SHALL appear in `failures` as `"<validator-name>: <reason>"`. The `ok` boolean SHALL be `false` if any failure is recorded.

The `Healthy()` callable SHALL be exposed for `/healthz` injection. This requirement does NOT define the `/healthz` wiring itself; that's the `pluggable-validator-webhook-wiring` capability.

#### Scenario: Healthy reports per-socket failures

GIVEN one validator whose `socketPath` is a writable unix socket and a second whose `socketPath` does not exist
WHEN `Healthy()` is called
THEN it SHALL return `ok = false` and a `failures` entry of the form `"<second-validator-name>: <reason>"` naming the missing socket, while the writable validator contributes no failure entry.

### Requirement: Authoritative Wire-Protocol Document

The repository SHALL host the authoritative wire-protocol document at `docs/development/validator-protocol.md`. The hub-side spec at `haproxy-spoa-hub/specs/004-validate-mode/contracts/validate-socket-protocol.md` is a one-line pointer to this document.

The HAPTIC-side document SHALL describe: framing, persistent connections with idle/poison semantics, adaptive pool semantics, request and response schemas, three-result behavior, parallel dispatch, error responses, versioning rules, and a worked example. It SHALL NOT describe the validator program's internals (plugins, dispatch logic, parser implementation) — those belong to each implementation's own repo.

#### Scenario: Protocol document is present and authoritative

WHEN the wire-protocol contract is consulted
THEN the authoritative document SHALL exist at `docs/development/validator-protocol.md` covering framing, connection/pool semantics, request/response schemas, the three-result behavior, parallel dispatch, error responses, and versioning, AND the hub-side spec SHALL reference it rather than redefining the protocol.
