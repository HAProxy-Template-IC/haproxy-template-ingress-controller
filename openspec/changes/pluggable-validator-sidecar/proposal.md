## Why

Today, HAPTIC's admission webhook validates Ingress / HAProxyTemplateConfig changes by performing a dry-run render of the operator's templates and a HAProxy syntax check on the resulting config. This catches templating mistakes and HAProxy-syntax mistakes — but the rendered config can also include plugin payloads (Coraza WAF directives via SPOE, OpenTelemetry exporters, etc.) that are *not* checked by HAProxy itself. A typo like `nginx.ingress.kubernetes.io/modsecurity-snippet: "SecResquestBodyAccess On"` ships through admission, lands in the rendered hub TOML, and only fails when the hub's plugin-init runs in production — at which point the entire HAProxy data plane is down until the operator notices and fixes the Ingress.

Upstream now exposes the missing hook: `haproxy-spoa-hub` v0.3.0 ships a `--validate-socket <path>` mode that runs each loaded plugin's `validate()` against a TOML config without calling `init()` and returns line-numbered diagnostics. The Coraza plugin v0.3.0 implements `validate()` against its SecLang directives. With these landed upstream, the admission-side gap is HAPTIC's to close.

The user has stated explicitly that HAPTIC must own the wire protocol long-term — the validator sidecar exists to serve HAPTIC's needs, and any future validator implementation (a haproxy-cfg validator, a third-party validator) must conform to a HAPTIC-owned definition. The hub spec at `haproxy-spoa-hub/specs/004-validate-mode/contracts/validate-socket-protocol.md` carries an "interim ownership" disclaimer pointing here.

## What Changes

- New CRD field `spec.validators` on `HAProxyTemplateConfig` — operator declares each pluggable validator (name, socket path, list of plugin subtrees to forward, timeout).
- New controller package `pkg/controller/pluggablevalidator/` — pure components for the wire protocol (length-prefixed JSON framing), content-hash LRU result cache, and unix-socket client; an event-adapter wrapping them into the existing scatter-gather validator pattern (`pkg/controller/validator/base.go`).
- Authoritative copy of the validator-sidecar wire protocol moved into HAPTIC at `docs/development/validator-protocol.md`. The hub spec retains a one-liner pointer.

## Capabilities

### New Capabilities

- `pluggable-validator-sidecar`: validator-client component, content-hash LRU cache, wire-protocol framing/encoding, CRD validators field, validator-protocol document.

### Modified Capabilities

None in this change. Wiring the new component into the admission-webhook flow and the `/healthz` probe is a follow-up change (`pluggable-validator-webhook-wiring`); the chart-side sidecar container + shared volume are a separate follow-up (`pluggable-validator-chart`). Splitting keeps each MR independently reviewable and revertable on this repo's heavy CI pipeline.

## Impact

- **pkg/apis/haproxytemplate/v1alpha1**: `Validators` field added to `HAProxyTemplateConfigSpec` plus a `ValidatorConfig` type. Codegen produces the matching CRD schema in `charts/haptic/crds/haproxy-haptic.org_haproxytemplateconfigs.yaml`.
- **pkg/controller/pluggablevalidator** (new): wire protocol (`protocol.go`), client (`client.go`), LRU cache (`cache.go`), event adapter (`component.go`).
- **pkg/controller/events**: two new event types, `PluggableValidationRequest` and `PluggableValidationResponse`, following the existing `ConfigValidationRequest` / `ConfigValidationResponse` shape.
- **pkg/controller/controller.go**: subscribe the new component during startup so it's wired even though no one publishes the new event yet (subsequent MR changes that).
- **docs/development/validator-protocol.md** (new): authoritative wire-protocol spec.
- **CHANGELOG.md**: `[Unreleased]` entry under `### Added`.

## Non-goals

- Wiring the new validator into the admission webhook. The component is dormant in this MR — the next MR (`pluggable-validator-webhook-wiring`) connects it to `DryRunValidator` and `/healthz`.
- Chart changes (sidecar container, shared volume, values shape). Tracked in the follow-up `pluggable-validator-chart` change.
- New validator implementations. The protocol is defined and tested with a fixture socket server; the only real validator that exists today is the Coraza plugin in haproxy-spoa-hub v0.3.0.
- Multiplexed / persistent connections. The protocol is one-request-per-connection (mirrors hub-side implementation).
