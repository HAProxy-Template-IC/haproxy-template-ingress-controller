# Pluggable Validators

## Overview

HAPTIC's admission webhook validates incoming `HAProxyTemplateConfig` and watched-resource changes by performing a dry-run render of the operator's templates and an HAProxy syntax check on the result. That catches templating mistakes and HAProxy-syntax mistakes — but the rendered config can also include plugin payloads (Coraza WAF directives via SPOE, OpenTelemetry exporter URLs, OIDC discovery endpoints, etc.) that are not exercised by HAProxy itself. A typo like `nginx.ingress.kubernetes.io/modsecurity-snippet: "SecResquestBodyAccess On"` would otherwise ship through admission, land in the rendered hub TOML, and only fail when the SPOA hub's plugin-init runs in production — at which point the entire HAProxy data plane is down until the operator notices.

**Pluggable validators close that gap.** You declare one or more validator sidecars in `spec.validators`; the controller forwards the rendered hub TOML to each sidecar, and the sidecar runs every loaded plugin's `validate()` against the config without booting it. Diagnostics come back with line numbers and surface in the admission denial reason — so a broken `modsecurity-snippet` is caught at `kubectl apply` time with the offending row highlighted.

This page documents how to declare validators, how to operate the sidecar, and how the wire protocol behaves at runtime. The protocol itself is specified in [`../../../development/validator-protocol.md`](../../../development/validator-protocol.md).

## When to use this

Enable pluggable validators when your templates use:

