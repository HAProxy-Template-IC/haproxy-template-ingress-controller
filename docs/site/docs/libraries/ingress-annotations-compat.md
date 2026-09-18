# `ingress-annotations-compat` library

This library provides shared macros for [haptic-annotations](haptic-annotations.md), [haproxytech](haproxytech.md), [haproxy-ingress](haproxy-ingress.md), and [nginx-ingress](nginx-ingress.md). It emits no configuration on its own. Keep it enabled while you use any of those annotation libraries.

## Overview

The shared helpers read Ingress resources and generate configuration for the
annotation libraries. Each caller supplies its annotation prefix and feature
settings; the helpers provide the common output behavior.

Use these macros when extending an Ingress annotation library. Resource-specific
libraries for other kinds, such as HTTPRoute or a custom resource, define their
own helpers.

## Configuration

```yaml
controller:
  templateLibraries:
    ingressAnnotationsCompat:
      enabled: true  # Default; required by haptic-annotations (on by default) and by haproxytech, haproxy-ingress, and nginx-ingress
```

Helm rejects disabling this library while any annotation library remains enabled, because their snippets import its macros. To disable it, also set `hapticAnnotations.enabled`, `haproxytech.enabled`, `haproxyIngress.enabled`, and `nginxIngress.enabled` to `false` under `controller.templateLibraries`.

## Hierarchy

```
Level 0:   base
Level 1:   ssl
Level 2:   ingress, gateway
Level 2.5: ingress-annotations-compat   <-- this library
Level 3:   haptic-annotations, haproxytech, haproxy-ingress, nginx-ingress
```

The level-3 libraries import macros from this scaffold; the scaffold knows about Ingress but not about any specific annotation vocabulary.

## Available macros

### `BuildAnnotationSSLPassthrough`

Loops over Ingress resources and registers SSL passthrough backends. Each Ingress with a truthy SSL-passthrough annotation contributes one backend per host rule.

**Signature:**

```scriggo
BuildAnnotationSSLPassthrough(
  sslData       map[string]any, // accumulator
  annotationKey string,         // e.g. "haproxy.org/ssl-passthrough"
  namePrefix    string,         // e.g. "ssl-passthrough-"
  firstSeenKey  string,         // e.g. "haproxytech_sslPassthrough_host"
)
```

