# `haptic-annotations` library

The `haptic-annotations` library is HAPTIC's **native** annotation vocabulary, under the `haproxy-haptic.org/*` prefix. Its capabilities are a best-of-breed **superset** of the three vendor annotation libraries ([`haproxytech`](./haproxytech.md), [`haproxy-ingress`](./haproxy-ingress.md), [`nginx-ingress`](./nginx-ingress.md)) combined: for every capability it adopts whichever vendor's semantics is strongest and exposes it under one clean name.

Where the vendor libraries exist to ease migration *from* an upstream ingress controller, this is the vocabulary to reach for when writing HAPTIC configuration from scratch. It's enabled by default.

Highlights it pulls together: haproxytech's pod-aware `pod-maxconn` and request capture; haproxy-ingress's agent checks, OAuth2-proxy flow, path-type control, and four config-section injection points; and nginx-ingress's canary routing, request mirroring, and bandwidth throttling — alongside the timeouts, load balancing, TLS, CORS, redirects, HSTS, session affinity, access control, and authentication all three share.

## Overview

This library is enabled by default. See `haproxy-haptic.org/*` annotations render to HAProxy config live:

<div class="pg-embed" markdown data-scenario="haptic-annotations" data-tab="haproxy.cfg" data-controls="tabs,resources" data-title="haproxy-haptic.org/* annotations rendered" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, change the `shop` Ingress's `haproxy-haptic.org/load-balance` from `leastconn` to `roundrobin`, then watch the `backend` section's `balance` line update in the `haproxy.cfg` tab.</p>

</div>

