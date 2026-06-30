---
hide:
  - navigation
---

# Migrating to HAPTIC

How to move an existing cluster from **ingress-nginx** or **haproxy-ingress** to
HAPTIC with zero downtime — run both controllers side by side, cut over one
Ingress at a time, and flip DNS only when you're ready.

!!! danger "Three things that silently break a migration"
    Each of these fails *quietly* — routing looks installed but doesn't behave as
    before. Read these before anything else:

    1. **HAPTIC ignores your existing Ingresses by default.** It only serves
       Ingresses whose `spec.ingressClassName` equals `ingressClass.name`
       (default **`haptic`**). Your `ingressClassName: nginx` Ingresses are
       filtered out *at the watch level* and never routed. → [Match the class](#1-match-the-ingressclass)
    2. **ingress-nginx annotations are off by default.** The
       `nginx.ingress.kubernetes.io/*` compatibility library is **disabled**, so
       every such annotation (timeouts, auth, CORS, rate-limits, redirects) is a
       silent no-op until you enable it. → [Enable the library](#2-enable-the-annotation-library)
    3. **Ingress status writes are on by default.** HAPTIC writes
       `.status.loadBalancer` on every Ingress it adopts, which `external-dns`
       can act on — repointing DNS to HAPTIC *before you've verified it*. → [Control the cutover](#3-control-the-dns-cutover)

## Before you start

- HAPTIC is installed and its HAProxy pods are running (see [Getting Started](getting-started.md)).
- Your incumbent controller (ingress-nginx / haproxy-ingress) is still running and serving traffic. **Leave it running** until cutover is complete.
- You can edit Ingress manifests (to change `ingressClassName`) or you accept renaming HAPTIC's class to match — see below.

HAPTIC is designed to coexist: it ships a distinct IngressClass (`haptic`,
**not** marked cluster-default) and its own HAProxy Service, so it adopts
*only* the Ingresses you explicitly point at it.

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

!!! warning "Not carried over (silently dropped)"
    `server-snippet`, `stream-snippet`, `auth-snippet`,
    `proxy-max-temp-file-size`, `auth-tls-verify-depth`, and the
    OpenTelemetry/OpenTracing families. `backend-protocol: AJP|FCGI` does **not**
    drop silently — it fails the config with an error.

!!! warning "Behaviour changes to check"
    - `proxy-read-timeout` and `proxy-send-timeout` collapse into one HAProxy `timeout server` (the larger wins) — asymmetric timeouts are lost.
    - `limit-rps` and `limit-connections` can't coexist; if both are set, `limit-connections` is dropped.
    - External-auth (`auth-url`) and mTLS (`auth-tls-*`) annotations **fail the render** on a rule with no `host:` — add a host to any wildcard/default-backend Ingress that uses them.
    - `mirror-target` needs `spoaHub.plugins.mirror` enabled and a rule `host:` (both fail the render otherwise, rather than silently no-op); `mirror-host` and `mirror-request-body: off` are not honoured.
    - Basic-auth Secret format is unchanged from ingress-nginx (htpasswd in a single `auth` key) — but it differs from the haproxy-ingress library's format, so don't mix.

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

!!! warning "Accepted but silently does nothing"
    These annotations are read without error but emit **no** HAProxy config —
    behaviour they controlled is lost on migration:

    | Dropped annotation | Use instead |
    |--------------------|-------------|
    | `maxconn-server` | `haproxy.org/pod-maxconn` (haproxytech library) |
    | `backend-check-interval`, `health-check-fall-count`, `health-check-rise-count`, `health-check-port` | `haproxy.org/check-*` (haproxytech library) |
    | `initial-weight` | `weight N` in a `haproxy.org/backend-config-snippet` |
    | `maxqueue-server`, `auth-tls-strict` | (no equivalent — `auth-tls-strict` → `auth-tls-verify-client: optional`) |

!!! warning "Behaviour changes to check"
    - External-auth (`auth-url`) and client-mTLS (`auth-tls-secret`) annotations **fail the render** on a rule with no `host:`.
    - Basic-auth Secret format differs from htpasswd: the value is `base64(bcrypt-hash)` only, keyed by username — a migrated htpasswd Secret will not authenticate.
    - `auth-method: POST|PUT|PATCH` forwards an **empty** body to the auth service.

---

## See also

- [Getting Started](getting-started.md) — install HAPTIC and route your first Ingress.
- [Watching Resources](watching-resources.md) — how `ingressClassName` scoping and field selectors work.
- [Troubleshooting](troubleshooting.md) — "my Ingress isn't being served" diagnostics.
