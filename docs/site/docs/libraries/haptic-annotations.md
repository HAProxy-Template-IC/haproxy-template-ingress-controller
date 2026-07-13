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
    Keep your existing annotations working by enabling the matching vendor library instead — see [Migrating to HAPTIC](../migrating.md). You can also run both: vendor annotations and `haproxy-haptic.org/*` annotations coexist on the same Ingress.

## Configuration

```yaml
controller:
  templateLibraries:
    hapticAnnotations:
      enabled: true  # Enabled by default
```

Snippet names use the `800`-band priority, so where the same Ingress carries both a `haproxy-haptic.org/*` annotation and a vendor annotation for the same capability, the vendor value (100–799 band) is applied first and the native value layers after it.

## Annotation reference

Every annotation below is `supported` (semantics carried faithfully) or, where HAProxy diverges from the source controller, `different` (works, with the caveat in the note). The library declares no inert annotations.

### Path and host matching

Route which requests reach a backend and alias extra hostnames onto an existing host.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/path-type` | ✅ Supported | Overrides how the path matches when the Ingress `pathType` is `ImplementationSpecific`: `regex`, `exact`, `prefix` (trailing slash normalized), or `begin`. |
| `haproxy-haptic.org/server-alias` | ✅ Supported | Adds extra exact hostnames (comma- or space-separated) that route to the same backends as the Ingress's primary host. |
| `haproxy-haptic.org/server-alias-regex` | ✅ Supported | Adds regular-expression hostnames that route to the same backends as the Ingress's primary host. |

### Backend tuning

Per-backend timeouts, load balancing, connection limits, health/agent checks, and a raw-directive escape hatch.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/agent-check-addr` | ✅ Supported | Sets the agent-check address via `agent-addr`; requires `agent-check-port`. |
| `haproxy-haptic.org/agent-check-interval` | ✅ Supported | Sets the agent-check interval via `agent-inter`; requires `agent-check-port`. |
| `haproxy-haptic.org/agent-check-port` | ✅ Supported | Enables the agent check on the given port (1-65535) via `agent-check` and `agent-port`; required by the other `agent-check-*` keys. |
| `haproxy-haptic.org/agent-check-send` | ✅ Supported | Sets the string sent to the agent check via `agent-send`; requires `agent-check-port`. |
| `haproxy-haptic.org/check` | ✅ Supported | Toggles server health checks; `off` emits `no-check` so servers aren't health-checked. |
| `haproxy-haptic.org/config-backend` | ✅ Supported | Injects raw operator-authored directives verbatim into the `backend` section. |
| `haproxy-haptic.org/fullconn` | ⚠️ Behaviour differs | Emits `fullconn` on the backend. Unlike a hard connection cap, HAProxy's `fullconn` is a soft threshold that scales the per-server `minconn`/`maxconn` ranges. |
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
| `haproxy-haptic.org/upstream-hash-by` | ⚠️ Behaviour differs | Maps nginx's hash key to the closest HAProxy consistent-hash `balance` equivalent (`balance uri`, `balance source`, `balance hdr(...)`, `balance url_param(...)`, or `balance hash req.cook(...)`) with `hash-type consistent`; nginx has no exact analogue. |

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
| `haproxy-haptic.org/backend-ssl-protocols` | ⚠️ Behaviour differs | Maps a space-separated TLS version list to `ssl-min-ver` (lowest) and `ssl-max-ver` (highest). HAProxy expresses only a contiguous span, so a gap in the list (for example, skipping `TLSv1.2`) can't be represented. |
| `haproxy-haptic.org/backend-verify` | ✅ Supported | A truthy value (`on`, `true`, `yes`, `1`) requires upstream certificate verification, and fails closed rather than silently downgrading to `verify none` when no CA is available. |
| `haproxy-haptic.org/backend-verify-host` | ✅ Supported | Sets the expected upstream certificate hostname via `verifyhost`, independent of the SNI value. |

### Rate and bandwidth limiting

