# SPOA hub

## Overview

HAPTIC ships a `spoa-hub` container image that bundles the [haproxy-spoa-hub](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) plus a curated set of plugin shared libraries. Deployed as a sidecar to each HAProxy pod, the hub is a Stream Processing Offload Agent (SPOA): it speaks the [Stream Processing Offload Protocol (SPOP) wire protocol](https://docs.haproxy.org/spoe.html) over a shared Unix domain socket and delegates per-request work to plugins: Web Application Firewall (WAF) inspection, geoip, JA3/JA4 fingerprinting, OpenTelemetry export, OpenID Connect (OIDC) / Security Assertion Markup Language (SAML) auth, request mirroring, and nginx-style external auth.

This page documents the exact components bundled with the version of HAPTIC you are reading docs for, how to verify them end-to-end, and how to tune the HAProxy-side Stream Processing Offload Engine (SPOE) wiring the chart emits when the sidecar is enabled.

## Enabling the hub

The sidecar renders whenever at least one plugin is enabled: with the default `spoaHub.enabled: null`, the chart derives the master switch from the per-plugin `spoaHub.plugins.<name>.enabled` values. To enable a plugin directly:

```bash
--set spoaHub.plugins.fingerprinting.enabled=true
```

Some plugins auto-enable with the template library that consumes them — each per-plugin `enabled` default is a chart-evaluated template string:

- **coraza** follows `controller.templateLibraries.nginxIngress.enabled` or `controller.templateLibraries.haproxyIngress.enabled`,
- **external-auth** follows `controller.templateLibraries.nginxIngress.enabled`,
- **mirror** follows `controller.templateLibraries.gateway.enabled`.

The haproxy-ingress and gateway libraries are on by default, so a default install already runs the hub with the `coraza` and `mirror` plugins; `fingerprinting`, `maxmind`, `otel`, and `sso-auth` stay off until you enable them.

An explicit boolean on `spoaHub.enabled` always wins: `false` forces the sidecar off even with plugins enabled; `true` renders it with none. See the [Chart Values Reference](../reference.md#spoa-hub-sidecar) for every `spoaHub.*` value.

## Bundled components

The image is published at `registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<HAPTIC version>` and is built from the following pinned upstream releases:

<!-- BEGIN: spoa-hub-bundle -->

| Component       | Pinned version                          |
| --------------- | --------------------------------------- |
| Hub               | `v0.7.3`                     |
| `coraza`          | `v0.5.0`           |
| `external-auth`   | `v0.5.0`    |
| `fingerprinting`  | `v0.3.0`   |
| `maxmind`         | `v0.4.0`          |
| `mirror`          | `v0.5.0`           |
| `otel`            | `v0.5.0`             |
| `sso-auth`        | `v0.3.0`         |

Plugin `.so` files target glibc `2.36` (Debian bookworm).

<!-- END: spoa-hub-bundle -->

The table is generated from `versions-spoa.env` at the repository root. CI fails if the rendered output drifts from the source of truth.

## What each plugin does

- **coraza** — embeds the [Coraza WAF](https://coraza.io/) engine and runs HTTP request/response inspection against the Open Worldwide Application Security Project (OWASP) Core Rule Set v4.
- **external-auth** — implements nginx-style `auth_request` semantics: makes an HTTP subrequest to an upstream auth service and returns allow/deny plus identity headers to HAProxy.
- **fingerprinting** — computes JA3, JA3N, and JA4 TLS fingerprints from the ClientHello.
- **maxmind** — performs in-memory MaxMind MMDB lookups against operator-provided database files: City, Country, Autonomous System Number (ASN), and so on.
- **otel** — emits OpenTelemetry traces, metrics, and log records via OpenTelemetry Protocol (OTLP) gRPC or HTTP.
- **mirror** — mirrors HTTP requests to a secondary backend for traffic shadowing; used by the gateway library to implement the Gateway API `HTTPRouteFilter` of type `RequestMirror`.
- **sso-auth** — handles OIDC and SAML2 single sign-on flows with encrypted session cookies.

## Verifying the published image

The image is signed by digest with cosign keyless via GitLab OIDC. The CycloneDX Software Bill of Materials (SBOM) is attached as an in-toto attestation.

```bash
# Image signature
cosign verify registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<version> \
  --certificate-identity-regexp '^https://gitlab\.com/haproxy-haptic/haptic//\.gitlab-ci\.yml@refs/tags/.*$' \
  --certificate-oidc-issuer 'https://gitlab.com'

# CycloneDX SBOM
cosign verify-attestation registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<version> \
  --type cyclonedx \
  --certificate-identity-regexp '^https://gitlab\.com/haproxy-haptic/haptic//\.gitlab-ci\.yml@refs/tags/.*$' \
  --certificate-oidc-issuer 'https://gitlab.com'
```

Each upstream `.so` was independently `sha256sum`-checked and `cosign verify-blob`-ed against its source project's tag identity at image-build time. The SBOM enumerates Rust dependencies via the [`cargo-auditable`](https://github.com/rust-secure-code/cargo-auditable) metadata embedded in every plugin binary.

## Performance tuning

The chart's `spoaHub.haproxy.*` values map directly to HAProxy directives the chart's `spoa-hub` template library emits in the `backend spoa-hub` block and the `spoa-hub-agent` agent inside `spoe.conf`. Defaults are tuned for the typical sidecar deployment (single hub colocated with HAProxy over a Unix domain socket); change them when traffic profile or plugin behavior diverges from that baseline.

| Values key                          | HAProxy directive                                                  | Default              | When to change                                                                                                        |
| ----------------------------------- | ------------------------------------------------------------------ | -------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `spoaHub.haproxy.socketPath`        | `server hub <path>` in `backend spoa-hub`                          | `/run/spoa/hub.sock` | Match a different bind path the sidecar listens on (for example when `securityContext.runAsUser` blocks `/run/spoa`).        |
| `spoaHub.haproxy.modeSpop`          | `mode` line in `backend spoa-hub` — `mode spop` (true) or `mode tcp` (false); the `filter spoe engine` directive on the frontend is emitted either way | `true`               | Auto-falls back to `mode tcp` on HAProxy 3.0 (`mode spop` was introduced in 3.1). Set `false` to force `mode tcp` on 3.1+ as well — rare, mostly compat testing.                                           |
| `spoaHub.haproxy.timeoutHello`      | `timeout hello` on `spoe-agent`                                    | `2s`                 | Raise if the hub regularly logs `HELLO` timeouts under cold-start (for example heavy plugin init like MaxMind DB load).        |
| `spoaHub.haproxy.timeoutIdle`       | `timeout idle` on `spoe-agent` and `timeout server` on the backend | `5m`                 | Lower to free pooled connections faster in low-traffic clusters; raise to match upstream auth-service idle budgets.   |
| `spoaHub.haproxy.timeoutProcessing` | `timeout processing` on `spoe-agent`                               | `500ms`              | Raise per slow plugin (Coraza WAF inspection, large MaxMind lookups). Keep tight to fail fast on hub regressions.     |
| `spoaHub.haproxy.poolMaxConn`       | `pool-max-conn` on the `server hub` line                           | `100`                | Tune to peak concurrent in-flight SPOE messages — usually `request-rate × p99-processing-latency`.                    |
| `spoaHub.haproxy.poolPurgeDelay`    | `pool-purge-delay` on the `server hub` line                        | `30s`                | Lower to release idle pooled connections sooner during traffic dips.                                                  |

The `spoaHub.plugins.<name>.timeoutMs` field on the chart side is independent — it sets the per-plugin timeout the hub enforces internally and doesn't appear in the rendered HAProxy config.

## See also

- [haproxy-spoa-hub](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) — upstream hub binary and SPOP gateway.
- [Chart Values Reference — SPOA Hub Sidecar](../reference.md#spoa-hub-sidecar) — every `spoaHub.*` value.
- [HAProxy versions matrix](./haproxy-versions.md) — supported HAProxy versions for the controller image.
