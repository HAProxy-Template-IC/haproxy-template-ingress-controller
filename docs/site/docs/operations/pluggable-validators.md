# Pluggable Validators

## Overview

You declare one or more validator sidecars in `spec.validators`, each pointing at a Unix domain socket inside the controller pod and listing file glob patterns. Before publication or deployment, the controller sends each rendered file to every validator whose globs match. An error blocks publication and deployment. For admission requests, line-numbered diagnostic logs also appear in the admission response, so `kubectl apply` identifies the offending row.

HAProxy doesn't interpret every auxiliary file. For example, the SPOA hub
validator checks its TOML configuration and embedded WAF directives. The
controller sends matching files over a Unix socket and uses the returned
diagnostic logs. Implementations follow the
[validator wire protocol](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/development/validator-protocol.md).

## How it works

Each sidecar receives matching files over a Unix socket shared through an
`emptyDir` volume. It returns diagnostics for the controller to aggregate:

- **Valid:** continue.
- **Warning:** continue and include warnings in admission responses.
- **Error:** stop publication and deployment, and deny admission requests.

Use these validators for auxiliary formats such as SPOA hub TOML, including
Coraza WAF and OpenID Connect (OIDC) settings. HAProxy configuration itself is
checked by `haproxy -c`.

## Configuration

### Declare validators on `HAProxyTemplateConfig`

```yaml
apiVersion: haproxy-haptic.org/v1alpha1
kind: HAProxyTemplateConfig
metadata:
  name: example
spec:
  # ... other fields ...
  validators:
    - name: spoa-hub
      socketPath: /var/run/haptic-validators/spoa-hub.sock
      files:
        - "/etc/haproxy-spoa-hub/*.toml"
      timeoutMs: 5000
      maxConnections: 4
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | RFC 1123 label, unique across the array. Surfaces in diagnostic logs so operators can identify which validator rejected a render. |
| `socketPath` | yes | Absolute path inside the controller pod to the validator's Unix domain socket. The chart-rendered shared `emptyDir` mounts at `/var/run/haptic-validators/`. |
| `files` | yes | List of glob patterns matched against rendered file paths to decide which files to send to this validator. Patterns follow Go's `path/filepath.Match` rules and must use the same relative or absolute form as the rendered path. Malformed patterns are rejected during config validation. At least one entry is required. |
| `dataFiles` | no | Glob patterns for files this validator needs in order to check the files it validates, but must not validate on its own. Every match is attached to **every** request sent to this validator, marked `kind: "data"`, in the same frame as the config file. A file matching both `files` and `dataFiles` is treated as data. Same glob rules as `files`. |
| `timeoutMs` | no | Per-call deadline in milliseconds covering one (file, validator) round-trip (acquire + write + read). Defaults to 5000. Range: 1–60000. |
| `maxConnections` | no | Cap on the controller's connection pool to this validator. Defaults to 4. Range: 1–32. Connections open on demand and close after an idle period. |

### Routing examples

A validator declared as

```yaml
- name: spoa-hub
  files: ["/etc/haproxy-spoa-hub/*.toml"]