Per-source request-rate caps (reload-surviving stick-tables) and per-connection download throttling.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/limit-rate` | ✅ Supported | Throttles per-connection download bandwidth (bytes per second) via a `bwlim-out` filter and `set-bandwidth-limit`, independent of the request-rate caps. |
| `haproxy-haptic.org/limit-rate-after` | ✅ Supported | Sets the number of bytes transferred before `limit-rate` throttling begins, via the `bwlim-out` minimum size. |
| `haproxy-haptic.org/rate-limit-connections` | ✅ Supported | Caps concurrent connections per source IP; ignored when `rate-limit-rps` or `rate-limit-rpm` is set. |
| `haproxy-haptic.org/rate-limit-period` | ⚠️ Behaviour differs | Overrides the stick-table window. When unset, it derives from the active cap (1 second for requests per second, 60 seconds for requests per minute, a 30-second TTL for connection caps) instead of a flat 1-second default, so per-minute limits stay per-minute. |
| `haproxy-haptic.org/rate-limit-rpm` | ✅ Supported | Caps requests per minute per source IP (a 60-second `http_req_rate` window); ignored when `rate-limit-rps` is also set. |
| `haproxy-haptic.org/rate-limit-rps` | ✅ Supported | Caps requests per second per source IP via an `http_req_rate` stick-table; requests over the cap are rejected with the deny status (default `429`), with no burst allowance. |
| `haproxy-haptic.org/rate-limit-size` | ✅ Supported | Sets the stick-table size (default `100k`). |
| `haproxy-haptic.org/rate-limit-status-code` | ✅ Supported | Sets the HTTP status returned to rejected requests (default `429`). |
| `haproxy-haptic.org/rate-limit-whitelist` | ✅ Supported | Exempts comma-separated CIDRs from the rate limit; invalid CIDRs fail the render. |

### Rewriting, retries, and session affinity

Path/target rewriting, body-size limits, upstream retries, Host/header overrides, and cookie-based stickiness.

| Annotation | Status | Behaviour |
|------------|--------|-----------|
| `haproxy-haptic.org/affinity` | ✅ Supported | The value `cookie` enables cookie-based session affinity via the backend `cookie` directive. |
| `haproxy-haptic.org/connection-proxy-header` | ✅ Supported | Overrides the `Connection` header sent to the upstream. |
| `haproxy-haptic.org/path-rewrite` | ✅ Supported | Rewrites the request path via `http-request replace-path`: a `<from> <to>` pair rewrites the match, and a bare value replaces the whole path. |
| `haproxy-haptic.org/proxy-body-size` | ✅ Supported | Limits the request body size (with `k`, `m`, or `g` suffixes), returning `413` when exceeded; `0` means unlimited. |
| `haproxy-haptic.org/proxy-next-upstream` | ⚠️ Behaviour differs | Maps nginx retry conditions to HAProxy `retry-on` values (`error`, `timeout`, `invalid_header`, and `http_<code>`); `non_idempotent` is ignored and `off` disables retries, so the retry surface differs from nginx. |
| `haproxy-haptic.org/proxy-next-upstream-tries` | ✅ Supported | Sets the number of upstream retries via `retries`; `0` keeps the default. |
| `haproxy-haptic.org/rewrite-target` | ✅ Supported | Rewrites the request path via `http-request replace-path`; capture references like `$1` become HAProxy backreferences like `\1`. |
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
| `haproxy-haptic.org/proxy-cookie-domain` | ✅ Supported | Rewrites the `Domain` in upstream `Set-Cookie` headers, given a `<from> <to>` pair. |
| `haproxy-haptic.org/proxy-cookie-path` | ✅ Supported | Rewrites the `Path` in upstream `Set-Cookie` headers, given a `<from> <to>` pair. |
| `haproxy-haptic.org/proxy-redirect-from` | ⚠️ Behaviour differs | Rewrites the `Location` and `Refresh` response headers matching this pattern; nginx's `default` mode isn't supported, and the rewrite applies at the frontend rather than per-backend. |
| `haproxy-haptic.org/proxy-redirect-to` | ⚠️ Behaviour differs | Supplies the replacement text for `proxy-redirect-from`; required whenever that annotation names a real pattern, or the render fails. |
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
| `haproxy-haptic.org/app-root` | ✅ Supported | Redirects requests for the host root (`/`) to the given path. |
| `haproxy-haptic.org/config-defaults` | ✅ Supported | Injects raw operator-authored directives verbatim into the `defaults` section. |
| `haproxy-haptic.org/config-frontend` | ✅ Supported | Injects raw operator-authored directives into every frontend, before routing. |
| `haproxy-haptic.org/config-global` | ✅ Supported | Injects raw operator-authored directives verbatim into the `global` section. |
| `haproxy-haptic.org/default-backend` | ⚠️ Behaviour differs | Routes unmatched requests for the host to a named Service (its first port) as a catch-all backend; silently skipped when the Service or port can't be resolved, unlike haproxy-ingress's redirect-based default backend. |
| `haproxy-haptic.org/default-backend-redirect` | ✅ Supported | Redirects requests that match the host but no path to the given URL. |
| `haproxy-haptic.org/default-backend-redirect-code` | ✅ Supported | Sets the status code for `default-backend-redirect` (default `302`). |
| `haproxy-haptic.org/from-to-www-redirect` | ✅ Supported | Issues a `301` redirect between the apex domain and its `www` subdomain. |
| `haproxy-haptic.org/hsts` | ✅ Supported | Enables HSTS by adding the `Strict-Transport-Security` response header for the host. |
| `haproxy-haptic.org/hsts-include-subdomains` | ✅ Supported | Appends `includeSubDomains` to the `Strict-Transport-Security` header when set to `true`. |
| `haproxy-haptic.org/hsts-max-age` | ✅ Supported | Sets the HSTS `max-age` in seconds (default `63072000`). |
| `haproxy-haptic.org/hsts-preload` | ✅ Supported | Appends `preload` to the `Strict-Transport-Security` header when set to `true`. |
| `haproxy-haptic.org/permanent-redirect` | ✅ Supported | Redirects the host to the given URL with a permanent status code (default `301`). |
| `haproxy-haptic.org/permanent-redirect-code` | ✅ Supported | Sets the status code for `permanent-redirect` (default `301`). |
| `haproxy-haptic.org/ssl-passthrough` | ✅ Supported | Passes TLS through to the backend without terminating it, routed by SNI on a dedicated TCP frontend. |
| `haproxy-haptic.org/ssl-redirect` | ✅ Supported | Redirects plain HTTP requests for the host to HTTPS; hosts that also set `ssl-redirect-port` are handled there instead to avoid a double redirect. |
| `haproxy-haptic.org/ssl-redirect-code` | ✅ Supported | Sets the HTTP-to-HTTPS redirect status code (`301`, `302`, `303`, `307`, or `308`; default `302`). |
| `haproxy-haptic.org/ssl-redirect-port` | ✅ Supported | Redirects plain HTTP requests to HTTPS on an explicit port, preserving the URI. |
| `haproxy-haptic.org/temporal-redirect` | ✅ Supported | Redirects the host to the given URL with a temporary status code (default `302`). |
| `haproxy-haptic.org/temporal-redirect-code` | ✅ Supported | Sets the status code for `temporal-redirect` (default `302`). |

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
| `haproxy-haptic.org/auth-tls-verify-client` | ⚠️ Behaviour differs | Sets client-certificate verification: `on` requires it, `optional` and `optional_no_ca` both map to `verify optional` (HAProxy has no distinct `optional_no_ca` mode), and `off` disables it. |
| `haproxy-haptic.org/auth-type` | ✅ Supported | Enables basic authentication; the only accepted value is `basic`. |
| `haproxy-haptic.org/auth-url` | ✅ Supported | Sets the external authentication service URL; requires the SPOA hub's external-auth plugin. |
| `haproxy-haptic.org/modsecurity-snippet` | ✅ Supported | Adds per-application Coraza WAF rules for the Ingress; requires the Coraza plugin. |
| `haproxy-haptic.org/oauth` | ✅ Supported | Enables authentication through `oauth2-proxy` (the only supported provider), building on external auth; skipped when `auth-url` is set. |
| `haproxy-haptic.org/oauth-headers` | ✅ Supported | Lists headers forwarded from the `oauth2-proxy` response on success (default `X-Auth-Request-Email`). |
| `haproxy-haptic.org/oauth-uri-prefix` | ✅ Supported | Sets the `oauth2-proxy` callback path prefix (default `/oauth2`). |
| `haproxy-haptic.org/satisfy` | ✅ Supported | The value `any` grants access when either the source-IP allowlist or basic authentication passes, instead of requiring both. |
| `haproxy-haptic.org/waf` | ✅ Supported | The value `modsecurity` enables the Coraza WAF for the Ingress; requires the Coraza plugin. |
| `haproxy-haptic.org/waf-mode` | ✅ Supported | Sets the WAF mode: `deny` (default) or `detect`; requires `waf` to be set. |

<!-- 134 annotations documented -->
