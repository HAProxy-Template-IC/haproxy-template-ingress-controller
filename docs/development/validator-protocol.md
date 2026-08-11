# Validator Sidecar Wire Protocol

## Overview

HAPTIC's admission webhook consults one or more validator sidecars to check rendered files before admitting changes. This document is the authoritative specification of the wire protocol between the controller and a validator sidecar; HAPTIC owns the contract.

A validator is an **opaque program** to the controller: it speaks this protocol over a unix socket. What it does internally — whether it parses TOML, has a plugin system, dispatches files to internal handlers, talks to remote services — is its own concern and is not described here. Concrete implementations document their internals in their own repositories. Reference implementation: [`haproxy-spoa-hub --validate-socket <path>`](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub/-/blob/main/specs/004-validate-mode/spec.md).

For end-user documentation about declaring validators on `HAProxyTemplateConfig` and operating the sidecar, see [`../site/docs/operations/pluggable-validators.md`](../site/docs/operations/pluggable-validators.md).

## Routing model (controller-side)

The controller does NOT know what's inside a validator. Routing of rendered files to validators is decided by the controller using `spec.validators[i].files` glob patterns:

```text
rendered files = controller's dry-run output (haproxy.cfg, *.map, hub TOMLs, certs, ...)

for each rendered file:
    for each configured validator:
        if any of validator.files globs match the file's path:
            send a single-file request to validator.socketPath
```

A file matching multiple validators' globs is sent to each of them. A file matching no validator's globs is not validated by any sidecar (it still flows through the existing template + HAProxy syntax dry-run).

The controller dispatches `(validator, file)` pairs **in parallel** — independent validators on different sockets validating independent files have no shared state, so there's no benefit to running them serially. Concurrency is bounded both by a top-level cap (default 16 in-flight tasks) and per-validator connection-pool ceilings.

The controller maintains a per-`(validator, file-path, content-hash)` LRU cache so unchanged files skip the round-trip entirely.

## Framing

Each frame on the socket is:

```text
| 4 bytes (big-endian, unsigned) | N bytes        |
|        length = N              |  JSON payload  |
```

- Length is an unsigned 32-bit big-endian integer.
- Maximum frame size: 8 MiB (`8 << 20` bytes). Frames exceeding the limit MUST be rejected by the producer; an oversized response causes the client to close the connection.
- The JSON payload MUST be valid UTF-8 with no BOM.

## Connections (persistent keep-alive)

Connections are persistent. A controller opens connections to a validator on demand and reuses them for many subsequent request-response cycles:

- **Sequential pipelined per connection.** Within one connection, frames are strictly ordered: client writes request *k*, validator writes response *k*, then the client writes request *k+1*. There is no out-of-order interleaving and no correlation IDs.
- **Concurrency comes from the pool.** The controller maintains a per-validator connection pool (size capped by `spec.validators[i].maxConnections`, default 4, adaptive: starts small, grows on contention, shrinks when idle). Concurrent webhook calls grab independent connections from the pool and run in parallel against the same validator.
- **Either side MAY close at inter-frame boundaries.** Validator MAY idle-close after some quiet period (recommended: 60 s) so file descriptors don't accumulate. Controller MAY close on shutdown or when shrinking the pool. Mid-frame close is a protocol violation.
- **Framing or decode errors poison the connection.** On any partial-read / oversized-frame / malformed-JSON error, the side that detected it closes the connection. Recovery happens by opening a fresh connection — never by trying to recover state on the broken one.
- **Application-level errors (malformed JSON, validation timeout, protocol-version mismatch) keep the connection open.** The validator writes a synthetic error response and waits for the next frame. Only framing failures close it.
- **Idle-close handling on the controller side.** If the validator idle-closed a connection between the client's last use and now, the first request on that connection MAY fail to write. Clients MUST tolerate this with a single transparent reconnect-and-retry; the call returns success on the retry's response. Two consecutive failures on a fresh connection is a real transport error.

The validator MUST handle multiple concurrent connections from the same controller. Implementations that only accept one connection at a time degrade the controller's pool to serial behaviour but do not break correctness.

## Request

```json
{
  "protocol_version": 1,
  "files": [
    {
      "path": "/etc/haproxy-spoa-hub/config.toml",
      "content": "[hub]\nlisten = \"0.0.0.0:9000\"\n\n[plugins.params.coraza]\ndirectives = \"SecRuleEngine On\"\n"
    }
  ]
}
```

