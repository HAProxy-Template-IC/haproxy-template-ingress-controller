---
hide:
  - navigation
---

# Migrating to HAPTIC

How to move an existing cluster from **ingress-nginx** or **haproxy-ingress** to
HAPTIC with zero downtime — run both controllers side by side, cut over one
Ingress at a time, and flip DNS only when you're ready.

HAPTIC is built to coexist with your current controller: it ships a distinct
IngressClass (`haptic`, **not** cluster-default) and its own HAProxy Service, so
it adopts *only* the Ingresses you explicitly point at it. Nothing you have today
moves until you move it, and every step is a `kubectl patch` away from rollback.

Work through the cutover below in order. If something doesn't route as expected,
the [Troubleshooting](#troubleshooting) section at the end covers the handful of
defaults that most often trip up a migration.

## Before you start

- Your incumbent controller (ingress-nginx / haproxy-ingress) is still running and serving traffic. **Leave it running** until cutover is complete.
- You have Helm and cluster access. Step 1 below installs HAPTIC with the migration-specific flags. If HAPTIC is *already* installed, that's fine — it adopts nothing until you point Ingresses at its class; just apply the same flags with `helm upgrade` instead.
- You can edit Ingress manifests (to change `ingressClassName`) or you accept renaming HAPTIC's class to match — see below.

## Step 0: Check what will change

Before you touch a single Ingress, run `migrate-check` to see exactly how your
current Ingresses fare under HAPTIC. It reads your Ingresses, classifies every
source-controller annotation as supported, different, dropped, or blocking, and
renders each Ingress through HAPTIC's real template pipeline to catch anything
that would be rejected — so you find the surprises now, not mid-cutover.

The controller image carries the tool and the chart, so a read-only audit of the
live cluster is a single command (it only lists and reads Ingresses — it changes
nothing):

```bash
docker run --rm \
  -v ~/.kube/config:/kube/config:ro -e KUBECONFIG=/kube/config \
  registry.gitlab.com/haproxy-haptic/haptic:latest migrate-check
```

Read the verdict on the first line. Exit code `0` means every checked annotation
is fully supported; `1` means there are differences or unknown annotations to
review; `2` means there are blockers — annotations HAPTIC rejects, or Ingresses
that fail to render — fix those before cutover.

To audit without cluster access — in CI, or against manifests you export with
`kubectl get ingress -A -o yaml > ingresses.yaml` — point the tool at a directory
of manifests and a directory of Kubernetes schemas instead:

```bash
haptic-controller migrate-check \
  --resources ./manifests --schema-dir ./schemas \
  --output markdown
```

Useful flags:

- `-n, --namespace <ns>` — audit only one namespace.
- `-o, --output text|json|markdown` — `text` (default) is the operator report;
  `json` feeds a script; `markdown` drops into a migration ticket.
- `-f, --file <config.yaml>` — classify against a specific HAProxyTemplateConfig
  instead of the image-embedded chart.
- `--resources <dir>` — read Ingress manifests from a directory instead of the
  live cluster.
- `--schema-dir <dir>` — read resource schemas from a directory instead of the
  live cluster.

## The cutover, step by step

1. **Install HAPTIC alongside** your existing controller. Give HAProxy a real
   external address — the default `haproxy.service.type` is **`NodePort`**; set
   it to `LoadBalancer` so status/DNS get a routable address:

    ```bash
    helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --namespace haptic --create-namespace \
      --set haproxy.service.type=LoadBalancer \
      --set controller.statusPatches.enabled=false   # no DNS writes yet
    ```

    If HAPTIC is already installed, apply the same flags with `helm upgrade`:

    ```bash
    helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --namespace haptic --reuse-values \
      --set haproxy.service.type=LoadBalancer \
      --set controller.statusPatches.enabled=false   # no DNS writes yet
    ```

2. **Enable the right annotation library** for your source controller — see
   [From ingress-nginx](#from-ingress-nginx) or
   [From haproxy-ingress](#from-haproxy-ingress).

3. **Move one test Ingress** to HAPTIC by changing only its class:

    ```bash
    kubectl patch ingress my-test-app \
      --type merge -p '{"spec":{"ingressClassName":"haptic"}}'
    ```

    The incumbent controller drops it; HAPTIC picks it up. Verify routing
    directly against HAPTIC's HAProxy Service address (curl with a `Host:`
    header) before touching DNS.

4. **Bulk cut over** once you're confident: change `ingressClassName` on the
   remaining Ingresses (in batches you can roll back).

5. **Flip DNS / enable status.** Set `controller.statusPatches.enabled=true`
   (so `external-dns` and dashboards see HAPTIC's address) and/or repoint DNS to
   HAPTIC's load balancer. Watch traffic.

6. **Decommission** the old controller once all Ingresses are served by HAPTIC
   and traffic is stable.

!!! tip "Rolling back"
    Until DNS is flipped, rollback is just `kubectl patch` the
    `ingressClassName` back. Keep the old controller installed until step 6.

## Key settings that affect migration

| Setting | Default | Why it matters |
|---------|---------|----------------|
| `ingressClass.name` | `haptic` | Only Ingresses with this exact `ingressClassName` are served. |
| `ingressClass.default` | `false` | Class-less Ingresses are **not** adopted; leave `false` during migration. |
| `controller.templateLibraries.nginxIngress.enabled` | `false` | Turn on for `nginx.ingress.kubernetes.io/*` annotations. |
| `controller.templateLibraries.haproxyIngress.enabled` | `true` | `haproxy-ingress.github.io/*` annotations (already on). |
| `controller.templateLibraries.haproxytech.enabled` | `true` | `haproxy.org/*` annotations (already on). |
| `controller.statusPatches.enabled` | `true` | Writes Ingress/Gateway status; **disable during migration**. |
| `haproxy.service.type` | `NodePort` | Set to `LoadBalancer` for a routable external address. |

---

## From ingress-nginx

### 1. Match the IngressClass

HAPTIC scopes Ingresses with a server-side field selector
`spec.ingressClassName=<ingressClass.name>`. Your Ingresses carry
`ingressClassName: nginx`, so pick one:

=== "Edit Ingress manifests (recommended)"

    Change `ingressClassName: nginx` → `ingressClassName: haptic` per Ingress.
    This is what enables the one-at-a-time, reversible cutover above.

=== "Rename HAPTIC's class to `nginx`"

    ```bash
    --set ingressClass.name=nginx
    ```

    HAPTIC then adopts every `ingressClassName: nginx` Ingress at once. Faster,
    but **all-or-nothing** and it will collide with ingress-nginx if both run —
    only do this after ingress-nginx is scaled down.

!!! note
    Marking HAPTIC's IngressClass cluster-default (`ingressClass.default: true`)
    does **not** help: the match is on the literal `spec.ingressClassName` value,
    so default-class resolution and class-less Ingresses are never picked up.

### 2. Enable the annotation library

```bash
--set controller.templateLibraries.nginxIngress.enabled=true
```

!!! warning "The flag is camelCase"
    It's `nginxIngress`, not `nginx-ingress`. `--set …nginx-ingress.enabled=true`
    silently does nothing.

Enabling it also pulls in the SPOA-hub sidecar (the Coraza WAF and external-auth
plugins auto-enable), adding ~50 MB to the HAProxy pod. Basic host/path routing
works without this library; only the `nginx.ingress.kubernetes.io/*` annotations
need it.

### 3. Control the DNS cutover

Keep `controller.statusPatches.enabled=false` until you've verified routing.
With it on, the moment HAPTIC's HAProxy Service has an address it stamps
`.status.loadBalancer` onto every adopted Ingress, and `external-dns` will
repoint DNS — a premature, unverified cutover.

### Annotation support

The library covers the common `nginx.ingress.kubernetes.io/*` annotations —
backend timeouts, `load-balance`, `proxy-body-size`, `rewrite-target`,
`ssl-redirect`/`force-ssl-redirect`, HSTS, CORS, `whitelist`/`denylist-source-range`,
custom headers, basic + external auth, `ssl-passthrough`, canary, client mTLS,
request mirroring (`mirror-target`), and ModSecurity. Full per-annotation reference:
[nginx-ingress library docs](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/charts/haptic/docs/libraries/nginx-ingress.md).

The table below lists every annotation that does **not** carry over unchanged —
generated from the library's declared migration coverage, so it can't drift from
the template code. Anything not listed is fully supported.

<!-- BEGIN generated: migration-coverage ingress-nginx -->
The library classifies 102 `nginx.ingress.kubernetes.io/*` annotations: 56 supported, 30 with behaviour differences, 16 not carried over, 0 failing.

| Annotation | Status | What to check |
|------------|--------|---------------|
| `nginx.ingress.kubernetes.io/auth-method` | Behaviour differs | Overrides the auth subrequest method; POST/PUT/PATCH are sent with an empty body. |
| `nginx.ingress.kubernetes.io/auth-secret` | Behaviour differs | htpasswd Secret format is unchanged from ingress-nginx, but a missing Secret disables auth for the route until the Secret appears — ingress-nginx serves 503 instead. |
| `nginx.ingress.kubernetes.io/auth-signin` | Behaviour differs | nginx variables ($escaped_request_uri, …) are not expanded — the URL is used verbatim. |
| `nginx.ingress.kubernetes.io/auth-snippet` | Not carried over | Freeform nginx configuration can't be translated to HAProxy; the haproxy-ingress library's auth-headers-request annotation covers the common use case. |
| `nginx.ingress.kubernetes.io/auth-tls-pass-certificate-to-upstream` | Behaviour differs | Forwards ssl-client-cert (base64 DER — ingress-nginx sends URL-encoded PEM) and ssl-client-subject-dn; ssl-client-verify and ssl-client-issuer-dn are not set. |
| `nginx.ingress.kubernetes.io/auth-tls-secret` | Behaviour differs | Client-CA verification is keyed by SNI — every rule needs an explicit host or the render fails; a missing Secret (or missing ca.crt) skips mTLS for the Ingress with a rendered warning. |
| `nginx.ingress.kubernetes.io/auth-tls-verify-client` | Behaviour differs | "on"→required; "optional" and "optional_no_ca"→optional (HAProxy has no verify-but-accept-invalid mode); other values fail the render. |
| `nginx.ingress.kubernetes.io/auth-tls-verify-depth` | Not carried over | HAProxy has no per-server/per-crt-list chain-depth option; a warning comment is rendered and the CA bundle scope bounds the accepted chain instead. |
| `nginx.ingress.kubernetes.io/auth-type` | Behaviour differs | Only "basic" is supported; "digest" fails the render. |
| `nginx.ingress.kubernetes.io/backend-protocol` | Behaviour differs | HTTP, HTTPS, GRPC and GRPCS map to HAProxy server options; AJP and FCGI have no HAProxy equivalent and fail the render with an error. |
| `nginx.ingress.kubernetes.io/canary-weight-total` | Not carried over | The weight base is fixed at 100. |
| `nginx.ingress.kubernetes.io/configuration-snippet` | Behaviour differs | Injected verbatim into the backend section — the value must contain HAProxy directives, not nginx configuration; existing nginx snippets need rewriting. |
| `nginx.ingress.kubernetes.io/cors-allow-credentials` | Behaviour differs | The header is only sent when explicitly "true" — ingress-nginx defaults it to true. |
| `nginx.ingress.kubernetes.io/cors-allow-origin` | Behaviour differs | Emitted verbatim into Access-Control-Allow-Origin; ingress-nginx's dynamic multi-origin matching is not performed — use a single origin or "*". |
| `nginx.ingress.kubernetes.io/denylist-source-range` | Behaviour differs | Host-scoped — the denylist only gates rules with an explicit host, so an Ingress without rule hosts gets no filtering; invalid CIDRs fail the render. |
| `nginx.ingress.kubernetes.io/enable-cors` | Behaviour differs | Header-injection only — OPTIONS preflights are forwarded to the backend instead of being answered by HAProxy (ingress-nginx answers them with 204), so the backend must accept OPTIONS. |
| `nginx.ingress.kubernetes.io/enable-modsecurity` | Behaviour differs | "false" opts the route out of the WAF; "true" is accepted as a no-op (dispatch is default-on when the coraza plugin is enabled); other values fail the render. |
| `nginx.ingress.kubernetes.io/enable-opentelemetry` | Not carried over | Requires the nginx OpenTelemetry module; no HAProxy-side tracing is wired. |
| `nginx.ingress.kubernetes.io/enable-opentracing` | Not carried over | Requires the nginx OpenTracing module; no HAProxy-side tracing is wired. |
| `nginx.ingress.kubernetes.io/hsts` | Behaviour differs | The header is emitted only when the annotation is explicitly "true" on the Ingress — ingress-nginx enables HSTS globally by default. |
| `nginx.ingress.kubernetes.io/hsts-include-subdomains` | Behaviour differs | includeSubDomains is added only when explicitly "true" — ingress-nginx defaults it to true. |
| `nginx.ingress.kubernetes.io/limit-connections` | Behaviour differs | Rejects with 429, and ignored when limit-rps or limit-rpm is set (one stick-table per backend). |
| `nginx.ingress.kubernetes.io/limit-rpm` | Behaviour differs | Same hard-cap/429 semantics as limit-rps, and ignored when limit-rps is also set (HAProxy stores one request-rate counter per backend). |
| `nginx.ingress.kubernetes.io/limit-rps` | Behaviour differs | Hard per-source-IP cap rejecting with 429 — ingress-nginx allows a 5x burst and rejects with 503. |
| `nginx.ingress.kubernetes.io/mirror-host` | Not carried over | The mirror plugin forces the mirrored Host header to the target authority. |
| `nginx.ingress.kubernetes.io/mirror-request-body` | Not carried over | The buffered request body is always forwarded to the mirror target. |
| `nginx.ingress.kubernetes.io/mirror-target` | Behaviour differs | Mirrors via the SPOA hub mirror plugin — requires spoaHub.plugins.mirror and a rule host (the render fails otherwise); only the URL's authority is used, the live request path/query is re-attached. |
| `nginx.ingress.kubernetes.io/opentelemetry-operation-name` | Not carried over | Requires the nginx OpenTelemetry module; no HAProxy-side tracing is wired. |
| `nginx.ingress.kubernetes.io/opentelemetry-trust-incoming-span` | Not carried over | Requires the nginx OpenTelemetry module; no HAProxy-side tracing is wired. |
| `nginx.ingress.kubernetes.io/opentracing-trust-incoming-span` | Not carried over | Requires the nginx OpenTracing module; no HAProxy-side tracing is wired. |
| `nginx.ingress.kubernetes.io/proxy-cookie-domain` | Behaviour differs | Only the "<from> <to>" rewrite form is supported; any other value (including "off") fails the render. |
| `nginx.ingress.kubernetes.io/proxy-cookie-path` | Behaviour differs | Only the "<from> <to>" rewrite form is supported; any other value (including "off") fails the render. |
| `nginx.ingress.kubernetes.io/proxy-max-temp-file-size` | Not carried over | HAProxy buffers in memory; there is no temp-file spooling. |
| `nginx.ingress.kubernetes.io/proxy-next-upstream` | Behaviour differs | Maps to HAProxy retry-on (error→conn-failure, timeout→response-timeout, invalid_header→junk-response, http_NNN→NNN); non_idempotent has no equivalent and is ignored; "off" emits retries 0. |
| `nginx.ingress.kubernetes.io/proxy-read-timeout` | Behaviour differs | Collapses with proxy-send-timeout into HAProxy's single timeout server — the larger value wins, asymmetric read/send timeouts are lost. |
| `nginx.ingress.kubernetes.io/proxy-redirect-from` | Behaviour differs | "default" is not supported (warning comment, no rewrite); requires proxy-redirect-to; values must be space-free. |
| `nginx.ingress.kubernetes.io/proxy-send-timeout` | Behaviour differs | Collapses with proxy-read-timeout into HAProxy's single timeout server — the larger value wins, asymmetric read/send timeouts are lost. |
| `nginx.ingress.kubernetes.io/proxy-ssl-server-name` | Not carried over | Not read; SNI toward the upstream is controlled via proxy-ssl-name instead. |
| `nginx.ingress.kubernetes.io/proxy-ssl-verify-depth` | Not carried over | HAProxy has no per-server chain-depth option; a warning comment is rendered. |
| `nginx.ingress.kubernetes.io/satisfy` | Behaviour differs | "any" OR-combines whitelist-source-range with basic auth only; unlike ingress-nginx it does not extend to external auth (auth-url). |
| `nginx.ingress.kubernetes.io/server-snippet` | Not carried over | nginx server-level directives have no HAProxy equivalent. |
| `nginx.ingress.kubernetes.io/session-cookie-expires` | Behaviour differs | Emitted as a Max-Age attribute — HAProxy can't compute an absolute Expires date; browsers treat both equivalently. |
| `nginx.ingress.kubernetes.io/session-cookie-hash` | Not carried over | HAProxy's dynamic-cookie hashing is not selectable; the value is ignored and a warning comment is rendered. |
| `nginx.ingress.kubernetes.io/ssl-redirect` | Behaviour differs | Redirects only when explicitly "true" — ingress-nginx redirects TLS-enabled Ingresses by default; the code comes from extraContext.nginxHttpRedirectCode (default 308, matching ingress-nginx's http-redirect-code). |
| `nginx.ingress.kubernetes.io/stream-snippet` | Not carried over | nginx stream directives have no HAProxy equivalent. |
| `nginx.ingress.kubernetes.io/whitelist-source-range` | Behaviour differs | Host-scoped — the allowlist only gates rules with an explicit host, so an Ingress without rule hosts gets no filtering; invalid CIDRs fail the render. |
<!-- END generated: migration-coverage ingress-nginx -->

---

## From haproxy-ingress

The `haproxy-ingress.github.io/*` library is **enabled by default**, so
jcmoraisjr/haproxy-ingress annotations work with no flag change. You still need
to [match the IngressClass](#1-match-the-ingressclass) (your Ingresses likely
use `ingressClassName: haproxy` — either edit them or `--set ingressClass.name=haproxy`)
and [control the DNS cutover](#3-control-the-dns-cutover) the same way.

Most routing, SSL, session-affinity, redirect, HSTS, CORS, access-control,
basic/external auth, client-mTLS, and WAF annotations are supported. Full
reference:
[haproxy-ingress library docs](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/charts/haptic/docs/libraries/haproxy-ingress.md).

The table below lists every annotation that does **not** carry over unchanged —
generated from the library's declared migration coverage. Anything not listed is
fully supported.

<!-- BEGIN generated: migration-coverage haproxy-ingress -->
The library classifies 92 `haproxy-ingress.github.io/*` annotations: 59 supported, 31 with behaviour differences, 2 not carried over, 0 failing.

| Annotation | Status | What to check |
|------------|--------|---------------|
| `haproxy-ingress.github.io/agent-check-addr` | Behaviour differs | Has no effect without agent-check-port — and setting it (or -interval/-send) without agent-check-port fails the render. |
| `haproxy-ingress.github.io/agent-check-interval` | Behaviour differs | Has no effect without agent-check-port — and setting it without agent-check-port fails the render. |
| `haproxy-ingress.github.io/agent-check-send` | Behaviour differs | Has no effect without agent-check-port — and setting it without agent-check-port fails the render. |
| `haproxy-ingress.github.io/allowlist-source-range` | Behaviour differs | Host-scoped — only gates rules with an explicit host; invalid CIDRs fail the render. |
| `haproxy-ingress.github.io/auth-method` | Behaviour differs | Overrides the auth subrequest method; POST/PUT/PATCH are sent with an empty body. |
| `haproxy-ingress.github.io/auth-secret` | Behaviour differs | Secret format is one key per username with a base64(bcrypt/apr1 hash) value — different from ingress-nginx's htpasswd; a migrated htpasswd Secret will not authenticate. A missing Secret disables auth for the route until it appears. |
| `haproxy-ingress.github.io/auth-tls-cert-header` | Behaviour differs | Forwards X-SSL-Client-CN, X-SSL-Client-DN and X-SSL-Client-Cert when "true"; jcmoraisjr/haproxy-ingress additionally forwards the SHA1 and serial headers, which are not set. |
| `haproxy-ingress.github.io/auth-tls-secret` | Behaviour differs | Verification is keyed by SNI — every rule needs an explicit host or the render fails; a missing Secret (or missing ca.crt) skips mTLS for the Ingress with a rendered warning. |
| `haproxy-ingress.github.io/auth-tls-strict` | Not carried over | Accepted for compatibility but has no separate effect; for soft verification use auth-tls-verify-client optional instead. |
| `haproxy-ingress.github.io/auth-tls-verify-client` | Behaviour differs | "on"→required; "optional"/"optional_no_ca"→optional (HAProxy has no verify-but-accept-invalid mode); other values fail the render. |
| `haproxy-ingress.github.io/auth-url` | Behaviour differs | External auth via the SPOA hub external-auth plugin, which (unlike the nginx-ingress library) is NOT auto-enabled — enable spoaHub.plugins.external-auth, otherwise the auth is silently not enforced. |
| `haproxy-ingress.github.io/backend-protocol` | Behaviour differs | h1, h2, h1-ssl and h2-ssl are accepted (h1-ssl/h2-ssl enable TLS); other values fail the render — note this is a different value set from ingress-nginx's HTTP/HTTPS/GRPC/GRPCS. |
| `haproxy-ingress.github.io/config-frontend` | Behaviour differs | Injected into HAPTIC's shared HTTP frontend (before routing), not a per-Ingress frontend — directives apply process-wide; deduplication is the operator's responsibility. |
| `haproxy-ingress.github.io/cors-allow-origin` | Behaviour differs | Emitted verbatim into Access-Control-Allow-Origin; dynamic multi-origin matching is not performed — use a single origin or "*". |
| `haproxy-ingress.github.io/cors-enable` | Behaviour differs | Header-injection only — OPTIONS preflights are forwarded to the backend rather than answered by HAProxy, so the backend must accept OPTIONS. |
| `haproxy-ingress.github.io/default-backend-redirect-code` | Behaviour differs | Default 302; an invalid code fails the render. |
| `haproxy-ingress.github.io/denylist-source-range` | Behaviour differs | Host-scoped — only gates rules with an explicit host; invalid CIDRs fail the render. |
| `haproxy-ingress.github.io/docs` | Not carried over | A pointer to jcmoraisjr/haproxy-ingress documentation, not a configuration key; not read. |
| `haproxy-ingress.github.io/hsts` | Behaviour differs | The header is emitted only when the annotation is explicitly "true" on the Ingress — jcmoraisjr/haproxy-ingress enables HSTS globally by default. |
| `haproxy-ingress.github.io/limit-connections` | Behaviour differs | Maps to backend fullconn (a soft full-queue threshold) rather than a hard per-server connection cap; must be a positive integer. |
| `haproxy-ingress.github.io/limit-rpm` | Behaviour differs | Same hard-cap/429 semantics, and ignored when limit-rps is also set (one stick-table per backend). |
| `haproxy-ingress.github.io/limit-rps` | Behaviour differs | Hard per-source-IP cap rejecting with 429 — jcmoraisjr/haproxy-ingress applies a burst allowance. |
| `haproxy-ingress.github.io/oauth` | Behaviour differs | Only "oauth2_proxy"/"oauth2-proxy" is supported and requires an Ingress path (oauth-uri-prefix, default /oauth2) routing to the oauth2-proxy Service — otherwise the render fails; auth-url takes precedence; needs the external-auth plugin (not auto-enabled) with plaintext allowed. |
| `haproxy-ingress.github.io/path-type` | Behaviour differs | regex, exact, prefix and begin are honoured, but only for paths with pathType ImplementationSpecific — the annotation is ignored on Prefix/Exact-typed paths. |
| `haproxy-ingress.github.io/redirect-to-code` | Behaviour differs | Default 302; an out-of-range code silently falls back to 302 rather than failing. |
| `haproxy-ingress.github.io/secure-crt-secret` | Behaviour differs | Presents a client certificate to the upstream from the Secret; a missing Secret or missing tls.crt/tls.key renders a warning comment and skips the client cert instead of failing. |
| `haproxy-ingress.github.io/secure-verify-ca-secret` | Behaviour differs | Verifies the upstream certificate against the Secret's ca.crt; a missing Secret or missing ca.crt renders a warning comment and silently downgrades to no verification instead of failing. |
| `haproxy-ingress.github.io/ssl-cipher-suites-backend` | Behaviour differs | TLS 1.3 cipher suites for upstream TLS — only applied when backend TLS is enabled; otherwise silently ignored. |
| `haproxy-ingress.github.io/ssl-ciphers-backend` | Behaviour differs | Cipher list for upstream TLS — only applied when backend TLS is enabled (secure-backends or an -ssl backend-protocol); otherwise silently ignored. |
| `haproxy-ingress.github.io/ssl-redirect-code` | Behaviour differs | Default 302 (jcmoraisjr/haproxy-ingress redirects with 302); an out-of-range code silently falls back to 302 rather than failing. |
| `haproxy-ingress.github.io/waf` | Behaviour differs | Only "modsecurity" is supported (enforced by the bundled Coraza WAF via the SPOA hub); requires the coraza plugin (auto-enabled with this library) or the render fails. |
| `haproxy-ingress.github.io/waf-mode` | Behaviour differs | "deny" (default) enforces, "detect" runs in shadow mode; other values, or waf-mode without waf, fail the render. |
| `haproxy-ingress.github.io/whitelist-source-range` | Behaviour differs | Deprecated alias of allowlist-source-range, honoured only when allowlist-source-range is absent; host-scoped. |
<!-- END generated: migration-coverage haproxy-ingress -->

---

## From haproxytech/kubernetes-ingress

The `haproxy.org/*` library (the official haproxytech/kubernetes-ingress
annotation set) is **enabled by default**, so those annotations work with no flag
change. You still need to [match the IngressClass](#1-match-the-ingressclass)
(your Ingresses likely use `ingressClassName: haproxy`) and
[control the DNS cutover](#3-control-the-dns-cutover) the same way.

HAPTIC reads these annotations on **Ingress** resources only. haproxytech's
controller also reads many of them on Service and ConfigMap resources; that
Service/ConfigMap-level configuration does not carry over. Full reference:
[haproxytech library docs](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/charts/haptic/docs/libraries/haproxytech.md).

The table below lists every annotation that does **not** carry over unchanged —
generated from the library's declared migration coverage. Anything not listed is
fully supported.

<!-- BEGIN generated: migration-coverage haproxytech -->
The library classifies 55 `haproxy.org/*` annotations: 33 supported, 18 with behaviour differences, 4 not carried over, 0 failing.

| Annotation | Status | What to check |
|------------|--------|---------------|
| `haproxy.org/allow-list` | Behaviour differs | Host-scoped source-IP allowlist — only gates rules with an explicit host; invalid CIDRs fail the render. |
| `haproxy.org/auth-realm` | Behaviour differs | Default "RestrictedArea"; a realm containing spaces fails the render unless extraContext.sanitize_auth_realm is set (DataPlane API limitation). |
| `haproxy.org/auth-secret` | Behaviour differs | Secret format is one key per username with a base64(hash) value — different from ingress-nginx's htpasswd; a missing Secret disables auth for the route until it appears. |
| `haproxy.org/auth-type` | Behaviour differs | Only "basic-auth" is supported; other values fail the render (note the value differs from ingress-nginx's "basic"). |
| `haproxy.org/blacklist` | Behaviour differs | Deprecated alias of deny-list, honoured only when deny-list is absent; host-scoped. |
| `haproxy.org/cookie-persistence-no-dynamic` | Behaviour differs | Static (non-dynamic) cookie stickiness; setting it together with cookie-persistence fails the render. |
| `haproxy.org/cors-allow-credentials` | Behaviour differs | Only emitted when "true"; combining it with cors-allow-origin "*" fails the render. |
| `haproxy.org/cors-allow-methods` | Behaviour differs | Default is "GET, POST" (narrower than ingress-nginx's default method set). |
| `haproxy.org/cors-allow-origin` | Behaviour differs | Emitted verbatim into Access-Control-Allow-Origin; combining "*" with cors-allow-credentials "true" fails the render. |
| `haproxy.org/cors-enable` | Behaviour differs | Header-injection only — OPTIONS preflights are forwarded to the backend rather than answered by HAProxy, so the backend must accept OPTIONS. |
| `haproxy.org/deny-list` | Behaviour differs | Host-scoped source-IP denylist — only gates rules with an explicit host; invalid CIDRs fail the render. |
| `haproxy.org/pod-maxconn` | Behaviour differs | Divided across the number of ready HAProxy pods (quantized to a power of two) rather than applied per-server verbatim; must be a positive integer. |
| `haproxy.org/request-redirect-code` | Behaviour differs | Default 302; an invalid code fails the render. |
| `haproxy.org/send-proxy-protocol` | Behaviour differs | proxy, proxy-v1, proxy-v2, proxy-v2-ssl and proxy-v2-ssl-cn map to the matching send-proxy flags; any other value is silently ignored. |
| `haproxy.org/server-ca` | Behaviour differs | Verifies the upstream certificate against the Secret's ca.crt; a missing Secret or missing ca.crt renders a warning comment and silently skips verification instead of failing. |
| `haproxy.org/server-crt` | Behaviour differs | Presents a client certificate to the upstream from the Secret; a missing Secret or missing tls.crt/tls.key renders a warning comment and skips the client cert instead of failing. |
| `haproxy.org/src-ip-header` | Behaviour differs | Rewrites the source IP from the named header (set-src), but only for rules with an explicit host. |
| `haproxy.org/standalone-backend` | Not carried over | Not implemented; HAPTIC always shares the backend model. |
| `haproxy.org/timeout-client` | Not carried over | A frontend-level timeout owned by HAPTIC's shared frontend, not settable per Ingress. |
| `haproxy.org/timeout-http-keep-alive` | Not carried over | A frontend-level timeout owned by HAPTIC's shared frontend, not settable per Ingress. |
| `haproxy.org/timeout-http-request` | Not carried over | A frontend-level timeout owned by HAPTIC's shared frontend, not settable per Ingress. |
| `haproxy.org/whitelist` | Behaviour differs | Deprecated alias of allow-list, honoured only when allow-list is absent; host-scoped. |
<!-- END generated: migration-coverage haproxytech -->

---

## Troubleshooting

Three defaults cause most "it's installed but doesn't behave like before"
reports. Each fails quietly, so check them first.

- **Existing Ingresses aren't being routed.** HAPTIC only serves Ingresses whose
  `spec.ingressClassName` equals `ingressClass.name` (default **`haptic`**), and
  the filter is applied *at the watch level* — an `ingressClassName: nginx`
  Ingress is never even seen. Fix: [match the IngressClass](#1-match-the-ingressclass).

- **Annotations seem to be ignored.** The `nginx.ingress.kubernetes.io/*`
  compatibility library is **disabled by default**, so those annotations
  (timeouts, auth, CORS, rate-limits, redirects) are silent no-ops until you turn
  it on. Fix: [enable the annotation library](#2-enable-the-annotation-library).
  (The `haproxy-ingress.github.io/*` and `haproxy.org/*` libraries are on by
  default.)

- **DNS cut over before you were ready.** Ingress status writes are **on by
  default**: as soon as HAPTIC's HAProxy Service has an address it stamps
  `.status.loadBalancer` onto every adopted Ingress, and `external-dns` can act
  on that to repoint DNS. Keep it off until you've verified routing. Fix:
  [control the DNS cutover](#3-control-the-dns-cutover).

See also [Troubleshooting](troubleshooting.md) for general "my Ingress isn't
being served" diagnostics.

## See also

- [Getting Started](getting-started.md) — install HAPTIC and route your first Ingress.
- [Watching Resources](watching-resources.md) — how `ingressClassName` scoping and field selectors work.
- [Troubleshooting](troubleshooting.md) — "my Ingress isn't being served" diagnostics.
