# SPOA Hub

## Overview

HAPTIC ships a `spoa-hub` container image that bundles the [haproxy-spoa-hub](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) plus a curated set of plugin shared libraries. Deployed as a sidecar to each HAProxy pod, the hub speaks the [SPOP wire protocol](https://docs.haproxy.org/spoe.html) over a shared Unix domain socket and delegates per-request work to plugins (WAF inspection, geoip, JA3/JA4 fingerprinting, OpenTelemetry export, OIDC/SAML auth, nginx-style external auth).

This page documents the exact components bundled with the version of HAPTIC you are reading docs for, and how to verify them end-to-end.

!!! note
    The chart values, sidecar wiring, and SPOE/SPOP performance tuning are introduced in a later cycle. This page is for the image content alone; a deployment guide will follow.

## Bundled components

The image is published at `registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<HAPTIC version>` and is built from the following pinned upstream releases:

<!-- BEGIN: spoa-hub-bundle -->

| Component       | Pinned version                          |
| --------------- | --------------------------------------- |
| Hub             | `v0.2.2`                     |
| coraza          | `v0.1.1`           |
| external-auth   | `v0.1.1`    |
| fingerprinting  | `v0.1.1`   |
| maxmind         | `v0.2.1`          |
| otel            | `v0.1.1`             |
| sso-auth        | `v0.1.1`         |

Plugin `.so` files target glibc `2.36` (Debian bookworm).

<!-- END: spoa-hub-bundle -->

The table is generated from `versions-spoa.env` at the repository root. CI fails if the rendered output drifts from the source of truth.

## What each plugin does

- **coraza** — embeds the [Coraza WAF](https://coraza.io/) engine and runs HTTP request/response inspection against OWASP Core Rule Set v4.
- **external-auth** — implements nginx-style `auth_request` semantics: makes an HTTP subrequest to an upstream auth service and returns allow/deny plus identity headers to HAProxy.
- **fingerprinting** — computes JA3, JA3N, and JA4 TLS fingerprints from the ClientHello.
- **maxmind** — performs in-memory MaxMind MMDB lookups (City, Country, ASN, etc.) against operator-provided database files.
- **otel** — emits OpenTelemetry traces, metrics, and log records via OTLP gRPC or HTTP.
- **sso-auth** — handles OIDC and SAML2 SSO flows with encrypted session cookies.

## Verifying the published image

The image is signed by digest with cosign keyless via GitLab OIDC. The CycloneDX SBOM is attached as an in-toto attestation.

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

## See also

- [haproxy-spoa-hub](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) — upstream hub binary and SPOP gateway.
- [HAProxy versions matrix](./haproxy-versions.md) — supported HAProxy versions for the controller image.