| Field | Type | Required | Semantics |
|-------|------|----------|-----------|
| `protocol_version` | integer | yes | Currently `1`. Validators MUST reject any other value with a protocol-level error response. |
| `files` | array | yes (non-empty) | One or more files to validate. Order-preserving. The controller typically sends one file per request frame; the array is multi-element for forward compatibility. |
| `files[].path` | string | yes | Operator-facing identifier echoed back in diagnostics. The validator MUST NOT open this path on disk; it processes `content` directly. |
| `files[].content` | string | yes | UTF-8 file body. Format is whatever the validator expects for that path. |
| `files[].kind` | string | no | `"config"` (default, omitted) or `"data"`. A `data` file is one the validator must NOT validate on its own — it is sent so that a `config` file referencing it can be checked. Validators that predate this field see only `config` files, since the controller omits the key unless it is `"data"`. |
| `staged_root` | string | no | The directory the `data` files' paths are relative to, as the process that loads them sees it at runtime. A validated config references its files by runtime path (`/etc/haproxy/general/crs-*.conf`) while the request carries them under the controller's own identifiers (`general/crs-….conf`); the validator can't bridge the two on its own, and matching by path suffix would resolve a mistyped directory just as readily as the right one — so it's stated rather than guessed. Omitted when empty, so a request with no data files is byte-identical to one from before the field existed. |

The controller does not interpret file contents in any way before sending; it relays the rendered bytes verbatim.

### Data files

A validator declares `spec.validators[i].dataFiles` (glob patterns) for files it needs in order to check the files it validates. Every match is attached to **every** request sent to that validator, marked `kind: "data"`, in the same frame as the config file.

This exists because a validator sidecar runs in the **controller** pod and cannot read the HAProxy pod's filesystem. A hub config that `Include`s a WAF ruleset by path can only be checked if the ruleset's content travels with the request; otherwise the validator either reports a spurious "no such file" or — worse — silently validates a config whose referenced rules it never saw.

A file matching both `files` and `dataFiles` is treated as data: validating a reference target standalone reports on the wrong thing, and parsing a SecLang ruleset as TOML would produce a parse error rather than a finding about the config that includes it.

The result cache keys on the data files' content as well as the config file's, so a ruleset change re-validates a byte-identical config.

## Response

```json
{
  "protocol_version": 1,
  "result": "error",
  "warnings": [],
  "errors": [
    {
      "path": "/etc/haproxy-spoa-hub/config.toml",
      "line": 6,
      "column": 0,
      "message": "unknown directive \"secresquestbodyaccess\""
    }
  ]
}
```

| Field | Type | Required | Semantics |
|-------|------|----------|-----------|
| `protocol_version` | integer | yes | `1` in this version. |
| `result` | string | yes | One of `"valid"`, `"warning"`, `"error"`. Computed: `"error"` if `errors` is non-empty; else `"warning"` if `warnings` is non-empty; else `"valid"`. |
| `warnings` | array | yes (possibly empty) | List of `Diagnostic` objects with implicit `severity = warning`. |
| `errors` | array | yes (possibly empty) | List of `Diagnostic` objects with implicit `severity = error`. |

The controller recomputes `result` from the diagnostic arrays. A missing or unknown value, or a value that disagrees with those arrays, is a decode failure. The controller discards the connection, fails the current render, and does not cache that response. Return the computed value shown in the table.

A `Diagnostic` SHALL have:

| Field | Type | Required | Semantics |
|-------|------|----------|-----------|
| `path` | string | yes | The file the diagnostic refers to, matching one of the request's `files[].path` values. Protocol-level diagnostics use `path: ""`. |
| `line` | integer | yes | 1-based line number, or `0` for "unknown / file-level". |
| `column` | integer | yes | 1-based column number, or `0` for "unknown / file-level". |
| `message` | string | yes | Human-readable error message. SHOULD be self-explanatory in the `kubectl apply` context. |

## Three-result behaviour

The `result` field's three values map to three webhook outcomes:

- **`valid`** → admission allowed; no message.
- **`warning`** → admission allowed; `warnings[]` are appended to `AdmissionResponse.Warnings` so `kubectl apply` prints them as soft warnings. The resource is admitted unchanged.
- **`error`** → admission denied. `errors[]` (and any `warnings[]` for context) are formatted as the denial reason. The resource is rejected.

