# Pluggable Validators

## Overview

You declare one or more validator sidecars in `spec.validators`, each pointing at a Unix domain socket inside the controller pod and listing file glob patterns. After every dry-run render, the controller routes each rendered file to every validator whose globs match. The validator returns line-numbered diagnostics, and the webhook surfaces them in the admission response — so a broken config payload is caught at `kubectl apply` time with the offending row highlighted.

**Why this exists:** HAPTIC's admission webhook validates incoming watched-resource changes (and the pre-rollout validation Job checks the whole configuration before an upgrade) by performing a dry-run render of the operator's templates and an HAProxy syntax check on the result. That catches templating mistakes and HAProxy-syntax mistakes — but the rendered config can also include payloads (Coraza Web Application Firewall (WAF) directives via the Stream Processing Offload Agent (SPOA) hub, OpenID Connect (OIDC) discovery endpoints, etc.) that aren't exercised by HAProxy itself. A typo like `nginx.ingress.kubernetes.io/modsecurity-snippet: "SecResquestBodyAccess On"` would otherwise ship through admission, land in the rendered config, and only fail when the SPOA hub's plugin-init runs in production — at which point the entire HAProxy data plane is down until the operator notices. Pluggable validators close that gap.

The validator on the other side of the socket is an **opaque program** as far as the controller is concerned: it speaks the [validator wire protocol](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/development/validator-protocol.md) and returns diagnostics. What it does internally — whether it has plugins, how it dispatches files, how it parses content — is its own concern. This page is about how operators declare and run validators; the wire protocol itself is the reference for anyone implementing a new one.

## When to use this

Enable pluggable validators when your templates render any file whose contents the validator program understands:

- **SPOA hub TOML configs** with Coraza WAF directives, OIDC single sign-on configs, etc. (validator: `haproxy-spoa-hub --validate-socket`).
- **Future** specialised validators (custom map-file linter, gateway-config validator, etc.) — anything that conforms to the wire protocol can plug in.

Skip this feature if your templates only produce HAProxy config — the core HAProxy syntax dry-run already catches everything that matters in that case.

## How it works

```text
kubectl apply Ingress with broken modsecurity-snippet
        │
        ▼
Kubernetes API server → HAPTIC admission webhook
        │
        ▼
Webhook renders the templates dry-run → produces a set of
{path, content} files (haproxy.cfg + auxiliary files).
        │
        ▼
For each rendered file: match the file's path against every
configured validator's `files` globs. Files that match are sent
to that validator (one file per request frame, in parallel).
        │  (length-prefixed JSON over Unix socket; protocol details:
        │   docs/development/validator-protocol.md)
        ▼
Validators return per-file diagnostics with line numbers.
        │
        ▼
Webhook aggregates warnings and errors:
  - result=valid     → admit, no message.
  - result=warning   → admit + AdmissionResponse.Warnings populated;
                       kubectl apply prints the warnings as soft text.
  - result=error     → deny admission with the formatted errors;
                       kubectl apply rejects the resource.
```

The sidecar runs in the controller pod alongside the controller container. The two share a Unix domain socket via an `emptyDir` volume — no network exposure, no firewall rules.

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
| `name` | yes | RFC 1123 label, unique across the array. Surfaces in admission denial messages so operators can identify which validator rejected a change. |
| `socketPath` | yes | Absolute path inside the controller pod to the validator's Unix domain socket. The chart-rendered shared `emptyDir` mounts at `/var/run/haptic-validators/`. |
| `files` | yes | List of glob patterns matched against rendered file paths to decide which files to send to this validator. Patterns follow Go's `path/filepath.Match` rules; absolute paths only. At least one entry required. |
| `dataFiles` | no | Glob patterns for files this validator needs in order to check the files it validates, but must not validate on its own. Every match is attached to **every** request sent to this validator, marked `kind: "data"`, in the same frame as the config file. A file matching both `files` and `dataFiles` is treated as data. Same glob rules as `files`. |
| `timeoutMs` | no | Per-call deadline in milliseconds covering one (file, validator) round-trip (acquire + write + read). Defaults to 5000. Range: 1–60000. |
| `maxConnections` | no | Cap on the controller's connection pool to this validator. Defaults to 4. Range: 1–32. The pool is adaptive: it starts small (one idle connection), grows on contention up to this cap, and shrinks back when traffic dies down. |

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

The `config.toml` file matches both globs, so it's sent to both validators in parallel; their diagnostics are aggregated. (This is unusual but supported — useful when you want a fast structural check alongside a slow deep semantic check.)

A file that matches no validator's globs isn't validated by any sidecar; it still flows through the existing template + HAProxy syntax dry-run.

### Chart wiring (default)

The chart's validator sidecar **auto-enables** whenever you have a SPOA hub plugin turned on — the plugins that need admission-time validation are the same ones that run on the data plane, so you don't set a separate flag. The shipped default is `controller.validators.enabled: null`, which derives the sidecar's state from the SPOA hub. Enable a plugin and the validator comes along with it:

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

### Three-result behaviour and `kubectl apply` output

Validators return one of three outcomes per file. The webhook maps them as follows:

| Validator `result` | Webhook outcome | What the operator sees |
|---|---|---|
| `valid` | Admission allowed | No message; admission completes normally. |
| `warning` | Admission allowed | `kubectl apply` prints each warning as a soft warning, prefixed `Warning:` per the admission API. The resource IS admitted. |
| `error` | Admission denied | `kubectl apply` prints the formatted errors as the denial reason. Any warnings are appended for context. The resource is rejected. |