```

receives any rendered file whose path matches `/etc/haproxy-spoa-hub/*.toml` (for example `config.toml`, `extra.toml`) — but **not** files outside that directory (`/etc/haproxy/maps/host.map`).

Two validators can claim overlapping globs:

```yaml
- name: spoa-hub-config
  files: ["/etc/haproxy-spoa-hub/config.toml"]
- name: spoa-hub-syntax-check
  files: ["/etc/haproxy-spoa-hub/*.toml"]
```

The `config.toml` file is sent to both validators in parallel, and their diagnostics are combined.

A file that matches no validator's globs isn't validated by any sidecar; it still flows through the existing template + HAProxy syntax dry-run.

### Chart wiring (default)

The chart's validator sidecar **auto-enables** whenever you have a SPOA hub plugin turned on. The shipped default is `controller.validators.enabled: null`, which derives the sidecar's state from the SPOA hub. Enable a plugin and the validator comes with it:

```yaml
# values.yaml
controller:
  validators:
    enabled: null  # default: auto-derive from the SPOA hub sidecar
```

When on, this adds one sidecar container to the controller pod, an `emptyDir` volume mounted at `/var/run/haptic-validators/`, and a default `spec.validators` entry pointing at the sidecar's socket with appropriate file globs.

Set `enabled` explicitly only to override the auto-derive:

```yaml
# values.yaml
controller:
  validators:
    enabled: true   # force the sidecar on even with no SPOA hub plugins
                    # (useful for bench/test setups validating template fragments)
    # enabled: false  # force the sidecar off even when a plugin is enabled
```

For custom validator implementations or multiple sidecars, see "Custom validators" below.

## Operations

### Three-result behaviour

Validators return one of three outcomes per file. The pipeline maps them as follows:

| Validator `result` | Pipeline outcome | Admission outcome |
|---|---|---|
| `valid` | Continue | Admission completes normally. |
| `warning` | Continue and record the warning count | `kubectl apply` prints each warning as a soft warning. |
| `error` | Stop before publication or deployment | `kubectl apply` prints the formatted errors and rejects the resource. |

When multiple validators check the same file (or different files), all their diagnostic logs are aggregated. The aggregate `result` is computed the same way: any error wins; any warning without errors wins; otherwise valid.

### `/healthz` integration

The controller's `/healthz` endpoint stat()s and then briefly dials every configured validator socket on every probe. A failed check (socket missing, wrong file type, connection refused) returns HTTP 503 with a structured failure list:

```json
{
  "healthy": false,
  "components": {
    "controller": {"healthy": true},
    "pluggable-validators": {
      "healthy": false,
      "error": "spoa-hub: dial: connection refused"
    }
  }
}
```

The chart probes the controller at `/healthz`. Repeated liveness failures restart the controller container. Kubernetes restarts a crashed validator container separately; a controller restart doesn't restart the validator. Check both containers when diagnosing a persistent socket failure.

### Validation execution

Each matching protocol-v1 validator receives a request on every applicable
validation run, including repeated output. Persistent connections reuse
transport, not validation results.

### Connection pooling and parallelism

The controller maintains a per-validator connection pool of persistent keep-alive connections. The pool starts small (no open connections) and **adapts to load**: it dials a new connection when an in-flight call finds the pool empty and there's headroom; it closes connections that sit idle for ~30 seconds. The cap is `spec.validators[i].maxConnections` (default 4).

`(validator, file)` pairs run **in parallel** — independent validators on different sockets validating independent files. Top-level concurrency is capped at 16 in-flight tasks; each validator's individual pool further throttles within-validator concurrency.

For a typical webhook call with one validator and a handful of matched files, the dispatch finishes about as fast as the slowest file's validation latency. Sequential single-file latency is the worst case (when `maxConnections=1`).

### Failure modes

| What | What HAPTIC does |
|------|------------------|
| Validator socket missing | The pipeline fails with `validator <name>: connect <path>: no such file or directory`; the last-good output remains active. Admission denies the request. |
| Validator returns an error response | The pipeline fails with the validator's message and row + column. |
| Validator returns a warning response | The pipeline continues. Admission surfaces the warning through `AdmissionResponse.Warnings`. |
| Validator times out | The pipeline fails with `validator <name>: validation timed out after Ns`. |
| Validator returns garbage, a wrong `protocol_version`, or a `result` that disagrees with its diagnostic logs | The pipeline fails with a protocol error identifying the validator. The next pipeline invocation calls the validator again. |
| Validator panics mid-validation | The sidecar returns a synthetic error diagnostic and continues serving subsequent requests. The current render fails. |
| Idle-closed connection on first reuse | Transparently reconnected and retried once. The operator sees no failure. |

In all error cases the current HAProxy data plane keeps its last-good output. **Fail-closed by design**: a broken configured validator blocks new output instead of silently disabling its validation surface.

## Custom validators

The chart's default sidecar is `haproxy-spoa-hub --validate-socket /var/run/haptic-validators/spoa-hub.sock`. To use a different validator implementation:

```yaml
controller:
  validators:
    enabled: false  # turn off the default sidecar
  sidecars:
    - name: my-validator
      image: registry.example.com/my-validator:v1.2.3
      args: ["--validate-socket", "/var/run/haptic-validators/my-validator.sock"]
      volumeMounts:
        - name: haptic-validators
          mountPath: /var/run/haptic-validators
  # Mount the shared socket dir into the controller too — with the default
  # sidecar off, the chart doesn't add this mount for you, so the controller
  # can't reach the socket without it.
  extraVolumeMounts:
    - name: haptic-validators
      mountPath: /var/run/haptic-validators
  extraVolumes:
    - name: haptic-validators
      emptyDir: {}
```

```yaml
# HAProxyTemplateConfig
spec:
  validators:
    - name: my-validator
      socketPath: /var/run/haptic-validators/my-validator.sock
      files: ["/etc/my-app/*.yaml"]
```

Any program that conforms to the [wire protocol](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/development/validator-protocol.md) can be substituted. The protocol is intentionally narrow:

1. Listen on a Unix domain socket at the configured path.
2. Accept multiple concurrent persistent connections.
3. On each connection, loop on read-frame / process / write-response until the client closes or the connection goes idle.
4. Reply with a length-prefixed JSON response carrying line-numbered diagnostic logs.

See [`development/validator-protocol.md`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/development/validator-protocol.md) for the full schema, error semantics, and an end-to-end worked example.

## Troubleshooting

**Admission denied with `connect: no such file or directory`.** The validator sidecar isn't running, or its socket path doesn't match the controller's `spec.validators[i].socketPath`. Check `kubectl logs <controller-pod> -c <validator-container>` and verify the socket path in `values.yaml` matches the path your validator binary actually opens.

**Admission denied with `unknown directive "..."`.** Use the reported row and column to locate the invalid directive and fix the annotation or template that emits it.

**Admission denied with `validation timed out after 5s`.** The validator is too slow on this file. First, check the validator container's logs for the panic / hang. If the slowness is real (very large Open Worldwide Application Security Project (OWASP) Core Rule Set (CRS) bundle, slow regex compile), bump `spec.validators[i].timeoutMs` to a higher value (max 60000).

**`/healthz` returns 503 with `pluggable-validators` failures listed.** Match the failure entries to your `spec.validators` and check the corresponding sidecar container's status. Common causes: OOMKilled (bump container resources), filesystem unmounted (check the chart's `emptyDir` volume), or an upstream image regression (pin a known-good `tag`).

**The validator is called again for unchanged files.** This is required for protocol v1. The protocol doesn't authenticate the live validator runtime, so HAPTIC can't safely reuse an earlier response. The persistent connection pool and parallel dispatch reduce the round-trip cost.

**Latency spikes after a long quiet period.** Idle connections close after about 30 seconds. The next call opens a new connection. Raising `maxConnections` increases concurrency but doesn't keep idle connections open.

## See also

- [Validator wire protocol](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/development/validator-protocol.md) — authoritative spec
- [SPOA hub overview](./spoa-hub.md) — the bundled plugin host (one possible validator implementation)
