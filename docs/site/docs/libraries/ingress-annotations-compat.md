# `ingress-annotations-compat` library

This library provides shared macros for [haptic-annotations](haptic-annotations.md), [haproxytech](haproxytech.md), [haproxy-ingress](haproxy-ingress.md), and [nginx-ingress](nginx-ingress.md). It emits no configuration on its own. Keep it enabled while you use any of those annotation libraries.

<a id="overview"></a>

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

<a id="hierarchy"></a>

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

**Backend entry fields:**

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

`PublishAccessControl` takes the same arguments as `EmitAnnotationAccessControl`
and publishes access rules in the `ingress-access-control` incremental group.
IPv4-only lists use maps. Lists containing IPv6 return a configuration fragment;
retain it under `fragments:<family>`. Render `AccessControlLane("<family>")` in
the frontend to apply both forms. Use the same family name for publishing and
rendering.

### `IngressCORSPublish`, `PublishCORS` and `CorsLane`

`IngressCORSPublish(ingress, prefix, enabledAnn, defaultMaxAge, family)` reads
nginx-style CORS annotations and publishes them in the `ingress-cors` group.
It accepts comma-separated origins with single-level `*.` wildcards. Use
`PublishCORS` directly for another annotation model. Render `CorsLane("<family>")`
in the frontend to apply origin checks, preflight responses, and CORS headers.

### `WebhookRejectOrWarn`

`WebhookRejectOrWarn(resource, reason, message)` rejects a proposed resource
during admission. For an existing resource, it records a Warning Event and
returns so other resources can still render. Import it from
`util-webhook-reject-or-warn`.

After a warning, skip the offending output with `{% continue %}`. Use this
helper only when omitting that feature is safe. For security checks or a
condition that prevents safe rendering, use `fail()` instead.

### Other exported macros

The scaffold exports these additional macros, all imported the same way:

| Macro | Signature | What it does |
|-------|-----------|--------------|
| `RenderAnnotationSSLPassthroughBackends` | `(cacheKey, firstSeenKey, commentLabel string)` | Emits the `mode tcp` backends for the entries a matching `BuildAnnotationSSLPassthrough` scan cached under `cacheKey` — the emission half of the passthrough pair |
| `RegisterAnnotationHSTS` | `(ingress *resources.ingresses.T, enabledAnn, maxAgeAnn, subdomainsAnn, preloadAnn, defaultMaxAge string)` | Registers per-host HSTS settings for the SSL library's global HSTS snippet to emit |
| `ValidateCidrList` | `(rawList, annotation, key string)` | Rejects a malformed CIDR list before it reaches the config, naming the annotation and the resource |
| `ValidateConfigValue` | `(value, annotation, key string, allowSpaces bool)` | Rejects annotation values carrying characters that would break out of the rendered directive |
| `WafGovernance` | `(gov map[string]any)` | Applies the shared Web Application Firewall governance rules to an annotation-derived policy selection |

<a id="design-rationale"></a>
<a id="adding-a-new-macro"></a>

## See also

- [Template Libraries Overview](../template-libraries.md)
- [Base Library](base.md) — provides `util-ingress-helpers` (HostMatchCondition) used inside scaffold macros