!!! tip "Coming from another ingress controller?"
    Keep your existing annotations working by enabling the matching vendor library instead — see [Migrating to HAPTIC](../migrating.md). Vendor and `haproxy-haptic.org/*` annotations coexist on the same Ingress as long as each feature is configured through a single family — see [Don't mix families for one feature](#dont-mix-annotation-families-for-one-feature).

## Configuration

```yaml
controller:
  templateLibraries:
    hapticAnnotations:
      enabled: true  # Enabled by default
```

### Don't mix annotation families for one feature

You may combine `haproxy-haptic.org/*` and vendor annotations on the same Ingress, but each feature must come from a single family. Configuring one feature through two families — for example `haproxy-haptic.org/waf-policy` and `haproxy-ingress.github.io/waf`, or `haproxy-haptic.org/cors-enable` and `nginx.ingress.kubernetes.io/enable-cors` — is a conflict, even when the two values agree, because the result would otherwise depend on which library renders last.

HAPTIC handles the conflict in two ways, depending on when it's caught:

- **When you apply or edit the Ingress**, the admission webhook rejects the change with a message naming the feature, the families, and the colliding annotations. This stops new conflicts from ever reaching the cluster.
- **For an Ingress that already carries a conflict** (applied before the check existed, or through a bypassed webhook), the controller keeps serving traffic and records a `Warning` Event with reason `AnnotationFamilyConflict` on the Ingress instead of failing — one bad Ingress must not block config updates for the whole fleet. Find it with `kubectl describe ingress <name>` or `kubectl get events --field-selector reason=AnnotationFamilyConflict`, then remove the duplicate annotation.

Different features from different families are fine (WAF from one family, CORS from another), and so are genuinely different parameters of the same category — for example a connect timeout from one family and a server timeout from another. Only enabled families count: a vendor annotation whose library is disabled is inert and never collides.

### How HAPTIC handles a misconfigured annotation

The same two-stage handling applies to any invalid annotation value (a bad redirect code, a malformed rewrite, an out-of-range port), not just family conflicts. It also applies to every annotation library, not only this one.

- **When you apply or edit the Ingress**, the admission webhook rejects the change and names the offending annotation, so a typo never reaches the cluster.
- **For an Ingress that's already in the cluster** (applied before a check existed, or through a bypassed webhook), the behavior depends on the *kind* of feature:
    - **Routing and presentation features** (redirects, CORS, cookie/header/location rewrites, canary, compression, traffic mirroring, host rewrites, fixed/mock responses) — the controller records a `Warning` Event on the Ingress, skips *that one feature for that one Ingress*, and keeps serving the rest of the fleet. One bad Ingress can't block config updates for everyone. Find these with `kubectl get events --field-selector reason=InvalidAnnotationValue` (or `reason=InvalidAnnotation` for malformed values), or `kubectl describe ingress <name>`.
    - **Security features** (authentication, client-certificate/mTLS, WAF, rate limiting, request-body validation) — the render **still hard-fails**. HAPTIC never silently disables a security control, because a skipped auth or WAF check would let traffic through unprotected (fail-open). Fix the annotation to restore reconciliation.

The reason strings on the Events are stable and machine-readable, so you can alert on them.

## Annotation reference

Every annotation below works. Most are **✅ Supported**; a few are marked **⚠️ Caveat** — they work too, but with the behavioural limitation described alongside. The library declares no inert annotations; nothing is silently ignored.

### Path and host matching

Route which requests reach a backend and alias extra hostnames onto an existing host.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/path-type` | ✅ Supported | Overrides how the path matches when the Ingress `pathType` is `ImplementationSpecific`: `regex`, `exact`, `prefix` (trailing slash normalized), or `begin`. |
| `haproxy-haptic.org/host-alias` | ✅ Supported | Adds extra exact hostnames (comma- or space-separated) that route to the same backends as the Ingress's primary host. Each hostname becomes a host-map entry pointing at the primary host's normalized routing key, so no backends or path-map entries are duplicated. Each hostname is injection-guarded (control characters and spaces rejected). |
| `haproxy-haptic.org/host-alias-regex` | ✅ Supported | Adds a regular-expression hostname pattern that routes every matching hostname to the same backends as the Ingress's primary host. The pattern becomes a regex host-map entry pointing at the primary host's normalized routing key, consulted after an exact host-map miss. The pattern is injection-guarded (control characters and spaces rejected). |

### Backend tuning

Per-backend timeouts, load balancing, connection limits, health/agent checks, and a raw-directive escape hatch.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/agent-check-addr` | ✅ Supported | Sets the agent-check address via `agent-addr`; requires `agent-check-port`. |
| `haproxy-haptic.org/agent-check-interval` | ✅ Supported | Sets the agent-check interval via `agent-inter`; requires `agent-check-port`. |
| `haproxy-haptic.org/agent-check-port` | ✅ Supported | Enables the agent check on the given port (1-65535) via `agent-check` and `agent-port`; required by the other `agent-check-*` keys. |
| `haproxy-haptic.org/agent-check-send` | ✅ Supported | Sets the string sent to the agent check via `agent-send`; requires `agent-check-port`. |
| `haproxy-haptic.org/check` | ✅ Supported | Toggles server health checks; `off` emits `no-check` so servers aren't health-checked. |
| `haproxy-haptic.org/config-backend` | ✅ Supported | Injects raw, operator-authored HAProxy directives verbatim into the `backend` section. Intended for trusted configuration, not request data. |
| `haproxy-haptic.org/fullconn` | ✅ Supported | Emits `fullconn <n>` on the backend. HAProxy uses this threshold to scale each server's `minconn`/`maxconn` range as backend load rises. For a hard per-server cap, use `maxconn-server`. |
| `haproxy-haptic.org/health-check-fall` | ✅ Supported | Sets the failed-check count before a server is marked down via `fall`. |
| `haproxy-haptic.org/health-check-interval` | ✅ Supported | Sets the health-check interval via `inter`; ignored when `check` is `off`. |
| `haproxy-haptic.org/health-check-port` | ✅ Supported | Sets the health-check port (1-65535) via `port`. |
| `haproxy-haptic.org/health-check-rise` | ✅ Supported | Sets the successful-check count before a server is marked up via `rise`. |
| `haproxy-haptic.org/health-check-uri` | ✅ Supported | Enables HTTP health checks via `option httpchk`; a bare path becomes `GET <path>`, and a value containing a space is used verbatim. |
| `haproxy-haptic.org/initial-weight` | ✅ Supported | Sets the initial server weight (0-256) via `weight`. |
| `haproxy-haptic.org/load-balance` | ✅ Supported | Sets the backend `balance` algorithm: `roundrobin`, `static-rr`, `leastconn`, `first`, `source`, `random`, or a parameterized `uri`, `url_param(<name>)`, `hdr(<name>)`, or `rdp-cookie(<name>)`; an invalid value fails the render. |
| `haproxy-haptic.org/maxconn-server` | ✅ Supported | Sets the per-server maximum concurrent connections via `maxconn`. |
| `haproxy-haptic.org/maxqueue-server` | ✅ Supported | Sets the per-server maximum queued connections via `maxqueue`. |
| `haproxy-haptic.org/pod-maxconn` | ✅ Supported | Sets a cluster-wide connection budget, divided across the ready HAProxy pods and rounded up to a power of two, then applied as each server's `maxconn`. |
| `haproxy-haptic.org/proxy-protocol` | ✅ Supported | Sends the PROXY protocol header to servers: `proxy`/`proxy-v1` emit `send-proxy`, and `proxy-v2`, `proxy-v2-ssl`, `proxy-v2-ssl-cn` emit the matching `send-proxy-v2` variant; any other value fails the render. |
| `haproxy-haptic.org/scale-server-slots` | ✅ Supported | Overrides the number of reserved server slots the backend pre-allocates for runtime scaling. |
| `haproxy-haptic.org/timeout-check` | ✅ Supported | Sets the check timeout via `timeout check`. |
| `haproxy-haptic.org/timeout-connect` | ✅ Supported | Sets the connect timeout via `timeout connect`. |
| `haproxy-haptic.org/timeout-http-request` | ✅ Supported | Sets the request timeout via `timeout http-request`. |
| `haproxy-haptic.org/timeout-keep-alive` | ✅ Supported | Sets the keep-alive timeout via `timeout http-keep-alive`. |
| `haproxy-haptic.org/timeout-queue` | ✅ Supported | Sets the queue timeout via `timeout queue`. |
| `haproxy-haptic.org/timeout-server` | ✅ Supported | Sets the server timeout via `timeout server`. |
| `haproxy-haptic.org/timeout-tunnel` | ✅ Supported | Sets the tunnel timeout via `timeout tunnel`. |
| `haproxy-haptic.org/consistent-hash-by` | ✅ Supported | Configures consistent hashing on the backend, emitting a `balance` directive plus `hash-type consistent`. Accepts a hash key: `uri`, `source`, `$http_<name>`, `$arg_<name>`, or `$cookie_<name>`; any other value is used verbatim as a HAProxy fetch expression via `balance hash <value>`. |

### Backend TLS (to the upstream)

Speak TLS to the backend Service — protocol, verification, client certs, SNI, ciphers.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/backend-ca-secret` | ✅ Supported | Loads the Secret's `ca.crt` as the backend `ca-file` and requires TLS verification; a missing Secret or key is skipped with a warning comment. |
| `haproxy-haptic.org/backend-ciphers` | ✅ Supported | Sets the cipher list for TLS 1.2 and earlier via `ciphers` on a TLS-enabled backend. |
| `haproxy-haptic.org/backend-ciphersuites` | ✅ Supported | Sets the cipher suites for TLS 1.3 via `ciphersuites` on a TLS-enabled backend. |
| `haproxy-haptic.org/backend-crt-secret` | ✅ Supported | Presents the Secret's `tls.crt` and `tls.key` as a client certificate to the upstream via `crt`; a missing Secret is skipped with a warning. |
| `haproxy-haptic.org/backend-protocol` | ✅ Supported | Selects the upstream protocol from `h1`, `h2`, `h1-ssl`, `h2-ssl`, `http`, `https`, `grpc`, or `grpcs`; the `h2`, `grpc`, `h2-ssl`, and `grpcs` values add `proto h2`, and `h1-ssl`, `https`, `h2-ssl`, and `grpcs` speak TLS to the upstream. |
| `haproxy-haptic.org/backend-sni` | ✅ Supported | Sets the SNI sent to the upstream: `host` or `sni` forwards the request Host via `sni req.hdr(host)`, and any other value is sent literally via `sni str(<value>)`. |
| `haproxy-haptic.org/backend-ssl-protocols` | ⚠️ Caveat | Maps a space-separated TLS version list to `ssl-min-ver` (lowest) and `ssl-max-ver` (highest). HAProxy expresses only a contiguous span, so a gap in the list (for example, skipping `TLSv1.2`) can't be represented. |
| `haproxy-haptic.org/backend-verify` | ✅ Supported | A truthy value (`on`, `true`, `yes`, `1`) requires upstream certificate verification, and fails closed rather than silently downgrading to `verify none` when no CA is available. |
| `haproxy-haptic.org/backend-verify-host` | ✅ Supported | Sets the expected upstream certificate hostname via `verifyhost`, independent of the SNI value. |

### Rate and bandwidth limiting

Per-source request-rate caps (reload-surviving stick-tables), shared fleet-wide request budgets through the rate-limit SPOA plugin, and download/upload bandwidth throttling.

Two facts about bandwidth limits surprise people, so check them against what you intend:

- **The limit applies per stream, not per connection.** An HTTP/2 or HTTP/3 client that opens ten streams gets ten times the configured rate. Use `bandwidth-limit-scope: client` when you want one budget per client regardless of how many streams it opens.
- **Only the HTTP payload is metered.** Headers are never counted toward the limit.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/download-bandwidth-limit` | ✅ Supported | Caps the bytes per second sent toward the client, using a `bwlim-out` filter plus `http-request set-bandwidth-limit`. Independent of the request-rate caps; both can apply to the same Ingress. Byte-size values are validated before interpolation. |
| `haproxy-haptic.org/upload-bandwidth-limit` | ✅ Supported | Caps the bytes per second received from the client, using a `bwlim-in` filter. Can be combined with `download-bandwidth-limit`; each direction gets its own filter. Byte-size values are validated before interpolation. |
| `haproxy-haptic.org/bandwidth-limit-scope` | ⚠️ Caveat | Who shares the budget: `stream` (default, each stream gets the full limit), `client` (all streams from one source IP share it, `key src`), or `service` (every stream of this backend shares it, `key be_id`). `client` and `service` add a stick-table to the backend, and HAProxy allows only one per backend — so they can't be combined with `rate-limit-rps`, `rate-limit-rpm`, or `rate-limit-connections`, and the render fails if you try. `service` scopes to one Ingress route to a service, not to a Kubernetes Service shared by several Ingresses. |
| `haproxy-haptic.org/rate-limit-algorithm` | ✅ Supported | Shared limiter algorithm: `token-bucket` (default, low-latency lease mode) or `gcra` (exact mode, synchronous store check with a short fail-closed timeout). `gcra` is for low-volume contractual limits; use the default token-bucket mode for public-edge DoS protection. Requires `rate-limit-requests`, `rateLimit.shared.enabled=true`, and an effective Redis/Valkey `store_url`/`store_urls`. |
| `haproxy-haptic.org/rate-limit-burst` | ✅ Supported | Shared limiter burst allowance; defaults to `rate-limit-requests`. Must be a positive integer. |
| `haproxy-haptic.org/rate-limit-connections` | ✅ Supported | Caps concurrent connections per source IP; ignored when `rate-limit-rps` or `rate-limit-rpm` is set. |
| `haproxy-haptic.org/rate-limit-key` | ✅ Supported | Shared limiter key dimension: `ip` (default) or `consumer`. Source-IP limits run in the frontend before Coraza and request-schema validation, making them the correct DoS guard. Consumer limits run in the selected backend after native API-key/JWT authentication has established the identity, falling back to source IP when no identity is present; use them for authenticated quotas, not as the sole public-edge flood control. |
| `haproxy-haptic.org/rate-limit-period` | ✅ Supported | Overrides the rate window. For the per-pod stick-table limiter, when unset the window derives from the active cap: 1 second for requests per second, 60 seconds for requests per minute, and a 30-second table TTL for connection caps. For the shared limiter it defaults to `1s` and accepts `ms`/`s`/`m`/`h`/`d`; zero or malformed values fail the render. |
| `haproxy-haptic.org/rate-limit-requests` | ✅ Supported | Enables the shared fleet-wide limiter for the Ingress: N requests per `rate-limit-period`, enforced through the rate-limit SPOA plugin. Requires `rateLimit.shared.enabled=true` plus either the default chart-managed HA Valkey/Sentinel store (`rateLimit.shared.managedStore.enabled=true`) or bring-your-own `rateLimit.shared.externalStore.urls`; HAPTIC fails the render rather than silently falling back to a per-pod budget. If the SPOA hub/plugin returns no verdict for an annotated route, HAProxy fails closed with 429 to avoid a rate-limit bypass. Source-IP rules execute before Coraza to keep rejected floods from consuming WAF CPU. The token-bucket mode bounds local key state with `max_keys`/`idle_ttl_ms`; under capacity pressure new keys wait for a shared lease rather than receiving fresh optimistic local tokens. Exact `gcra` mode uses a default store timeout of 10 milliseconds so store trouble fails closed instead of adding a long request tail. The managed store is a fixed-size HA topology: one writable primary, replicas, Sentinel failover, PodDisruptionBudget, and NetworkPolicy. Use bring-your-own infrastructure when you need horizontally scalable Valkey. |
| `haproxy-haptic.org/rate-limit-rpm` | ✅ Supported | Caps requests per minute per source IP (a 60-second `http_req_rate` window); ignored when `rate-limit-rps` is also set. |
| `haproxy-haptic.org/rate-limit-rps` | ✅ Supported | Caps requests per second per source IP via an `http_req_rate` stick-table; requests over the cap are rejected with the deny status (default `429`), with no burst allowance. |
| `haproxy-haptic.org/rate-limit-size` | ✅ Supported | Sets the stick-table size (default `100k`). |
| `haproxy-haptic.org/rate-limit-status-code` | ✅ Supported | Sets the HTTP status returned to rejected requests (default 429). Validated as a 3-digit HTTP status before interpolation, then emitted as the `http-request deny deny_status` code. |
| `haproxy-haptic.org/rate-limit-allowlist` | ✅ Supported | Exempts comma-separated CIDRs from the rate limit; invalid CIDRs fail the render. |

### Compression

HAProxy-side response compression, per Ingress.

Compression runs before the bandwidth limiter, so a `download-bandwidth-limit` on a compressed route meters the compressed bytes that go on the wire, not the larger uncompressed response.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/compress-algorithm` | ✅ Supported | Compression algorithm (default `gzip`; `deflate`/`raw-deflate`). `brotli`/`zstd` fail the render — unavailable in the community HAProxy build. |
| `haproxy-haptic.org/compress-enable` | ✅ Supported | The value `true` enables HAProxy-side response compression for the backend. |
| `haproxy-haptic.org/compress-types` | ✅ Supported | Comma-separated MIME types to compress (default a standard text/JSON/XML/SVG set). |

### Shared response cache

Routes cache-eligible requests through a chart-deployed, consistent-hash-sharded Varnish tier, so the cache is shared across the whole HAProxy fleet. These annotations take effect only when the tier is enabled (`cache.varnish.enabled`). The tier's default-on NetworkPolicy admits cache requests only from the same release's HAProxy pods and limits Varnish egress to DNS plus the same HAProxy HTTP origin; disable `cache.varnish.networkPolicy.enabled` only when replacing it with equivalent isolation. Per-route behaviour is driven by internal `X-Haptic-Cache-*` headers that HAProxy strips from the client request first, so a client can't influence the cache key or the exclusion rules. Source-verified Varnish cache-miss loopback requests bypass the shared rate limiter because the external request has already consumed its budget; this prevents double counting and cache-cold self-throttling.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/cache-enable` | ✅ Supported | The value `true` routes the Ingress's requests through the shared Varnish cache tier. |
| `haproxy-haptic.org/cache-exclude-content-types` | ✅ Supported | Comma-separated response media types never cached even if otherwise eligible (for example `text/html`); matched after stripping the `; charset=…` suffix. |
| `haproxy-haptic.org/cache-exclude-paths` | ✅ Supported | Comma-separated request path prefixes that bypass the cache and go straight to the app. |
| `haproxy-haptic.org/cache-key` | ✅ Supported | Adds a vary component to the cache key: `consumer`, `src`, `header:<h>`, `cookie:<c>`, `query:<q>`, or a comma-separated composite — so, for example, per-consumer responses are cached separately (which is what makes caching authenticated content safe). |
| `haproxy-haptic.org/cache-max-object-size` | ✅ Supported | Maximum cacheable response size in bytes; a larger response (by `Content-Length`) stays uncacheable. |
| `haproxy-haptic.org/cache-ttl` | ✅ Supported | Cache lifetime in seconds for the route; non-2xx or `Set-Cookie` responses stay uncacheable. |

### Rewriting, retries, and session affinity

Path/target rewriting, body-size limits, upstream retries, Host/header overrides, and cookie-based stickiness.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/affinity` | ✅ Supported | The value `cookie` enables cookie-based session affinity via the backend `cookie` directive. |
| `haproxy-haptic.org/backend-connection-header` | ✅ Supported | Overrides the `Connection` header sent to the backend server. |
| `haproxy-haptic.org/path-rewrite` | ✅ Supported | Rewrites the request path via `http-request replace-path`: a `<from> <to>` pair rewrites the match, and a bare value replaces the whole path. |
| `haproxy-haptic.org/max-request-body-size` | ✅ Supported | Limits the request body size (accepts `k`, `m`, or `g` suffixes), returning `413` when exceeded; `0` means unlimited. |
| `haproxy-haptic.org/request-buffering` | ✅ Supported | `on` or `off`, overriding the fleet-wide `requestBuffering.enabled` default for this route. See [Request buffering](#request-buffering). |
| `haproxy-haptic.org/retry-on` | ✅ Supported | Sets the conditions under which HAProxy retries a failed request against the next server, emitting `retry-on`. Conditions cover connection failures, response timeouts, malformed responses, and per-status-code retries (`http_<code>`); a disable value emits `retries 0`. `option redispatch` in defaults sends the retry to a different server. |
| `haproxy-haptic.org/retries` | ✅ Supported | Sets the number of retry attempts against backend servers via HAProxy `retries`; `0` keeps the default. |
| `haproxy-haptic.org/session-cookie-domain` | ✅ Supported | Sets the session cookie's `Domain` via the `domain` cookie keyword. |
| `haproxy-haptic.org/session-cookie-dynamic` | ✅ Supported | Enables dynamically generated cookie values via the `dynamic` cookie keyword (default on). |
| `haproxy-haptic.org/session-cookie-keywords` | ✅ Supported | Appends extra keywords to the `cookie` directive verbatim (for example, `httponly`). |
| `haproxy-haptic.org/session-cookie-max-age` | ✅ Supported | Sets the browser cookie lifetime in seconds via `attr Max-Age`; this is the cookie's `Max-Age`, not HAProxy's server-affinity lifetime. |
| `haproxy-haptic.org/session-cookie-name` | ✅ Supported | Sets the session cookie name (default `INGRESSCOOKIE`). |
| `haproxy-haptic.org/session-cookie-path` | ✅ Supported | Sets the session cookie's `Path` via `attr Path`. |
| `haproxy-haptic.org/session-cookie-preserve` | ✅ Supported | The value `true` adds the `preserve` keyword to the `cookie` directive. |
| `haproxy-haptic.org/session-cookie-samesite` | ✅ Supported | Sets the cookie `SameSite` attribute (`None`, `Lax`, or `Strict`) via `attr SameSite`. |
| `haproxy-haptic.org/session-cookie-secure` | ✅ Supported | The value `true` sets the cookie `Secure` attribute via `attr Secure`. |
| `haproxy-haptic.org/session-cookie-strategy` | ✅ Supported | Selects the cookie mode: `insert` (default), `rewrite`, or `prefix`; `insert` and `prefix` add `indirect nocache`. |
| `haproxy-haptic.org/set-host` | ✅ Supported | Overrides the `Host` header sent to the upstream. |
| `haproxy-haptic.org/x-forwarded-prefix` | ✅ Supported | Sets the `X-Forwarded-Prefix` header sent to the upstream. |

#### Request buffering

HAProxy buffers request bodies by default, so a client that trickles its upload holds an HAProxy buffer instead of a backend server slot. Set `haproxy-haptic.org/request-buffering` to change that for one route:

```yaml
metadata:
  annotations:
    haproxy-haptic.org/request-buffering: "off"
```

Use `off` when the route's clients declare a `Content-Length` but still expect a response before the request body ends, such as a resumable-upload endpoint. Use `on` to buffer one route while [`requestBuffering.enabled`](base.md#request-buffering) is `false` fleet-wide.

Only requests that declare a `Content-Length` are ever buffered, so `on` can't break a gRPC or chunked streaming route. The base library explains [why that condition is the right one](base.md#streaming-requests-are-never-buffered).

### Headers, CORS, and access control

Request/response header manipulation, capture, CORS, source-IP allow/deny, and upstream cookie/redirect rewriting.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/allowlist-source-range` | ✅ Supported | Allows only the listed CIDRs and denies all other source IPs for the host. |
| `haproxy-haptic.org/cors-allow-credentials` | ✅ Supported | Sets `Access-Control-Allow-Credentials: true` when enabled. |
| `haproxy-haptic.org/cors-allow-headers` | ✅ Supported | Sets the `Access-Control-Allow-Headers` response header. |
| `haproxy-haptic.org/cors-allow-methods` | ✅ Supported | Sets the `Access-Control-Allow-Methods` response header. |
| `haproxy-haptic.org/cors-allow-origin` | ✅ Supported | Sets the allowed origins (comma-separated, with a single-level `*.` wildcard); the matching request `Origin` is echoed back (default `*`). |
| `haproxy-haptic.org/cors-enable` | ✅ Supported | Enables CORS response headers and answers `OPTIONS` requests with `204`. |
| `haproxy-haptic.org/cors-expose-headers` | ✅ Supported | Sets the `Access-Control-Expose-Headers` response header. |
| `haproxy-haptic.org/cors-max-age` | ✅ Supported | Sets the `Access-Control-Max-Age` response header (default `86400`). |
| `haproxy-haptic.org/denylist-source-range` | ✅ Supported | Denies the listed CIDRs and allows all other source IPs for the host. |
| `haproxy-haptic.org/forwardfor` | ✅ Supported | Controls the `X-Forwarded-For` header: `add`, `update`, `ifmissing`, or `ignore`. |
| `haproxy-haptic.org/response-cookie-domain` | ✅ Supported | Rewrites the `Domain` attribute of upstream `Set-Cookie` response headers, given a `<from> <to>` pair, preserving the rest of the cookie string. Host-scoped; a wrong-arity value fails the render. |
| `haproxy-haptic.org/response-cookie-path` | ✅ Supported | Rewrites the `Path` attribute of upstream `Set-Cookie` response headers, given a `<from> <to>` pair, preserving the rest of the cookie string. Host-scoped; a wrong-arity value fails the render. |
| `haproxy-haptic.org/response-location-rewrite-from` | ✅ Supported | Names the literal text to match in the `Location` and `Refresh` response headers; the matched text is regex-escaped and replaced with the value of `response-location-rewrite-to`. Host-scoped. |
| `haproxy-haptic.org/response-location-rewrite-to` | ✅ Supported | Supplies the replacement text for `response-location-rewrite-from`; required whenever a match pattern is set, or the render fails. |
| `haproxy-haptic.org/request-capture` | ✅ Supported | Captures the named request headers (newline-separated) in the logs via `capture request header`, across the whole frontend. |
| `haproxy-haptic.org/request-capture-len` | ✅ Supported | Sets the capture length for `request-capture` (default `128`). |
| `haproxy-haptic.org/request-set-header` | ✅ Supported | Sets request headers sent to the upstream via `http-request set-header`, one `<name> <value>` per line. |
| `haproxy-haptic.org/response-set-header` | ✅ Supported | Sets response headers via `http-response set-header`, one `<name> <value>` per line. |
| `haproxy-haptic.org/src-ip-header` | ✅ Supported | Derives the client source IP from the named request header via `http-request set-src`. |

### Canary and traffic mirroring

Header/cookie/weight-based canary routing and request mirroring via the SPOA hub.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/canary` | ✅ Supported | Marks the Ingress as a canary for a host owned by another Ingress, overlaying a `use_backend` split instead of owning the route. |
| `haproxy-haptic.org/canary-by-cookie` | ✅ Supported | Routes to the canary backend when the named cookie is present. |
| `haproxy-haptic.org/canary-by-header` | ✅ Supported | Routes to the canary backend when the named header is present. |
| `haproxy-haptic.org/canary-by-header-pattern` | ✅ Supported | Routes to the canary backend when the named header matches this regular expression; takes precedence over `canary-by-header-value`. |
| `haproxy-haptic.org/canary-by-header-value` | ✅ Supported | Routes to the canary backend only when the named header equals this value. |
| `haproxy-haptic.org/canary-weight` | ✅ Supported | Sends a percentage of traffic (an integer 0-100) to the canary backend via a weighted random split. |
| `haproxy-haptic.org/mirror-target` | ✅ Supported | Mirrors requests fire-and-forget to a `scheme://host[:port]` target through the SPOA hub's mirror plugin, buffering the request body via `option http-buffer-request`; requires the mirror plugin and a host on the rule. |

### Redirects, HSTS, passthrough, and config injection

HTTP→HTTPS and host redirects (reload-free maps), HSTS, SSL passthrough, a default backend, and raw section injection.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/root-redirect` | ✅ Supported | Redirects requests for the host root path (`/`) to the given sub-path. |
| `haproxy-haptic.org/config-defaults` | ✅ Supported | Injects raw operator-authored directives verbatim into the `defaults` section. |
| `haproxy-haptic.org/config-frontend` | ✅ Supported | Injects raw operator-authored directives into every frontend, before routing. |
| `haproxy-haptic.org/config-global` | ✅ Supported | Injects raw operator-authored HAProxy directives verbatim into the `global` section. |
| `haproxy-haptic.org/default-backend` | ⚠️ Caveat | Routes requests that match the host but none of its configured paths to a named Service as a catch-all backend pool, using the Service's first port. Produces no backend, and no error, when the Service or its first port can't be resolved. |
| `haproxy-haptic.org/default-backend-redirect` | ✅ Supported | Redirects requests that match the host but no path to the given URL. |
| `haproxy-haptic.org/default-backend-redirect-code` | ✅ Supported | Sets the status code for `default-backend-redirect` (default `302`). |
| `haproxy-haptic.org/apex-www-redirect` | ✅ Supported | Issues a `301` redirect between the apex domain and its `www` subdomain, in both directions, preserving the request path and scheme. |
| `haproxy-haptic.org/hsts` | ✅ Supported | Enables HSTS by adding the `Strict-Transport-Security` response header for the host. |
| `haproxy-haptic.org/hsts-include-subdomains` | ✅ Supported | Appends `includeSubDomains` to the `Strict-Transport-Security` header when set to `true`. |
| `haproxy-haptic.org/hsts-max-age` | ✅ Supported | Sets the HSTS `max-age` in seconds (default `63072000`). |
| `haproxy-haptic.org/hsts-preload` | ✅ Supported | Appends `preload` to the `Strict-Transport-Security` header when set to `true`. |
| `haproxy-haptic.org/permanent-redirect` | ✅ Supported | Redirects the host to the given URL with a permanent status code (default `301`). |
| `haproxy-haptic.org/permanent-redirect-code` | ✅ Supported | Sets the status code for `permanent-redirect` (default `301`). |
| `haproxy-haptic.org/ssl-passthrough` | ✅ Supported | Passes TLS through to the backend without terminating it, routed by SNI on a dedicated TCP frontend. |
| `haproxy-haptic.org/https-redirect` | ✅ Supported | Redirects plain HTTP requests for the host to HTTPS. Hosts that also set `https-redirect-port` are handled there instead, avoiding a double redirect. |
| `haproxy-haptic.org/https-redirect-code` | ✅ Supported | Sets the HTTP-to-HTTPS redirect status code (`301`, `302`, `303`, `307`, or `308`; default `302`). |
| `haproxy-haptic.org/https-redirect-port` | ✅ Supported | Redirects plain HTTP requests to HTTPS on an explicit port, preserving the request URI. |
| `haproxy-haptic.org/temporary-redirect` | ✅ Supported | Redirects the host to the given URL with a temporary status code (default `302`). |
| `haproxy-haptic.org/temporary-redirect-code` | ✅ Supported | Sets the status code for `temporary-redirect` (default `302`). |

### Authentication, mTLS, and WAF

Basic auth, client-certificate verification, external/forward auth, OAuth2-proxy, and the Coraza WAF via the SPOA hub.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/auth-headers-fail` | ✅ Supported | Adds response headers on failed external authentication via `http-after-response set-header`. |
| `haproxy-haptic.org/auth-headers-request` | ✅ Supported | Lists the request headers forwarded to the external authentication service. |
| `haproxy-haptic.org/auth-headers-succeed` | ✅ Supported | Adds request headers to the upstream on successful external authentication. |
| `haproxy-haptic.org/auth-method` | ✅ Supported | Overrides the HTTP method used for the external authentication subrequest. |
| `haproxy-haptic.org/auth-realm` | ✅ Supported | Sets the basic-auth realm (default `Restricted`). |
| `haproxy-haptic.org/auth-secret` | ✅ Supported | Names the Secret holding basic-auth credentials; an absent Secret skips the challenge. |
| `haproxy-haptic.org/auth-secret-type` | ✅ Supported | Selects the credentials Secret format: `auth-file` (htpasswd in the `auth` key) or `auth-map` (one key per user); default `auth-file`. |
| `haproxy-haptic.org/auth-signin` | ✅ Supported | Sets the sign-in redirect URL for failed external authentication. |
| `haproxy-haptic.org/auth-tls-cert-header` | ✅ Supported | Forwards the client certificate details (`X-SSL-Client-CN`, `X-SSL-Client-DN`, `X-SSL-Client-Cert`) to the upstream when a client certificate was presented. |
| `haproxy-haptic.org/auth-tls-error-page` | ✅ Supported | Redirects to the given URL when client-certificate (mTLS) verification fails. |
| `haproxy-haptic.org/auth-tls-secret` | ✅ Supported | Enables client-certificate (mTLS) verification for the host using the CA in the named Secret; a host is required. |
| `haproxy-haptic.org/auth-tls-verify-client` | ⚠️ Caveat | Sets client-certificate verification: `on` requires it, `optional` and `optional_no_ca` both map to `verify optional` (HAProxy has no distinct `optional_no_ca` mode), and `off` disables it. |
| `haproxy-haptic.org/auth-type` | ✅ Supported | Enables basic authentication; the only accepted value is `basic`. |
| `haproxy-haptic.org/auth-url` | ✅ Supported | Sets the external authentication service URL; requires the SPOA hub's external-auth plugin. |
| `haproxy-haptic.org/waf-policy` | ✅ Supported | Selects one exact reusable Coraza policy. Definitions come from `extraContext.waf.policies.inline`, explicitly trusted ConfigMaps, or — with `policies.selfService` enabled — the Ingress's own namespace's well-known `waf-policies` ConfigMap; an Ingress can't define or redirect a source. Configuring any catalog source activates policy governance and Coraza automatically. |
| `haproxy-haptic.org/oauth` | ✅ Supported | Enables authentication through `oauth2-proxy` (the only supported provider), building on external auth; skipped when `auth-url` is set. |
| `haproxy-haptic.org/oauth-headers` | ✅ Supported | Lists headers forwarded from the `oauth2-proxy` response on success (default `X-Auth-Request-Email`). |
| `haproxy-haptic.org/oauth-uri-prefix` | ✅ Supported | Sets the `oauth2-proxy` callback path prefix (default `/oauth2`). |
| `haproxy-haptic.org/satisfy` | ✅ Supported | The value `any` grants access when either the source-IP allowlist or basic authentication passes, instead of requiring both. |
| `haproxy-haptic.org/waf-mode` | ✅ Supported | Sets `deny` or `detect`, overriding the selected policy's enforcement only when `waf.ingressPermissions.allowEnforcementOverride` permits it. Requires a selected `waf-policy`. |

#### Reusable WAF policies

Reusable policies separate three responsibilities cleanly:

- The HAPTIC administrator chooses trusted policy sources and owns all Ingress override permissions; configuring the catalog activates policy governance automatically.
- A security team can maintain policy contents in a ConfigMap in a dedicated namespace.
- An Ingress author normally adds only `haproxy-haptic.org/waf-policy: <name>`.

There are no route-selectable built-in profiles and no policy-definition annotation. A name is resolved exactly and case-sensitively against `extraContext.waf.policies.inline` plus the exact `namespace`/`name`/`key` triples in `configMapRefs`. A same-named ConfigMap in an application namespace is ignored — unless the administrator enables [self-service authoring](#self-service-namespaced-policies), which honors exactly one well-known ConfigMap per namespace, for that namespace's own Ingresses only. Duplicate names, missing sources, unknown fields, invalid SecLang, and unknown selections are rejected by the admission webhook; on a live render an unknown or broken selection fails that route closed with `503` and a Warning Event instead of aborting the whole render.

`controller.config.templatingSettings.extraContext.waf.dispatch.mode` controls the global activation model. The default `opt-in` mode sends only annotated routes to Coraza. `default-on` inspects all routes and uses `dispatch.defaultEnforcement` where no selected policy or authorized route override supplies an enforcement mode. This stays in `extraContext` because request dispatch is template-library behaviour and must also be configurable in a raw `HAProxyTemplateConfig`. Coraza's chart-wide directives and low-level plugin parameters remain under `spoaHub.plugins.coraza`.

The following example lets a security team own the catalog in the `security` namespace while application teams select approved policies:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        waf:
          ingressPermissions:
            allowPolicySelection: true
            allowEnforcementOverride: false
            allowWafDisable: false
            allowCustomRules: false
            allowRawHAProxyConfig: false
          policies:
            configMapRefs:
              security:
                namespace: security
                name: haptic-waf-policies
                key: policies.yaml
```

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: security
  name: haptic-waf-policies
data:
  policies.yaml: |
    public-web:
      description: Public browser applications without request-body inspection
      requestBody:
        mode: none
      enforcement: deny
      ruleExclusions:
        - tags: [attack-sqli, attack-xss]
          excludeTarget: "ARGS:q"
    json-api:
      requestBody:
        mode: json
        maxBytes: 4096
      enforcement: deny
    git-host:
      description: Git smart-HTTP server — allowlist git's content type, don't disable the rule
      requestBody:
        mode: none
      enforcement: deny
      crsSettings:
        allowedRequestContentTypes:
          - application/x-git-upload-pack-request
          - application/x-git-receive-pack-request
```

```yaml
metadata:
  annotations:
    haproxy-haptic.org/waf-policy: public-web
```

Each policy supports `description`, `enforcement`, nested `requestBody.mode`/`maxBytes`, `allowedMethods`, `paranoiaLevel`, `anomalyThreshold`, `crsSettings`, `ruleExclusions`, and `secLang`.

`crsSettings` fine-tunes a rule's *inputs* instead of disabling the rule — the preferred first response to a false positive. It's a curated allowlist of Open Worldwide Application Security Project (OWASP) Core Rule Set (CRS) tuning variables. `allowedRequestContentTypes` merges your media types into the standard CRS content-type allowlist — HAPTIC carries a copy of that default because upstream CRS ships it commented out, so it emits the full list (standard types plus yours), keeping rule 920420 active for every other content type. `maxFileSize`, `maxNumArgs`, and `totalArgLength` set a bounded scalar. The git example above is the model: adding the git `application/x-git-upload-pack-request` content type to the allowlist keeps 920420 active, where `ruleExclusions: [920420]` would switch content-type inspection off entirely. Reach for `ruleExclusions` only when a rule is categorically wrong for the application (for example CRS 930130 on a code host); it disables CRS rules by numeric ID — optionally scoped to a URL path by `onPathPrefix`, `onPathSuffix`, `onPathExact`, or `onPathContains` — or removes an exact variable such as `ARGS:q` from a rule or CRS tag. All of these work without application teams writing SecLang; `secLang` remains available to trusted policy authors for cases the structured fields can't express.

`requestBody.mode: none` inspects metadata without buffering or limiting uploads. `any` inspects a complete bounded body; `json` additionally requires a JSON media type. Body routes require an unambiguous `Content-Length`; oversized or incomplete bodies are rejected before Coraza. A policy that omits `requestBody.maxBytes` uses `policies.requestBody.defaultMaxBytes`; it may never exceed `policies.requestBody.maxBytes`. Keeping those two settings separate lets an administrator approve one larger policy without silently enlarging every policy that relied on the default. The effective per-policy cap is set both in HAProxy and in that Coraza application, so neither layer silently inspects a different amount. Template body behavior lives under `extraContext.waf.policies.requestBody`; SPOA timeout/concurrency live only under `spoaHub.plugins.coraza`; the process-global HAProxy buffer lives under `extraContext.requestBodyInspection.haproxyBuffer`.

##### WAF and gRPC streaming

Set `requestBody.mode: none` on any route carrying gRPC client-streaming or bidirectional-streaming calls. Metadata inspection — method, path, headers, source IP — still runs, so the route keeps WAF coverage of everything the engine can actually read.

This isn't a HAPTIC limitation to work around. Coraza buffers a complete request body because that's what makes blocking reliable, and it ships body processors for urlencoded, multipart, JSON, and (partially) XML — [there is no protobuf or gRPC processor](https://www.coraza.io/docs/reference/body-processing/). So even a fully buffered gRPC body is an opaque length-prefixed binary blob to the rule set: CRS finds nothing in it, while every byte still costs buffering. Other Coraza integrations hit the same wall and say so plainly — Solo's WAF server [doesn't support streaming](https://docs.solo.io/kgateway/2.2.x/security/waf/overview/) either, and ModSecurity has carried an [open request to parse gRPC bodies since 2021](https://github.com/owasp-modsecurity/ModSecurity/issues/2645).

A body-inspecting mode (`any` or `json`) therefore can't be combined with a streaming route. Those modes wait for a complete, bounded body, and a streaming request never provides one: it declares no `Content-Length` and holds the body open until the peer is done. The wait runs to `policies.requestBody.waitTimeout` and HAProxy answers `408`, having never contacted the backend. That's fail-closed, not a bypass — a body the WAF can't bound is never forwarded uninspected — but the route stops working, so pick `none` deliberately rather than discovering it in production.

Unary gRPC is unaffected in every mode: its body is complete on arrival, so the wait returns immediately.

Detect (shadow) mode never blocks a streaming request. It waits only for a body whose length is declared and leaves an unbounded one uninspected, so switching a buffered policy to `detect` can't take a streaming route down. A declared body is still buffered in detect mode, keeping shadow verdicts faithful to what enforcement would have decided. Where the wait is skipped the body is reported to the engine as incomplete, so a shadow verdict is never computed over a partly arrived request and then read as a sign that enforcement would have been safe.

HAPTIC enforces this rather than leaving it to be discovered in production. An Ingress that declares a gRPC backend (`haproxy-haptic.org/backend-protocol: grpc`/`grpcs`, or the nginx-compat `GRPC`/`GRPCS`) **and** selects a policy with a body-inspecting `requestBody.mode` is rejected when you apply it, with a message naming the policy and the fix. A route that already carries the combination isn't taken down: it records a `Warning` Event with reason `WafBodyPolicyOnGRPCRoute` and keeps serving, because the runtime already refuses exactly the calls it can't inspect and the unary ones are inspected correctly — there's nothing to fail closed.

Plain `h2`/`h2-ssl` backends aren't affected. HTTP/2 to the backend is how gRPC travels, but it's equally an ordinary HTTP API backend whose bodies are bounded and can be inspected, so the check keys on the unambiguous gRPC declarations only.

**Don't reach for partial-body inspection.** Coraza's `SecRequestBodyLimitAction ProcessPartial` truncates at the limit and runs the rules on what it has, which looks like a way to inspect a stream. It isn't: an attacker prepends padding up to the inspected size and the payload lands in the uninspected remainder. [Coraza documents that bypass](https://www.coraza.io/docs/seclang/directives/), and HAPTIC's `reject` posture is deliberate.

Because body rules can't protect a streaming route, protect it with the controls that don't need the body — all available as annotations on the same route:

| Control | Annotation |
|---------|-----------|
| Per-method authorization (a gRPC path is `/package.Service/Method`) | `allowed-methods`, `allowlist-source-range` |
| Caller identity | `jwt-*`, `api-key-*`, client mTLS |
| Abuse and volume limits | `rate-limit-*` |
| Message size cap | `max-request-body-size` |

For an immutable cluster baseline, configure a default and disable selection:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        waf:
          ingressPermissions:
            allowPolicySelection: false
            allowEnforcementOverride: false
            allowWafDisable: false
            allowCustomRules: false
            allowRawHAProxyConfig: false
          policies:
            defaultPolicy: public-web
```

Removing all WAF annotations doesn't remove this default. HAPTIC also rejects HAPTIC and vendor annotations that select another application, switch to detect mode, disable the WAF, inject SecLang, or inject raw HAProxy configuration. Raw configuration is checked cluster-wide because a frontend, defaults, or global snippet from one Ingress can short-circuit processing for other routes.

The safe defaults are `allowEnforcementOverride: false`, `allowWafDisable: false`, `allowCustomRules: false`, and `allowRawHAProxyConfig: false`. Enforcement-mode overrides and complete WAF opt-outs are deliberately separate permissions: allowing an application team to choose `deny`/`detect` doesn't also let it disable inspection. Turning on `allowCustomRules` grants every Ingress writer arbitrary SecLang capability, including directives that can disable or rewrite policy rules. Turning on `allowRawHAProxyConfig` grants every Ingress writer HAProxy-configuration-administrator capability. Use those switches only where Ingress write access is already trusted at that level. `allowPolicySelection: true` authorizes every Ingress writer to choose any policy in the approved catalog; set it to false with a `defaultPolicy` when that's too broad.

Protect every referenced ConfigMap with Kubernetes RBAC. Anyone who can update one is a WAF policy author. HAPTIC intentionally can't infer the human identity or RBAC path behind a ConfigMap update; the chart establishes the exact source boundary, while Kubernetes authorizes writers to that source.

A policy's directives compile in a deterministic order: chart-wide Coraza/CRS directives, the policy's setup-position tuning (paranoia level, allowed methods, path-scoped rule exclusions) before the CRS include, its config-time rule exclusions after, then HAPTIC's non-overridable body-safety directives. Policies compile once and are shared across every route that selects them. The nginx-compatible custom-rule path (`nginx.ingress.kubernetes.io/modsecurity-snippet`, documented on the nginx-ingress page) creates a private per-Ingress Coraza application and shares `waf.customRules.limits.maxIngresses` and `maxBytesPerIngress`; these DoS bounds apply even when no reusable policy catalog is configured.

#### Self-service namespaced policies

`waf.policies.selfService` lets every namespace author WAF policies for its **own** Ingresses without any per-namespace registration — the admin enables the mode once, and each team owns one well-known ConfigMap (default name `waf-policies`, data key `policies.yaml`) in its namespace:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        waf:
          policies:
            selfService:
              enabled: true
```

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: team-a
  name: waf-policies
data:
  policies.yaml: |
    app-baseline:
      requestBody:
        mode: none
      enforcement: detect
      ruleExclusions:
        - tags: [attack-sqli]
          excludeTarget: "ARGS:q"
```

An Ingress in `team-a` then selects `haproxy-haptic.org/waf-policy: app-baseline` — the same annotation as trusted policies. Names resolve against the trusted catalog first, then the Ingress's own namespace; a policy defined in another namespace is invisible, and explicit cross-namespace addressing (`team-a/app-baseline`) is rejected.

Self-service stays safe for the shared data plane by construction:

- **Namespace-scoped identity.** A self-service policy can't collide with, shadow, or hijack another namespace's or the trusted catalog's names. A name that clashes with a trusted policy is never resolved silently in either direction: the clashing namespace's selectors fail closed while other namespaces still get the trusted policy.
- **Scoped failure.** A broken catalog (invalid YAML) or invalid policy records Warning Events (`WafPolicyCatalogInvalid` / `WafPolicyInvalid`) on the ConfigMap, and only that namespace's *selecting* routes fail closed with `503` — the global render, and every other team, continue untouched. The admission webhook still rejects a change that would introduce the breakage, so the fail-closed path only covers breakage that pre-dates the webhook or raced past it.
- **Bounded content.** `secLang` is refused unless the administrator sets `selfService.allowSecLang: true` — the structured fields (`enforcement`, `requestBody`, `allowedMethods`, `paranoiaLevel`, `anomalyThreshold`, `crsSettings`, `ruleExclusions`) cover false-positive tuning without arbitrary rule code in the shared Coraza process. `requestBody` stays bounded by the administrator's `policies.requestBody.maxBytes` ceiling, and `selfService.limits.maxPoliciesPerNamespace` / `maxTotalPolicies` cap catalog growth (cuts are deterministic: sorted namespaces, sorted names). Note the caps bound size and count, not rule CPU — that's why `allowSecLang` is a separate, off-by-default grant.
- **The baseline stays admin-owned.** `defaultPolicy` resolves in the trusted catalog only, and under `default-on`/`deny` dispatch a self-service policy whose effective enforcement is `detect` is rejected — a tenant can't weaken the cluster baseline.

Enabling self-service activates WAF governance (the `ingressPermissions` gates), the Coraza plugin, and a dedicated, name-scoped ConfigMap watch (only the well-known catalogs are retained in memory, not every cluster ConfigMap). Use `configMapRefs` instead when a central security team authors policies for other teams, and inline policies for admin-only catalogs; all three sources compose.

### API gateway

API-management controls expressed as pure HAProxy config plus low-latency SPOA plugins where HAProxy can't do the work natively: token authentication (API key, JWT, HMAC) that establishes a shared consumer identity, consumer-group authorization, stateless request gating (method/content-type/header validation, mocking, termination), JSON request-body validation, and request correlation IDs.

JWT and API-key auth both set a shared `txn.haptic_consumer` identity (JWT from the `sub` claim, API key from its map), which consumer-group authorization and — in later releases — per-consumer quotas build on.

JSON request-body validation is opt-in via `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true`. Schemas are resolved from ConfigMaps or Secrets and compiled when the bundled plugin initializes/reloads. HAProxy rejects bodies above the route cap before SPOE, waits up to `requestBody.waitTimeout` only on matching POST/PUT/PATCH routes, and then validates against an in-memory compiled schema. The process-global `tune.bufsize` comes from `extraContext.requestBodyInspection.haproxyBuffer.sizeBytes`; `reservedBytes` (default `8192`) protects request headers and rewrite space. Any validator or policy body cap above the remaining capacity fails. Requests without `Content-Length` return `411`, duplicate lengths return `400`, and incomplete buffering returns `413` instead of validating truncated input. Request-body transformation isn't supported.

`haproxy-haptic.org/request-schema-max-body-size` is a validator input cap, not the general upload/body-size policy. Use `haproxy-haptic.org/max-request-body-size` when you want to limit the body size a backend may receive. Use `request-schema-max-body-size` to bound how much body data HAProxy may pass to the API-gateway validator and how much JSON the plugin may parse. If both apply to a validated POST/PUT/PATCH request, either one may return `413`; in practice the stricter applicable limit wins.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/allowed-consumer-groups` | ✅ Supported | Comma-separated group set the route permits; a request whose consumer isn't in an allowed group is denied `403`. Requires `consumer-groups-secret` and an authenticated consumer. |
| `haproxy-haptic.org/allowed-methods` | ✅ Supported | Restricts the accepted HTTP methods (comma-separated); any other method is denied with `405`. |
| `haproxy-haptic.org/api-key-consumer-header` | ✅ Supported | Forwards the resolved consumer id to the upstream in the named header. |
| `haproxy-haptic.org/api-key-header` | ✅ Supported | Header carrying the API key (default `X-API-Key`); mutually exclusive with `api-key-query`. |
| `haproxy-haptic.org/api-key-query` | ✅ Supported | Query parameter carrying the API key; mutually exclusive with `api-key-header`. |
| `haproxy-haptic.org/api-key-secret` | ✅ Supported | Names the Secret (data key `keys`, one `apikey[:consumer]` per line) that becomes a reload-free key→consumer map; an unknown key is denied with `401`, and a valid key sets the shared `txn.haptic_consumer` identity. Fails closed (`503`) while the Secret is absent. |
| `haproxy-haptic.org/consumer-groups-secret` | ✅ Supported | Names the Secret (data key `groups`, one `<consumer>:<group>` per line) mapping each consumer to a group; combined with `allowed-consumer-groups` to authorize. Requires an authenticated consumer and fails closed (`503`) while the Secret is absent. |
| `haproxy-haptic.org/hmac-algorithm` | ✅ Supported | HMAC digest algorithm (default `sha256`; `sha1`/`sha224`/`sha384`/`sha512`). |
| `haproxy-haptic.org/hmac-header` | ✅ Supported | Header carrying the client HMAC signature (default `X-Signature`; lowercase hex). |
| `haproxy-haptic.org/hmac-secret` | ⚠️ Caveat | Names the Secret (data key `secret`) for HMAC request-signature verification (deny `401` on mismatch). The shared key is inlined (base64) into the rendered config and the compare isn't constant-time — prefer JWT. Fails closed (`503`) when the Secret is absent. |
| `haproxy-haptic.org/hmac-signed-string` | ✅ Supported | What the signature covers: `body` (default, buffers the request) or `path`. |
| `haproxy-haptic.org/jwt-algorithm` | ✅ Supported | JWT signature algorithm (default `RS256`); asymmetric only (`RS`/`ES`/`PS` `256`/`384`/`512`) — symmetric `HS*` is rejected so no shared secret is inlined. |
| `haproxy-haptic.org/jwt-audience` | ⚠️ Caveat | Required `aud` claim value (exact match); an array `aud` (multiple audiences) isn't matched — scalar only. |
| `haproxy-haptic.org/jwt-forward-claims` | ✅ Supported | Comma-separated `<claim>:<header>` pairs forwarded upstream after verification; each header is stripped from the client request first (anti-spoof). |
| `haproxy-haptic.org/jwt-issuer` | ✅ Supported | Required `iss` claim value (exact match). |
| `haproxy-haptic.org/jwt-required-claims` | ✅ Supported | Comma-separated claim names that must be present in the payload; a missing claim is denied `401`. |
| `haproxy-haptic.org/jwt-secret` | ✅ Supported | Names the Secret (data key `pubkey.pem`) for asymmetric JWT verification with an alg-confusion guard, `exp`/`iss`/`aud`/required-claim checks, and the shared consumer identity from `sub`. Fails closed (`503`) when the Secret is absent; key rotation needs a reload. |
| `haproxy-haptic.org/mock-response` | ✅ Supported | A non-empty value returns it as a canned response body, short-circuiting the backend (for stubbing an API). |
| `haproxy-haptic.org/mock-response-code` | ✅ Supported | HTTP status for `mock-response` (default `200`). |
| `haproxy-haptic.org/mock-response-content-type` | ✅ Supported | Content-Type for the `mock-response` body (default `application/json`). |
| `haproxy-haptic.org/request-id` | ✅ Supported | The value `true` generates a per-request correlation id and forwards it upstream (HAProxy `unique-id`). |
| `haproxy-haptic.org/request-id-accept-inbound` | ✅ Supported | The value `true` preserves a client-supplied id (used only when the header is absent) instead of always generating a fresh one. |
| `haproxy-haptic.org/request-id-header` | ✅ Supported | Header carrying the correlation id (default `X-Request-ID`). |
| `haproxy-haptic.org/request-schema-configmap` | ✅ Supported | Enables JSON request-body validation using a ConfigMap schema reference: `[namespace/]name[:key]`, default key `schema.json`. Exactly one schema source is required. Requires `extraContext.apiGateway.requestSchemaValidation.enabled=true`. |
| `haproxy-haptic.org/request-schema-content-types` | ✅ Supported | Comma-separated accepted media types for the schema (default `application/json`). The plugin strips `; charset=...` parameters before matching; mismatches return `415`. |
| `haproxy-haptic.org/request-schema-fail-open` | ✅ Supported | Per-route policy for missing plugin verdicts/schema ids (`true` or `false`, default from `extraContext.apiGateway.requestSchemaValidation.defaultFailOpen`, chart default `false`). The default fails closed with `422`. |
| `haproxy-haptic.org/request-schema-max-body-size` | ✅ Supported | Per-route validator input cap (1..1048576; default `requestSchemaValidation.requestBody.defaultMaxBytes`, chart default `8192`). It must fit within `requestBodyInspection.haproxyBuffer.sizeBytes - reservedBytes`. Oversized requests return `413` before SPOE. This doesn't replace `haproxy-haptic.org/max-request-body-size`, the general backend body-size limit. |
| `haproxy-haptic.org/request-schema-secret` | ✅ Supported | Enables JSON request-body validation using a Secret schema reference: `[namespace/]name[:key]`, default key `schema.json`. The Secret data value must be base64-encoded JSON Schema. Exactly one schema source is required. |
| `haproxy-haptic.org/fixed-response` | ✅ Supported | The value `true` returns a fixed response for every request matching the route's hosts via `http-request return` — for maintenance windows or sunset routes. Runs before mocking and the validators. Defaults to status 503 / `text/plain`, and can return a bare status with no body. |
| `haproxy-haptic.org/fixed-response-body` | ✅ Supported | Optional response body for `fixed-response`. |
| `haproxy-haptic.org/fixed-response-code` | ✅ Supported | HTTP status for `fixed-response` (default 503; must be 100-599). |
| `haproxy-haptic.org/fixed-response-content-type` | ✅ Supported | Content-Type for the `fixed-response` body (default `text/plain`). |
| `haproxy-haptic.org/require-content-type` | ✅ Supported | Requires an allowed `Content-Type` (comma-separated) on body methods (POST/PUT/PATCH); a disallowed type is rejected with `415` (prefix-matched, so charset suffixes still match). |
| `haproxy-haptic.org/require-headers` | ✅ Supported | Requires the listed request headers (comma-separated); a request missing any is rejected with `400`. |

<!-- 181 annotations documented -->

## Access-log fields

The library contributes these fields to the [structured access log](../haproxy-deployment.md#access-logging),
each only when the corresponding annotation or feature is in use:

| Field | Contributed when | Meaning |
|-------|------------------|---------|
| `consumer` | any resource sets `jwt-secret` or `api-key-secret` | Authenticated consumer identity — the key a per-consumer rate limit buckets on |
| `cache`, `app_backend` | the Varnish tier is enabled | Varnish's `HIT`/`MISS` verdict, and the application backend the route resolved to (the core `backend` field reads `varnish_cache` for cached routes) |
| `client_ip_peer` | any resource sets `src-ip-header` | The real TCP peer, which is how you spot a client claiming an address it doesn't own once `set-src` has rewritten `client_ip` |
| `captured_headers` | any resource sets `request-capture` | The captured request headers |
| `mtls_verify`, `mtls_cn` | any resource sets `auth-tls-secret` or `auth-tls-cert-header` | The certificate verification result (0 on success, otherwise an X509 error code) and the client's CN |

The presented API key, the computed HMAC signature and the full client
certificate are deliberately never logged.
