# Shared response caching

Use a shared [Varnish](https://varnish-cache.org/) cache to serve repeated GET and
HEAD requests without sending each one to the application. Other methods bypass
it. Cache contents are divided among Varnish pods by a hash of the cache key.

## Enable caching

The default cache adds two Varnish pods, reserving **200m CPU and 768 MiB of
memory** in total. Set `cache.varnish.resources` to adjust that budget.

Add this to your [complete Helm values file](../deploying-with-helm.md#change-settings):

```yaml
cache:
  varnish:
    enabled: true
```

Apply the values through your [Helm deployment](../deploying-with-helm.md), then
add annotations to an Ingress:

```yaml
metadata:
  annotations:
    haproxy-haptic.org/cache-enable: "true"
    haproxy-haptic.org/cache-ttl: "60"
```

This requests a 60-second cache lifetime for `200` responses. Responses with `Set-Cookie`,
`Cache-Control: no-cache`/`no-store`/`private`, or `Vary: *` aren't stored.
Use `cache-ttl: auto` to follow the origin's cache headers instead. See the
[cache annotations](../libraries/haptic-annotations.md#shared-response-cache)
for exclusions, object-size limits, and other lifetimes.

## Check cache behavior

Inspect the response's `X-Cache` header or the access log's `cache` field:

| Value | Meaning |
|-------|---------|
| `HIT` | Served a fresh cached response. |
| `MISS` | Fetched the response from the application. |
| `STALE` | Served an expired response within an allowed staleness window. |

The log's `cache_age` reports the object's age in seconds. If a response isn't
stored, `cache_uncacheable_reason` identifies why: excluded content type, excessive
size, `Set-Cookie`, status, or origin cache restrictions.

[Vector](https://vector.dev/) exposes cache counters through the existing metrics
port: `haptic_cache_status_total`, `haptic_cache_age_seconds_total`,
`haptic_cache_uncacheable_total`, and `haptic_degraded_cache_total`.
Use access logs when you need to identify a particular route.

## Cache keys and authentication

`cache-key` adds request-specific components to the cache key:

| Key | Effect |
|-----|--------|
| `consumer` | Separates responses by authenticated identity. Authentication and consumer-group checks still run on cache hits. |
| `src` | Separates responses by source IP. It doesn't establish an authenticated identity. |
| `header:<name>`, `cookie:<name>`, `query:<name>` | Adds the named request value. Combine components with commas. |

Only `consumer` allows caching requests that carry `Authorization` or `Cookie`.
With other keys, those requests bypass Varnish. Rate and bandwidth limits still
apply to cache hits; an internal cache-miss fetch doesn't consume a second budget.

HAPTIC also protects response variants from being combined by a proxy in front
of the cluster. Header and cookie keys add `Vary` values without replacing the
origin's existing values. Query parameters are already part of the URL.
`consumer` and `src` keys set `Cache-Control: private` because a downstream cache
can't reproduce those identities. These headers apply even when a request
bypasses Varnish or falls back to the application.

## Handle expiry and cache failures

Choose how the route handles an expired object:

| Annotation | Behavior |
|------------|----------|
| `cache-stale-while-revalidate` | Serve the stale object immediately while refreshing it in the background. The default window is 10 seconds. |
| `cache-stale-if-error` | Wait for the origin, then use a stale object only if the refresh fails. |
| `cache-revalidate` | Retain expired objects for conditional requests that can return `304 Not Modified`. |

The windows are independent. For example, `cache-stale-while-revalidate: "30"`
and `cache-stale-if-error: "600"` allow fast responses during refresh and a longer
fallback during an origin failure. Origin errors don't replace a good cached object.

HAProxy bypasses unhealthy Varnish pods. A failed or timed-out cache attempt
retries the application if response headers haven't reached the client. Once
response delivery starts, an idle partial response is terminated instead.
Set `cache.haproxy.responseTimeoutMs` above the application's normal time to first
byte to avoid duplicate fetches on cache misses.

Health checks cover Varnish's connection back to the origin. The default
NetworkPolicy allows requests only from the release's HAProxy pods and outbound
connections to DNS and those HAProxy pods' origin-fetch port. HAPTIC strips
client-supplied `X-Haptic-Cache-*` headers before routing.

## Size and restart the cache

Cached objects don't survive a Varnish process restart. Rolling updates replace
one pod at a time; other shards stay warm. More `cache.varnish.replicas` reduce
the share of keys affected by one pod's restart.

Budget `cache.varnish.resources.limits.memory` for `cache.varnish.malloc`, the
shared-log buffer (80 MiB with the default image), compiled configuration, and
process overhead. The working directory is an executable, memory-backed
`emptyDir`; its memory also counts toward the container limit.

### Prevent the shared log from being swapped

The working directory must remain resident in memory. Use a verified `noswap`
mount, container swap prohibition, or nodes without swap; `tmpfs` alone doesn't
prevent paging. See [Kubernetes swap behavior](https://kubernetes.io/docs/concepts/cluster-administration/swap-memory-management/#swap-behaviors).

If Varnish logs `mlock() of VSM failed`, [upstream permits it](https://www.varnish.org/docs/reference/vsm/#warning-mlock-of-vsm-failed)
only when the environment prevents paging. Otherwise, raise the memory-lock limit in the container
runtime or disable swapping for these containers. The chart
keeps the non-root image and adds no capabilities or host-limit changes.
Collect routine access logs from HAProxy; Varnish's shared log is a circular
diagnostic buffer.
