# Design

## Why this lives in HAPTIC, not in the hub

The validator-sidecar wire protocol exists to serve HAPTIC's admission-webhook needs. The hub repo authored the first version because it shipped the first server-side implementation (MR !136 over there); the user has stated explicitly that HAPTIC owns the contract long-term. Concretely: any future validator implementation — a haproxy-cfg validator that doesn't run plugins, a third-party validator implementing the same wire format — must conform to a HAPTIC-defined protocol. The hub spec carries an "interim ownership" disclaimer pointing at this document.

This change moves the authoritative copy. The hub side gets reduced to a one-line pointer in a follow-up MR.

## Why event-driven and not direct call

The repo's existing validator pattern (`pkg/controller/validator/base.go`) uses scatter-gather over the event bus: a coordinator publishes one `ConfigValidationRequest`, every registered validator (BasicValidator, TemplateValidator, JSONPathValidator) consumes it and publishes a `ConfigValidationResponse`. The pluggable validator fits this same shape — operators may declare zero, one, or many validators in `spec.validators`, each backed by its own sidecar, each producing its own diagnostics.

We could short-cut to a direct synchronous call from the webhook, but that would:

- Bypass the existing diagnostic-aggregation glue.
- Force the webhook to know about the validator topology (one or many sidecars, which subset of plugins each handles).
- Make `/healthz` integration awkward — health checks would have to reach into the webhook code path rather than asking a component for its health.

Event-driven keeps the cost low (one extra hop on the bus) and the architecture coherent.

## Why a content-hash LRU cache

`spec.validators` declares which `[plugins.params.<name>]` subtrees forward to which sidecar. After a render, the controller knows which subtrees changed; for those, it issues `PluggableValidationRequest`s. For unchanged subtrees, it can serve a cached response.

Without the cache, every reconciliation re-validates every plugin subtree even when nothing changed, doubling the validator-sidecar load and adding latency to every render. With the cache:

- Key: `sha256(plugin-name || rendered-toml-bytes)`.
- Value: full `Response` (preserving any warnings + the `result` flag).
- Capacity: 256 entries by default (covers a healthy reconciliation churn even on busy clusters; one entry per (plugin, distinct-content) combination).
- LRU eviction.

The cache is process-local. Restarts re-warm. No persistent storage.

## Why one-request-per-connection

The hub-side server (MR !136) accepts one request and closes the connection. Mirroring that on the client keeps the protocol simple and matches the upstream contract verbatim. If multiplexed connections ever become a bottleneck (unlikely — each request is short-lived and the validator runs in the controller pod alongside us), it's a separate protocol-version bump.

## Why a separate event type

The existing `ConfigValidationRequest` / `ConfigValidationResponse` shape is for *config-level* validators (basic, template, jsonpath) — they answer "is the HAProxyTemplateConfig itself well-formed". Pluggable validators answer a different question: "is the rendered hub TOML accepted by the configured plugins". They run at a different point in the pipeline, against different inputs, and produce diagnostics keyed by `path: line: column` rather than per-config-key.

Separate event types keep the scatter-gather correlation IDs from getting tangled and let the coordinator wait on the right group.

## Cache poisoning concerns

Any cache-on-content design risks poisoning if the validator's output depends on hidden state. The hub-side `validate()` is documented as pure (no goroutine fan-out, no network I/O, no global state — see `haproxy-spoa-hub/specs/004-validate-mode/contracts/plugin-api-delta.md`). With pure validators, content-hash keying is correct. If a future validator violates that contract, the cache produces stale results — but the contract violation is the bug, not the caching.

## Failure mode: validator unreachable

If a validator's socket is gone, the client returns a synthetic `error`-severity diagnostic with `path: ""` and a message identifying the validator. The webhook surfaces it to the user as an admission denial. This is fail-closed: an Ingress change cannot land if its declared validator is broken. The follow-up `/healthz` integration takes the validator down with the controller pod via the liveness probe so Kubernetes restarts the pair, recovering automatically.

The alternative — fail-open on validator outage — was rejected because (a) the user has explicitly named "silent validator unavailability breaks admission feedback" as a requirement, and (b) fail-open under outage means broken Ingress configs ship to the cluster and crash the data plane on next reconcile. Better to block admission and let `/healthz` recycle the pod.

## Content shape sent over the wire

Per the wire-protocol doc, the request contains `files[].content` (raw TOML text). The controller's render produces a single hub TOML per validator. Each `PluggableValidationRequest` contains exactly one entry under `files`, named `hub-config.toml`. Diagnostics from the validator are scoped to that file path; the webhook decoration translates the `line` field into the source Ingress's `modsecurity-snippet` annotation for the user-visible message.

That source-mapping (line N in hub TOML → line M in user's Ingress annotation) is a *next-MR* problem (`pluggable-validator-webhook-wiring`). For this MR, we round-trip the wire format.

## Why no retries at the client level

The webhook flow is synchronous from the user's POV: `kubectl apply` waits. A retry loop here would add latency without value:

- Network hiccup on a unix socket → the connection either succeeds immediately or never (no transient failure mode).
- Plugin panic → the hub catches it and returns a synthetic error; retry produces the same error.
- Timeout → the user wants to know now, not in 30 seconds.

If we ever discover a real transient failure mode, retries can be added at the component level with a backoff, behind a config flag. Out of scope here.