**Backend entry shape (don't change without updating ssl.yaml consumers):**

```scriggo
{
  "name":        namePrefix + ns + "-" + name,
  "sni":         host,
  "namespace":   ns,
  "ingress":     name,
  "svcName":     <first path's service name>,
  "svcPort":     <first path's service port (int, resolved from port.number or port.name)>,
  "svcPortName": <first path's service port name ("" if the port is referenced by number)>,
}
```

The service fields (`svcName`, `svcPort`, `svcPortName`) are captured at scan time rather than re-fetched by the consumer's `backends-*-ssl-passthrough` snippet — a second `GetSingle()` lookup could disagree with the `use_backend` pass under concurrent Ingress deletion. The macro comment in the source has the full rationale.

**Used by:**

- `haproxytech/` → `util-haproxytech-ssl-passthrough` (annotation: `haproxy.org/ssl-passthrough`)
- `haproxy-ingress/` → `util-haproxy-ingress-ssl-passthrough` (annotation: `haproxy-ingress.github.io/ssl-passthrough`)
- `nginx-ingress/` → `util-nginx-ingress-ssl-passthrough` (annotation: `nginx.ingress.kubernetes.io/ssl-passthrough`)
- `haptic-annotations/` → `util-haptic-ssl-passthrough` (annotation: `haproxy-haptic.org/ssl-passthrough`)

The vendor library still owns the per-library `ComputeIfAbsent` cache key, so the data slots stay distinct.

### `EmitAnnotationAccessControl`

Emits CIDR-based source-range access control: `acl <name> src <cidrs>` plus `http-request deny if <host-match> [!]<acl>`, scoped to the ingress's hosts.

**Signature:**

```scriggo
EmitAnnotationAccessControl(
  ingress         *resources.ingresses.T,
  allowAnnotation string,  // e.g. "nginx.ingress.kubernetes.io/whitelist-source-range"
  denyAnnotation  string,  // e.g. "nginx.ingress.kubernetes.io/denylist-source-range"
  aclPrefix       string,  // e.g. "ni" — produces ACL names like "ni_allowlist_<ns>_<name>"
  allowCommentFmt string,  // comment template, may contain {KEY} for "<ns>/<name>"
  denyCommentFmt  string,
)
```

**Used by:** `PublishAccessControl` (below), for a list with an IPv6 entry; every other list rides the map lane.

### `PublishAccessControl` and `AccessControlLane`

The map lane for source-IP allow and deny lists. Each library's publisher (`ingress-access-control-<band>-<library>`, in incremental group `ingress-access-control`) calls `PublishAccessControl` with the same arguments as `EmitAnnotationAccessControl`; an IPv4-only list becomes rows for four shared maps (`ing-ac-routes.map`, `ing-ac-partitions.map` from `cidr_partition`, `ing-ac-allow.map`, `ing-ac-deny.map`), and a list with an IPv6 entry comes back as the legacy fragment for the publisher to keep under `fragments:<family>`. Each library's frontend block, at its own band, renders `AccessControlLane("<family>")`: one exact lookup by route id, one `map_ip` lookup of the client address, and one deny rule each for the allow and the deny mode, followed by the family's legacy fragments. `family` doubles as the legacy ACL prefix (`ni`, `hi`, `haproxytech`, `haptic`).

### `IngressCORSPublish`, `PublishCORS` and `CorsLane`

The map lane for CORS. `IngressCORSPublish(ingress, prefix, enabledAnn, defaultMaxAge, family)` reads the ingress-nginx model (a comma-separated origin allow-list with single-level `*.` wildcards) and calls `PublishCORS`, which publishes one route's rows into the shared incremental group `ingress-cors`: `ing-cors-routes.map` (`<family>:<any|list>` plus the credentials, Vary, regex and preflight flags), `ing-cors-origins.map` (exact origins keyed by route), `ing-cors-regex.map` (wildcard origins as regex rows keyed by route), and the URL-encoded `ing-cors-methods.map`, `ing-cors-headers.map`, `ing-cors-maxage.map` and `ing-cors-expose.map`. haproxytech's publisher reads its own model (one origin regex, opt-in preflight) and calls `PublishCORS` directly. Each library's frontend block, at its own band, renders `CorsLane("<family>")`: the origin check, the preflight answer and the `Access-Control-*` headers on `http-after-response`, all from the maps.

### `WebhookRejectOrWarn`

`WebhookRejectOrWarn(resource, reason, message)` (from the `util-webhook-reject-or-warn` snippet) is the shared way to reject a misconfigured watched resource. It branches on the `renderMode` global: under the admission webhook it `fail()`s (so the API server denies the proposed resource), and on a live reconcile or the daemon load gate it records a `Warning` Event against `resource` and returns, so one already-present bad resource can't abort the whole render. The vendor libraries **and** the native [`haptic-annotations`](haptic-annotations.md) library import it for per-resource routing/presentation validation.

Callers must skip the offending resource's output in the warn path (`{% continue %}` in the Ingress loop). Use it only where skipping the feature is safe to serve without — routing/presentation guards — and keep a plain `fail()` for security features (skipping those would be fail-open) and for guards where a clean skip isn't possible. The full decision rule lives in the chart development guide (`charts/CLAUDE.md`).

### Other exported macros

The scaffold exports six more macros, all imported the same way:

| Macro | Signature | What it does |
|-------|-----------|--------------|
| `RenderAnnotationSSLPassthroughBackends` | `(cacheKey, firstSeenKey, commentLabel string)` | Emits the `mode tcp` backends for the entries a matching `BuildAnnotationSSLPassthrough` scan cached under `cacheKey` — the emission half of the passthrough pair |
| `RegisterAnnotationHSTS` | `(ingress *resources.ingresses.T, enabledAnn, maxAgeAnn, subdomainsAnn, preloadAnn, defaultMaxAge string)` | Registers per-host HSTS settings for the SSL library's global HSTS snippet to emit |
| `ValidateCidrList` | `(rawList, annotation, key string)` | Rejects a malformed CIDR list before it reaches the config, naming the annotation and the resource |
| `ValidateConfigValue` | `(value, annotation, key string, allowSpaces bool)` | Rejects annotation values carrying characters that would break out of the rendered directive |
| `WafGovernance` | `(gov map[string]any)` | Applies the shared Web Application Firewall governance rules to an annotation-derived policy selection |

## Design rationale

Why the scaffold sits at level 2.5, which patterns were extracted, which were surveyed and deliberately left duplicated (cookie-based session affinity, backend timeouts, header manipulation), and the rename from `annotation-compat` to `ingress-annotations-compat`: see Architecture Decision Record (ADR) [ADR-0003](../development/adr/0003-annotation-compat-scaffold-level-2-5.md).

## Adding a new macro

A new macro earns its keep when:

1. Two or more vendor libraries already implement nearly the same emission logic.
2. The differences can be expressed as a small fixed set of string parameters (annotation keys, naming prefixes, comment formats).
3. Validation-test assertions can survive the move (or can be updated cheaply).

If any of those fail, leave the duplication in place. Forced abstractions over genuinely different behaviour are worse than direct duplication — the future reader has to follow the parameters back to figure out what each library actually does.

## See also

- [Template Libraries Overview](../template-libraries.md)
- [Base Library](base.md) — provides `util-ingress-helpers` (HostMatchCondition) used inside scaffold macros
- [ADR-0003](../development/adr/0003-annotation-compat-scaffold-level-2-5.md) — the decision record for this library's placement and scope