The aggregate result across multiple validators × multiple files is computed the same way: any error wins; any warning without errors wins; otherwise valid. The webhook always preserves the per-diagnostic `path` + `line` + `column` so the operator can pinpoint the offending file.

## Error responses (protocol-level)

These responses come from the validator framework itself, not from any internal validation logic. All have `result: "error"`, `warnings: []`, `errors: [<single diagnostic>]`. The diagnostic's `path` is empty (`""`).

| Trigger | Diagnostic message |
|---------|--------------------|
| Frame too large | `"request frame exceeds maximum size of N bytes"` (connection closed after response) |
| Malformed JSON | `"request body is not valid JSON: <reason>"` (connection MAY remain open) |
| Wrong protocol version | `"protocol version N not supported (max: 1)"` (connection MAY remain open) |
| Missing required field | `"missing required field 'files'"` etc. (connection MAY remain open) |
| Empty files array | `"'files' array must be non-empty"` (connection MAY remain open) |
| Per-request timeout exceeded | `"validation timed out after Ns"` (connection closed; mid-write timeout poisons stream state) |
| Connect refused (client-side) | `"validator <name>: connect <path>: <reason>"` |
| Decode failure (client-side) | `"validator <name>: decode response: <reason>"` |

## Versioning

The current protocol is version `1`. Future evolution rules:

- **Same protocol version**: adding optional fields to request or response.
- **New protocol version**: adding new severity levels (e.g., `info`); changing the meaning of any existing field.
- **Backward compatibility**: a v1-only validator that receives a v2 request returns the protocol-version error and closes cleanly. Clients that receive an unsupported `protocol_version` in a response close the connection and surface a transport-level error.

## Caching (client-side)

The HAPTIC controller maintains a process-local LRU cache keyed by `(validator-name, path, sha256(content))`. Cache hits skip the round-trip and return the cached response. The cache holds protocol-conforming round-trips (including responses with `result: "warning"` or `result: "error"`); it does NOT cache transport or protocol-decode failures so a transient outage or malformed response doesn't poison subsequent admissions.

The cache is process-local — a controller restart re-warms it. There is no cross-pod sharing.

This caching layer is not visible on the wire; validators can ignore it. Validators MUST be pure functions of their input (the wire-protocol contract); violating purity poisons the cache and produces stale results.

## Worked example

### Request (single file)

```text
00 00 00 D9  # 4-byte length: 217 bytes of JSON below

{"protocol_version":1,"files":[{"path":"/etc/haproxy-spoa-hub/config.toml","content":"[hub]\nlisten = \"0.0.0.0:9000\"\n\n[plugins.params.coraza]\ndirectives = \"SecRulRemoveById 942100\"\n"}]}
```

### Response

```text
00 00 00 DD  # length: 221 bytes

{"protocol_version":1,"result":"error","warnings":[],"errors":[{"path":"/etc/haproxy-spoa-hub/config.toml","line":4,"column":0,"message":"invalid WAF config from string: unknown directive \"secrulremovebyid\""}]}
```

The validator extracted the `line: 4` from its internal parser; how it does so is opaque to the controller.

## Implementation notes for new validators

A new validator (whether a haproxy-cfg validator, a third-party WAF, or anything else) must:

1. Open a unix-domain stream socket at the path declared in `spec.validators[i].socketPath`.
2. Accept multiple concurrent connections (one tokio task / goroutine per connection, or equivalent).
3. On each connection, loop on read-frame / process / write-response until the client closes, the connection goes idle past the validator's timeout, or a transport-level error poisons the byte stream.
4. Handle one request frame per cycle: read 4 bytes of length prefix, then exactly `length` bytes of JSON; reply with one length-prefixed JSON response within the per-request timeout (recommended default 5 s).
5. Process every file in `request.files[]` according to whatever internal logic the validator defines. The controller has already filtered files by glob match before sending.
6. Implement validation as a **pure function** of the input: no goroutine fan-out, no network I/O, no file I/O outside what the request carries, no global state mutation. The HAPTIC-side cache assumes purity.
7. Surface line numbers via the 1-based `line` field, columns via the 1-based `column` field, or `0` for "file-level". Self-explanatory `message` text — operators see this in `kubectl apply` denial reasons.

Conforming implementations SHOULD pass the protocol-level conformance scenarios in [`openspec/specs/pluggable-validator-sidecar/spec.md`](../../openspec/specs/pluggable-validator-sidecar/spec.md).
