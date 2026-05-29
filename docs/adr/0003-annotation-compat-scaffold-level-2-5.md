# ADR-0003: Annotation-compat scaffold lives at library hierarchy level 2.5

## Status

Accepted.

## Context

Three Helm chart libraries (`libraries/haproxytech.yaml`, `libraries/haproxy-ingress.yaml`, `libraries/nginx-ingress.yaml`) provide compatibility for vendor-specific Ingress annotations. They are documented as peers at level 3 of the chart's library knowledge hierarchy:

```
Level 0: base
Level 1: ssl
Level 2: ingress, gateway
Level 3: haproxytech, haproxy-ingress, nginx-ingress
```

In practice these three independently re-implement the same HAProxy output shapes — cookie-based session affinity, SSL-passthrough scanning (`util-haproxytech-ssl-passthrough` and `util-haproxy-ingress-ssl-passthrough` are near-identical: cached scan, loop, append to `gf["sslPassthroughBackends"]`), backend timeout directives, ACL/header manipulation. Total: ~9k lines, of which a non-trivial fraction is the same pattern emitted thrice with different annotation names.

The deletion test held: removing one of the three protocol libraries did not collapse complexity — the same patterns reappeared, intact, in the others.

## Decision

Introduce a new library `libraries/annotation-compat.yaml` at **level 2.5** of the hierarchy — loaded after `ingress.yaml` and `gateway.yaml`, before the three protocol libraries. It exposes parameterized macros that the protocol libraries call into. Patterns are extracted only when the call sites genuinely share behaviour; patterns that diverge across libraries stay in place.

**Extracted on first pass:**

- `BuildAnnotationSSLPassthrough(sslData, annotationKey, namePrefix, firstSeenKey)` — replaces three near-identical scanners that loop over Ingresses, check a vendor-specific `ssl-passthrough` annotation, and append host entries to `gf["sslPassthroughBackends"]`. Each library keeps its own `util-X-ssl-passthrough` snippet (owns the `ComputeIfAbsent` cache key), but the build logic is shared.
- `EmitAnnotationAccessControl(ingress, allowAnnotation, denyAnnotation, aclPrefix, allowCommentFmt, denyCommentFmt)` — replaces three near-identical CIDR allow/deny ACL emitters that read source-range annotations and emit `acl ... src` + `http-request deny` rules.

**Surveyed and not extracted:**

- **Cookie-based session affinity** — diverges substantially: `haproxytech` triggers on `cookie-persistence: <name>` (annotation value IS the cookie name), while `haproxy-ingress` and `nginx-ingress` use a `affinity: cookie` gate plus a `session-cookie-name` annotation. `haproxy-ingress` carries seven separate session-cookie-* parameters; `nginx-ingress` adds an extra `dynamic-cookie-key` line. A shared macro would have to absorb three different control-flow shapes and would obscure rather than concentrate the behaviour.
- **Backend timeouts** — each library has its own annotation naming (`haproxy.org/timeout-server` vs `haproxy-ingress.github.io/timeout-server` vs `nginx.ingress.kubernetes.io/proxy-read-timeout`). `nginx-ingress` maps `proxy-read-timeout` and `proxy-send-timeout` both onto HAProxy's `timeout server` using `max()`. The transforms are too vendor-specific to share.
- **Request/response header manipulation** — the libraries emit headers at different positions (backend-directives in `haproxytech`, frontend-filters in `nginx-ingress`), use different annotation value separators (`\n` vs `|`), different name/value separators (space vs colon), and apply host-match conditions inconsistently.

The protocol libraries become *annotation parsers* for the patterns where the scaffold applies. For the rest, they keep their own implementations.

## Consequences

- The library hierarchy gains a new tier (2.5) between resource libraries and vendor-specific annotation libraries. The diagram in `charts/CLAUDE.md` is updated accordingly.
- For the SSL passthrough and CIDR ACL patterns, the protocol libraries shrink and become focused on annotation mapping rather than HAProxy code generation.
- The scaffold does not process resources directly. It contains only macros that take parameters and emit HAProxy directives. Resource scanning that is not annotation-driven (e.g., `util-gateway-ssl-passthrough`, which reads Gateway listener spec) stays in its existing library — it is not the same pattern.
- A new annotation namespace (e.g., a future Traefik-compat library) can use the SSL-passthrough and CIDR-ACL macros without reimplementing them. For cookie affinity, timeouts, and headers it must carry its own implementation.
- The "honest extraction" principle is part of the rationale: a shared abstraction that hides genuinely different behaviours is worse than direct duplication. Future contributions to the scaffold should pass the same test — if a fourth pattern emerges and three libraries' shapes converge enough to share it, add the macro; otherwise leave the duplication.

## Do not re-suggest

A future reviewer may ask "why does this scaffold not live in `base.yaml` — that would put all the shared HAProxy output shapes in one place." **Do not.** `base.yaml` is required to be completely resource-agnostic (it must not access `ingress.metadata`, `route.spec`, etc. — see the "CRITICAL ARCHITECTURE RULE" in `charts/CLAUDE.md`). The scaffold's macros are themselves resource-agnostic, but its utility snippet takes a resource list as parameter, and the convention is that snippets which loop over resources sit above level 0. Level 2.5 is the right tier: above the resource libraries that supply the loops, below the annotation libraries that consume the macros.

A reviewer may also ask "why not push these macros into `ingress.yaml` since most annotation libraries layer on top of Ingress?" Because `gateway.yaml` may grow to use the same macros for HTTPRoute filters, and the scaffold would have to be promoted then anyway.

## Update: rename to `ingress-annotations-compat` (post-acceptance)

The original framing claimed the scaffold "does not process resources directly" and contained only "stateless macros". That was aspirational, not factual: `BuildAnnotationSSLPassthrough` walks `resources.ingresses.List()` itself, and every other macro takes a typed `*resources.ingresses.T` parameter. The library was always Ingress-coupled; the generic framing made the typed access feel like a violation that needed working around (e.g. parameter regeneralization to `any`) instead of an honest scope statement.

The library was renamed `annotation-compat.yaml` → `ingress-annotations-compat.yaml` and the values key `controller.templateLibraries.annotationCompat.enabled` → `ingressAnnotationsCompat.enabled` to make the scope explicit. The "may grow to use these macros for HTTPRoute filters" speculation in the section above remained speculation — `gateway.yaml` never imported them, and Gateway API filters diverge enough from Ingress annotation shapes that sharing macros would have obscured rather than clarified the behaviour. A vendor library operating on a non-Ingress CRD writes its own equivalents at the same hierarchy level.

The level-2.5 placement, the extraction criteria (genuine shape overlap, parameterizable differences), and the rejection of base.yaml as a host all remain correct under the renamed name. This is a naming change to match observed scope; no logic changed.