When multiple validators check the same file (or different files), all their diagnostics are aggregated. The aggregate `result` is computed the same way: any error wins; any warning without errors wins; otherwise valid.

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

Configure the controller's liveness probe to hit `/healthz` so a stuck validator triggers a pod restart (the chart does this by default). Both containers share the pod lifecycle: when the validator crashes hard, Kubernetes restarts the pod, the sidecar comes back, and admission flows resume.

### Caching

The controller maintains an in-memory Least Recently Used (LRU) cache of validator responses keyed by `(validator-name, file-path, sha256(file-content))`. A repeat reconciliation that produces identical files for the same validator skips the round-trip entirely — typical reconciliation churn (label changes, status updates) doesn't re-validate unchanged plugin configs.

The cache:

- Is process-local. A controller restart re-warms it.
- Holds successful round-trips, including responses with `result: "warning"` or `result: "error"`. Validator output is a deterministic function of its input (per the protocol's purity contract).
- Does **not** cache transport failures (connect refused, decode failure). A transient sidecar outage isn't allowed to poison subsequent admissions.
- Is bounded at 256 entries with LRU eviction.

### Connection pooling and parallelism

The controller maintains a per-validator connection pool of persistent keep-alive connections. The pool starts small (no open connections) and **adapts to load**: it dials a new connection when an in-flight call finds the pool empty and there's headroom; it closes connections that sit idle for ~30 seconds. The cap is `spec.validators[i].maxConnections` (default 4).

`(validator, file)` pairs run **in parallel** — independent validators on different sockets validating independent files. Top-level concurrency is capped at 16 in-flight tasks; each validator's individual pool further throttles within-validator concurrency.

For a typical webhook call with one validator and a handful of matched files, the dispatch finishes about as fast as the slowest file's validation latency. Sequential single-file latency is the worst case (when `maxConnections=1`).

### Failure modes

| What | What HAPTIC does |
|------|------------------|
| Validator socket missing at admission time | Admission denied with `validator <name>: connect <path>: no such file or directory`. The Ingress **isn't** admitted. |
| Validator returns an error response | Admission denied with the validator's `errors[i].message` and the row + column the validator pointed at. |
| Validator returns a warning response | Admission ALLOWED; the warning surfaces via `AdmissionResponse.Warnings` (operator sees it via `kubectl apply` soft warnings). |
| Validator times out | Admission denied with `validator <name>: validation timed out after Ns`. |
| Validator returns garbage / wrong `protocol_version` | Admission denied with a transport-level error message identifying the validator. |
| Validator panics mid-validation | The sidecar catches the panic (per the wire protocol), returns a synthetic error diagnostic, and continues serving subsequent requests. The first admission sees the error; further admissions work. |
| Idle-closed connection on first reuse | Transparently reconnected and retried once. The operator sees no failure. |

In all cases the data plane (HAProxy itself) keeps running — only admission is gated. **Fail-closed by design**: a broken validator means broken admission, not silent acceptance.

!!! note
    If you need a temporary escape hatch (for example, the validator has a bug that's blocking a critical Ingress change), remove the offending entry from `spec.validators` and the webhook reverts to template + HAProxy-syntax dry-run only. Re-add the entry once the validator is fixed.

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
4. Reply with a length-prefixed JSON response carrying line-numbered diagnostics.

See [`development/validator-protocol.md`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/development/validator-protocol.md) for the full schema, error semantics, and an end-to-end worked example.

## Troubleshooting

**Admission denied with `connect: no such file or directory`.** The validator sidecar isn't running, or its socket path doesn't match the controller's `spec.validators[i].socketPath`. Check `kubectl logs <controller-pod> -c <validator-container>` and verify the socket path in `values.yaml` matches the path your validator binary actually opens.

**Admission denied with `unknown directive "..."` or similar specific errors.** This is the feature working — the validator caught a broken config in a rendered file. The diagnostic carries the row + column; use that to find the offending Ingress annotation.

**Admission denied with `validation timed out after 5s`.** The validator is too slow on this file. First, check the validator container's logs for the panic / hang. If the slowness is real (very large Open Worldwide Application Security Project (OWASP) Core Rule Set (CRS) bundle, slow regex compile), bump `spec.validators[i].timeoutMs` to a higher value (max 60000).

**`/healthz` returns 503 with `pluggable-validators` failures listed.** Match the failure entries to your `spec.validators` and check the corresponding sidecar container's status. Common causes: OOMKilled (bump container resources), filesystem unmounted (check the chart's `emptyDir` volume), or an upstream image regression (pin a known-good `tag`).

**Cache returning stale answers.** The cache assumes the validator is a pure function of its input — that's the wire-protocol contract. If a validator implementation violates purity (reaches into the cluster, depends on time, etc.), it produces stale results. The fix is in the validator, not the cache. As a workaround, restart the controller pod to clear the in-memory cache.

**Latency spikes after a long quiet period.** The connection pool reaps idle connections to free file descriptors. The first admission after a quiet stretch may pay one extra connect. If this is a problem, bump `spec.validators[i].maxConnections` so the pool keeps a warmer set of connections (each gets the same idle close — they're just less likely to all be reaped simultaneously).

## See also

- [Validator wire protocol](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/docs/development/validator-protocol.md) — authoritative spec
- [SPOA hub overview](./spoa-hub.md) — the bundled plugin host (one possible validator implementation)