- **Coraza WAF** annotations (`nginx.ingress.kubernetes.io/modsecurity-snippet`, `haproxy-ingress.github.io/waf-mode` with custom rules) — broken SecLang directives become admission denials with line numbers.
- **OpenTelemetry** annotations with custom endpoint URLs or sampler configs (once the otel plugin's `validate()` lands).
- **Any future SPOA plugin** that surfaces user-supplied configuration through the chart's annotation libraries.

Skip this feature if you only use the built-in templates without any plugin annotations — the core HAProxy syntax dry-run already catches everything that matters in that case, and the validator sidecar adds complexity (one extra container in the controller pod) for no benefit.

## How it works

```text
kubectl apply Ingress with broken modsecurity-snippet
        │
        ▼
Kubernetes API server → HAPTIC admission webhook
        │
        ▼
Webhook renders the TEMPLATES dry-run → produces hub TOML
        │
        ▼
Webhook sends rendered TOML to each spec.validators[i] socket
        │  (length-prefixed JSON, see validator-protocol.md)
        ▼
Validator sidecar runs each loaded plugin's validate()
        │
        ▼
Diagnostics with line numbers → returned to webhook
        │
        ▼
Webhook denies admission with the validator's message,
the offending Ingress is rejected with a clear error,
the data plane keeps running.
```

The sidecar runs in the controller pod alongside the controller container. The two share a Unix domain socket via an `emptyDir` volume — there is no network exposure, and no extra firewall rules.

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
      timeoutMs: 5000
```

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | RFC 1123 label, unique across the array. Surfaces in admission denial messages so operators can identify which validator rejected a change. |
| `socketPath` | yes | Absolute path inside the controller pod to the validator's Unix domain socket. The chart-rendered shared `emptyDir` mounts at `/var/run/haptic-validators/`. |
| `plugins` | no | Optional list of `[plugins.params.<name>]` subtree names this validator should handle. Empty (default) means "validate the whole hub TOML"; the validator dispatches to every loaded plugin's `validate()`. |
| `timeoutMs` | no | Per-call deadline in milliseconds covering connect + write + read. Defaults to 5000 (5 seconds). Range: 1–60000. |

### Chart wiring (default)

The Helm chart wires up the SPOA hub validator by default when you set `controller.validators.enabled: true`:

```yaml
# values.yaml
controller:
  validators:
    enabled: true
    # Image and tag default to the bundled spoa-hub; override only if needed.
    # image: registry.gitlab.com/haproxy-haptic/haptic/spoa-hub
    # tag: <pinned by chart appVersion>
```

This adds one sidecar container to the controller pod, an `emptyDir` volume mounted at `/var/run/haptic-validators/`, and a default `spec.validators` entry pointing at the sidecar's socket. To disable, set `enabled: false`.

For custom validator implementations or multiple sidecars, see "Custom validators" below.

## Operations

### `/healthz` integration

The controller's `/healthz` endpoint stat()'s every configured validator socket on every probe. A failed check (socket missing, wrong file type, connection refused) returns HTTP 503 with a structured failure list:

```json
{
  "healthy": false,
  "components": {
    "controller": {"healthy": true},
    "validators": {
      "healthy": false,
      "failures": ["spoa-hub: dial: connection refused"]
    }
  }
}
```

Configure the controller's liveness probe to hit `/healthz` so a stuck validator triggers a pod restart (the chart does this by default). Both containers share the pod lifecycle: when the validator crashes hard, Kubernetes restarts the pod, the sidecar comes back, and admission flows resume.

### Caching

The controller maintains an in-memory LRU cache of validator responses keyed by `sha256(validator-name || rendered-toml)`. A repeat reconciliation that produces identical TOML skips the validator round-trip entirely — typical reconciliation churn (label changes, status updates) doesn't re-validate unchanged plugin configs.

The cache:

- Is process-local. A controller restart re-warms it.
- Holds successful round-trips, including responses with `result: "warning"` or `result: "error"`. Validator output is a deterministic function of its input (per the protocol's purity contract).
- Does NOT cache transport failures (connect refused, decode failure). A transient sidecar outage isn't allowed to poison subsequent admissions.
- Is bounded at 256 entries with LRU eviction. Sized for a healthy reconciliation churn — there's no Helm value to tune it; if you find yourself wanting one, file an issue.

### Failure modes

| What | What HAPTIC does |
|------|------------------|
| Validator socket missing at admission time | Admission denied with `validator <name>: connect <path>: no such file or directory`. The Ingress is NOT admitted. |
| Validator returns an error response | Admission denied with the validator's `errors[i].message` and the row + column the validator pointed at. |
| Validator times out | Admission denied with `validator <name>: validation timed out after 5s`. |
| Validator returns garbage / wrong protocol_version | Admission denied with a transport-level error message identifying the validator. |
| Validator panics mid-validation | The sidecar catches the panic (per the wire protocol), returns a synthetic error diagnostic, and continues serving subsequent requests. The first admission sees the error; further admissions work. |

In all cases the data plane (HAProxy itself) keeps running — only admission is gated. **Fail-closed by design**: a broken validator means broken admission, not silent acceptance.

!!! note
    If you need a temporary escape hatch (e.g., the validator has a bug that's blocking a critical Ingress change), remove the offending entry from `spec.validators` and the webhook reverts to template + HAProxy-syntax dry-run only. Re-add the entry once the validator is fixed.

## Custom validators

The chart's default sidecar is `haproxy-spoa-hub --validate-socket /var/run/haptic-validators/spoa-hub.sock` running every plugin loaded by the bundled hub image. To use a different validator implementation:

```yaml
controller:
  validators:
    enabled: false  # turn off the default sidecar
  extraContainers:
    - name: my-validator
      image: registry.example.com/my-validator:v1.2.3
      args: ["--validate-socket", "/var/run/haptic-validators/my-validator.sock"]
      volumeMounts:
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
```

Any validator that conforms to the [wire protocol](../../../development/validator-protocol.md) can be substituted. The protocol is intentionally narrow — implementing a new validator is roughly:

1. Listen on a Unix domain socket at the configured path.
2. Read one length-prefixed JSON request frame per connection.
3. Process every `[plugins.params.<name>]` subtree in the supplied TOML.
4. Reply with a length-prefixed JSON response carrying line-numbered diagnostics.
5. Close the connection.

See [`development/validator-protocol.md`](../../../development/validator-protocol.md) for the full schema, error semantics, and an end-to-end worked example.

## Troubleshooting

**Admission denied with `connect: no such file or directory`.** The validator sidecar isn't running, or its socket path doesn't match the controller's `spec.validators[i].socketPath`. Check `kubectl logs <controller-pod> -c <validator-container>` and verify the socket path in `values.yaml` matches the path your validator binary actually opens.

**Admission denied with `unknown directive "..."` or similar plugin-specific errors.** This is the feature working — the validator caught a broken plugin config in the rendered TOML. The diagnostic carries the row + column; use that to find the offending Ingress annotation. The diagnostic message comes verbatim from the plugin's `validate()`; if it's unclear, check the plugin's documentation.

**Admission denied with `validation timed out after 5s`.** The validator is too slow on this config. First, check the validator container's logs for the panic / hang. If the slowness is real (very large CRS bundle, slow regex compile), bump `spec.validators[i].timeoutMs` to a higher value (max 60000).

**`/healthz` returns 503 with `validators` failures listed.** Match the `failures[]` entries to your `spec.validators` and check the corresponding sidecar container's status. Common causes: OOMKilled (bump container resources), filesystem unmounted (check the chart's `emptyDir` volume), or an upstream image regression (pin a known-good `tag`).

**Cache returning stale answers.** The cache assumes the validator is a pure function of its input — that's the wire-protocol contract. If a validator implementation violates purity (reaches into the cluster, depends on time, etc.), it produces stale results. The fix is in the validator, not the cache. As a workaround, restart the controller pod to clear the in-memory cache.

## See also

- [Validator wire protocol](../../../development/validator-protocol.md) — authoritative spec
- [SPOA hub overview](./spoa-hub.md) — the bundled plugin host
- [HAProxy-haptic SPOA Coraza plugin](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub-plugin-coraza) — first plugin shipping `validate()` support
