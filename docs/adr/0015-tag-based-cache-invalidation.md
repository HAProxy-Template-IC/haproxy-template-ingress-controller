# ADR-0015: Tag-based cache invalidation

## Status

Proposed. No code. Records what was verified against the deployed image, and the
one property any implementation has to satisfy before it is worth building.

## Context

The shared cache tier has **no invalidation path at all**. Content leaves the
cache exactly one way: its TTL expires. An operator who ships a wrong page, or
whose data changes out of band, waits the TTL out. `cache-ttl` is therefore doing
double duty — it is both a performance knob and the blast radius of a mistake,
which is why routes tend to be configured with lifetimes far shorter than their
content actually warrants.

Everything needed to fix that already ships in `varnish:9.0`. What is missing is
not the mechanism; it is a safe way to reach every shard.

### The mechanism works

`xkey` is present in the image and functions on 9.0. Verified with `varnishtest`
against the stock image: an origin emitting `xkey: product-123 catalog` on two
different URLs, then one `xkey.purge("catalog")` call, reported `2` objects
evicted and both URLs were re-fetched on the next request.

Tags come from the **origin application**, as `xkey:` response headers. This is
worth stating plainly, because it decides who the feature is for: an annotation
can switch xkey handling on for a route, but it cannot invent tags. A team that
cannot change its application's response headers gets nothing from this.

### The constraint that shapes everything

The tier is sharded. `backend varnish_cache` uses `balance uri` with
`hash-type consistent`, so any single request reaches **exactly one pod**. A tag,
by contrast, spans pods: `product-123` may be cached on any shard that happened
to receive one of its URLs.

So a tag purge is not a request. It is a **fan-out to every shard**, and it is
only correct if every shard is reached.

Access is currently closed by design. The Varnish NetworkPolicy admits only this
release's HAProxy pods, on port 6081. Nothing else can reach the tier, so a purge
caller does not exist today and cannot be added without opening that policy.

## The hard part is partial purge, not purging

A purge that reaches two of three shards is **worse than no purge**. The operator
believes the content is gone; one shard keeps serving it until its TTL; and
because the tier is consistent-hashed, whether a given user sees the stale copy
depends on which URL they request. It fails silently and unreproducibly — the
shape of bug that costs a day to find.

This is the property any design must satisfy:

> A purge either provably reached every shard, or it reports failure loudly
> enough that the operator does not believe it succeeded.

That rules out fire-and-forget designs, and it is the reason this is an ADR
rather than a small MR. Shard-set membership changes under autoscaling
(`cache.varnish.autoscaling`), so "every shard" is a moving target that has to be
resolved at purge time, not configured.

## Options

| | Approach | Reaches all shards | Reports partial failure | Cost |
|---|---|---|---|---|
| A | Caller fans out over the existing headless Service | yes, if DNS is complete at that moment | caller's job — a shipped tool can exit non-zero | small; one NetworkPolicy exception |
| B | Purge through HAProxy | no — HAProxy routes one request to one backend; would need N per-pod backends and N calls, so the caller fans out anyway | same as A | same as A plus config churn per replica |
| C | Controller-mediated purge API | yes — the controller can resolve the pod set and own the result | authoritative; can fail the whole operation | large: HTTP surface, authz, Go-side coupling to a chart feature |
| D | Varnish cluster-wide invalidation | n/a | n/a | not available; OSS Varnish has no clustering |

B is A wearing a costume: HAProxy cannot broadcast one request to many backends,
so the fan-out lands on the caller either way, with extra generated config as the
price. D is out.

## Recommendation

**Option A**, with the fan-out done by a small tool the chart ships, not by
documentation telling operators to write their own loop.

The headless Service (`clusterIP: None`) already exists for the sharded backend,
so pod IPs are resolvable without new infrastructure. The tool resolves it,
issues one purge per address, and reports per-shard results — exiting non-zero if
any shard was unreachable or returned anything other than success. Making the
partial case an error is the whole point; a tool that prints "purged" after two
of three succeeded reintroduces exactly the failure this ADR exists to prevent.

Open questions that need answering before implementation, not during:

- **Authentication.** Purge is destructive and the NetworkPolicy exception is a
  hole in an otherwise closed tier. HAPTIC already has consumer authentication
  (API key, JWT); reusing it is the obvious path, but the purge listener is
  reached directly rather than through the client-facing frontend, so it does not
  currently sit behind that machinery.
- **Scope of the exception.** Which callers get to reach 6081, expressed as a
  podSelector or namespace selector, and whether purge should be a separate port
  so cache reads and invalidation can be authorised independently.
- **Autoscaling races.** A pod that starts *during* a purge has a cold cache and
  is trivially correct; a pod that starts just *after* one, from a warm
  neighbour, is not an issue either since Varnish shares nothing between pods. The
  real race is a purge that resolves DNS before a scale-up completes, which the
  tool cannot detect. Whether that is acceptable, or wants a re-resolve-and-retry
  pass, is a decision.

Recommended only if a team actually wants it: the feature needs application
changes (emitting `xkey:` headers) to deliver anything, so it should follow a
concrete request rather than being built speculatively.

## Consequences

Nothing changes until this is accepted. `cache-ttl` and `cache-negative-ttl`
remain the only levers, and short TTLs remain the workaround for the absence of
invalidation — which is the cost being paid today, in origin load, for not having
this.

If accepted as written, the tier gains one NetworkPolicy exception and one shipped
tool, and the chart gains an annotation to enable `xkey` handling per route. The
controller is not involved, which keeps a chart feature out of the Go side.
