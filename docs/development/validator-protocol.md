# Validator Sidecar Wire Protocol

## Overview

HAPTIC's admission webhook consults one or more validator sidecars to check the rendered hub TOML before admitting changes that affect plugin configuration. This document is the authoritative specification of the wire protocol between the controller and a validator sidecar.

The reference implementation is `haproxy-spoa-hub --validate-socket <path>` (see [haproxy-spoa-hub specs/004-validate-mode](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub/-/blob/main/specs/004-validate-mode/contracts/validate-socket-protocol.md)). Any implementation conforming to this document may be substituted; HAPTIC owns the protocol's evolution rules. The hub-side spec carries an interim-ownership disclaimer that points back here.

For end-user documentation about declaring validators on `HAProxyTemplateConfig` and operating the sidecar, see [`../controller/docs/operations/pluggable-validators.md`](../controller/docs/operations/pluggable-validators.md).

## Framing

Each frame on the socket is:

```text
| 4 bytes (big-endian, unsigned) | N bytes        |
|        length = N              |  JSON payload  |
```

- Length is an unsigned 32-bit big-endian integer.
- Maximum frame size: 1 MiB (`1 << 20` bytes). Frames exceeding the limit MUST be rejected by the producer; an oversized response causes the client to close the connection.
- The JSON payload MUST be valid UTF-8, no BOM. Decoder errors on either side are surfaced as a single error-severity diagnostic.

## Connection lifecycle

- Each accepted connection serves exactly one request-response cycle.
- The client opens the socket, writes one request frame, reads one response frame, closes the connection.
- Persistent / multiplexed connections are out of scope for this version.

## Request

```json
{
  "protocol_version": 1,
  "files": [
    {
      "path": "hub-config.toml",
      "content": "[hub]\nlisten = \"0.0.0.0:9000\"\n\n[[plugins]]\nname = \"coraza\"\nlibrary = \"libcoraza.so\"\n\n[plugins.params.coraza]\ndirectives = '''\nSecRuleEngine On\nSecRule ARGS \"@rx evil\" \"id:1001,deny\"\n'''\n"
    }
  ]
}
```

| Field | Type | Required | Semantics |
|-------|------|----------|-----------|
| `protocol_version` | integer | yes | Currently `1`. Validators MUST reject any other value. |
| `files` | array | yes (non-empty) | One or more files to validate. Order-preserving. |
| `files[].path` | string | yes | Operator-facing identifier echoed back in diagnostics. The validator MUST NOT open this path on disk; it processes `content` directly. |
| `files[].content` | string | yes | UTF-8 file body. For hub-TOML files, this is the raw TOML text. |

The validator processes each file independently. For hub-TOML files, the validator: parses TOML → structurally checks `[hub]` and `[[plugins]]` → for each `[plugins.params.<name>]` subtree, looks up the named plugin among loaded plugins and dispatches to its `validate()` → aggregates diagnostics under that file's path.

## Response

