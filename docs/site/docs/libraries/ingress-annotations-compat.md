# ingress-annotations-compat Library

This library is a set of shared internal macros consumed by the three vendor annotation libraries ([haproxytech](haproxytech.md), [haproxy-ingress](haproxy-ingress.md), [nginx-ingress](nginx-ingress.md)) — it emits nothing on its own, and there is nothing in it to configure. Leave it enabled: the vendor libraries `import` its macros, so disabling it while any of them is enabled breaks their template compilation.

## Overview

`ingress-annotations-compat.yaml` is a scaffold library at hierarchy level 2.5 that holds parameterized macros consumed by the three **Ingress** vendor annotation libraries. The scaffold either walks `resources.ingresses.List()` itself (e.g. `BuildAnnotationSSLPassthrough`) or takes a typed `*resources.ingresses.T` parameter (e.g. `EmitAnnotationAccessControl`) — that's correct, because all three vendor libraries process the same resource and differ only by annotation namespace.

The scaffold exists to concentrate behaviour that would otherwise be duplicated three times. Each vendor library still owns its annotation extraction (the keys differ per vendor: `haproxy.org/*`, `haproxy-ingress.github.io/*`, `nginx.ingress.kubernetes.io/*`); the shared output emission lives in this library.

**Scope: Ingress only.** A vendor library operating on a non-Ingress CRD (HTTPRoute, GRPCRoute, custom CRDs) does NOT use these macros — it writes its own equivalents.

## Configuration

```yaml
controller:
  templateLibraries:
    ingressAnnotationsCompat:
      enabled: true  # Default; required by haproxytech, haproxy-ingress, and nginx-ingress
```

Disabling the scaffold while any of the three vendor annotation libraries stays enabled produces an invalid configuration: their snippets `import` macros defined here, and the controller's template validation rejects the merged config when those imports don't resolve. Disable it only together with all three vendor libraries.

## Hierarchy

```
Level 0:   base
Level 1:   ssl
Level 2:   ingress, gateway
Level 2.5: ingress-annotations-compat   <-- this library
Level 3:   haproxytech, haproxy-ingress, nginx-ingress
```

The vendor libraries import macros from this scaffold; the scaffold knows about Ingress but not about any specific vendor.

## Available Macros

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

**Backend entry shape (do not change without updating ssl.yaml consumers):**

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

The service fields (`svcName`, `svcPort`, `svcPortName`) are captured at scan time rather than re-fetched by the consumer's `backends-*-ssl-passthrough` snippet — a second `GetSingle()` lookup could disagree with the use_backend pass under concurrent Ingress deletion. The macro comment in the source has the full rationale.

**Used by:**

- `haproxytech/` → `util-haproxytech-ssl-passthrough` (annotation: `haproxy.org/ssl-passthrough`)
- `haproxy-ingress/` → `util-haproxy-ingress-ssl-passthrough` (annotation: `haproxy-ingress.github.io/ssl-passthrough`)
- `nginx-ingress/` → `util-nginx-ingress-ssl-passthrough` (annotation: `nginx.ingress.kubernetes.io/ssl-passthrough`)

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

**Used by:**

- `haproxytech/` → `frontend-filters-200-haproxytech-access-control`
- `haproxy-ingress/` → `frontend-filters-610-haproxy-ingress-access-control`
- `nginx-ingress/` → `frontend-filters-700-nginx-ingress-access-control`

Validation tests assert on ACL names (`ni_allowlist_*`, `hi_allowlist_*`, `haproxytech_allowlist_*`), so each library passes its distinct `aclPrefix`.

## Design rationale

Why the scaffold sits at level 2.5, which patterns were extracted, which were surveyed and deliberately left duplicated (cookie-based session affinity, backend timeouts, header manipulation), and the rename from `annotation-compat` to `ingress-annotations-compat`: see [ADR-0003](../development/adr/0003-annotation-compat-scaffold-level-2-5.md).

## Adding a New Macro

A new macro earns its keep when:

1. Two or more vendor libraries already implement nearly the same emission logic.
2. The differences can be expressed as a small fixed set of string parameters (annotation keys, naming prefixes, comment formats).
3. Validation-test assertions can survive the move (or can be updated cheaply).

If any of those fail, leave the duplication in place. Forced abstractions over genuinely different behaviour are worse than direct duplication — the future reader has to follow the parameters back to figure out what each library actually does.

## See Also

- [Template Libraries Overview](../template-libraries.md)
- [Base Library](base.md) — provides `util-ingress-helpers` (HostMatchCondition) used inside scaffold macros
- [ADR-0003](../development/adr/0003-annotation-compat-scaffold-level-2-5.md) — the decision record for this library's placement and scope