```json
{
  "protocol_version": 1,
  "result": "error",
  "warnings": [],
  "errors": [
    {
      "path": "hub-config.toml",
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
| `result` | string | yes | One of `"valid"`, `"warning"`, `"error"`. Computed: `error` if `errors` is non-empty; else `warning` if `warnings` is non-empty; else `valid`. |
| `warnings` | array | yes (possibly empty) | List of `Diagnostic` objects with implicit `severity = warning`. |
| `errors` | array | yes (possibly empty) | List of `Diagnostic` objects with implicit `severity = error`. |

Each `Diagnostic` object:

| Field | Type | Required | Semantics |
|-------|------|----------|-----------|
| `path` | string | yes | The file the diagnostic refers to, matching one of the request's `files[].path` values. Protocol-level diagnostics use `path: ""`. |
| `line` | integer | yes | 1-based line number, or `0` for "unknown / file-level". |
| `column` | integer | yes | 1-based column number, or `0` for "unknown / file-level". |
| `message` | string | yes | Human-readable error message. SHOULD be self-explanatory in the `kubectl apply` context (e.g., `"unknown directive 'secresquestbodyaccess'"` rather than `"validation error #4"`). |

## Result aggregation

When a single response carries diagnostics from multiple plugins:

- Diagnostics across all files and all plugins are collected into the warnings/errors arrays.
- Severity counts determine `result` (any error → `"error"`; any warning, no errors → `"warning"`; else `"valid"`).
- Order: file-level diagnostics (TOML parse, plugin-not-loaded) first, then per-plugin in plugin-load order.
- Within a single plugin's diagnostics, the order is what the plugin produced — plugins SHOULD return diagnostics in source order.

## Error responses (protocol-level)

These responses come from the validator framework itself, not from a plugin's `validate()`. All have `result: "error"`, `warnings: []`, `errors: [<single diagnostic>]`. The diagnostic's `path` is empty.

| Trigger | Diagnostic message |
|---------|--------------------|
| Frame too large | `"request frame exceeds maximum size of N bytes"` (connection closed after response) |
| Malformed JSON | `"request body is not valid JSON: <reason>"` |
| Wrong protocol version | `"protocol version N not supported (max: 1)"` |
| Missing required field | `"missing required field 'files'"` etc. |
| Empty files array | `"'files' array must be non-empty"` |
| Per-request timeout exceeded | `"validation timed out after Ns"` (configurable; reference implementation defaults to 5s) |
| Plugin panic | `"internal validator error in plugin <name>: <panic message>"` (sidecar continues serving subsequent requests) |
| Connect refused (client-side) | `"validator <name>: connect <path>: <reason>"` |
| Decode failure (client-side) | `"validator <name>: decode response: <reason>"` |

## Versioning

The current protocol is version `1`. Future evolution rules:

- **Same protocol version**: adding optional fields to request or response.
- **New protocol version**: adding new severity levels (e.g., `info`); changing the meaning of any existing field.
- **Backward compatibility**: a v1-only validator that receives a v2 request returns the protocol-version error and closes cleanly. Clients that receive an unsupported `protocol_version` in a response close the connection and surface a transport-level error.

## Caching (client-side)

The HAPTIC controller maintains a process-local LRU cache keyed by `sha256(validator-name || request-body)`. Cache hits skip the round-trip and return the cached response byte-for-byte. The cache holds successful round-trips (including warning/error responses); it does NOT cache transport-level failures so a transient sidecar outage doesn't poison subsequent admissions.

The cache is process-local — a controller restart re-warms it. There is no cross-pod sharing.

This caching layer is not visible on the wire; validators can ignore it. Validator implementations MUST NOT rely on hidden state (the wire-protocol contract requires `validate()` to be a pure function of its inputs); violating purity poisons the cache and produces stale results.

## Worked example

### Request

```text
00 00 00 D6  # 4-byte length: 214 bytes of JSON below

{"protocol_version":1,"files":[{"path":"hub-config.toml","content":"[hub]\nlisten = \"0.0.0.0:9000\"\n\n[[plugins]]\nname = \"coraza\"\nlibrary = \"libcoraza.so\"\n\n[plugins.params.coraza]\ndirectives = '''\nSecRulRemoveById 942100\n'''\n"}]}
```

### Response

```text
00 00 00 D2  # length: 210 bytes

{"protocol_version":1,"result":"error","warnings":[],"errors":[{"path":"hub-config.toml","line":11,"column":0,"message":"invalid WAF config from string: unknown directive \"secrulremovebyid\""}]}
```

The `line: 11` corresponds to the line within the embedded TOML file at which the bad SecLang directive appears. The Coraza validator extracts that line number from the WAF parser's structured logs (see [`haproxy-spoa-hub-plugin-coraza/specs/003-validate-override`](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub-plugin-coraza/-/blob/main/specs/003-validate-override/spec.md)).

## Implementation notes for new validators

A new validator (whether a haproxy-cfg validator, a third-party WAF, or anything else) must:

1. Open a unix-domain stream socket at the path declared in `spec.validators[i].socketPath`.
2. Accept one connection per request; read exactly 4 bytes of length prefix, then exactly `length` bytes of JSON.
3. Reply with a length-prefixed JSON response within the per-request timeout (default 5s).
4. Close the connection after writing the response.
5. Implement validate as a **pure function** of the input: no goroutine fan-out, no network I/O, no file I/O outside what the request carries, no global state mutation. The HAPTIC-side cache assumes purity.
6. Surface line numbers via 1-based `line` field, columns via 1-based `column` field, or `0` for "file-level". Self-explanatory `message` text — operators see this in `kubectl apply` denial reasons.

Conforming implementations SHOULD pass the protocol-level conformance scenarios in [`openspec/specs/pluggable-validator-sidecar/spec.md`](../../openspec/specs/pluggable-validator-sidecar/spec.md).
