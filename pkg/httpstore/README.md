# pkg/httpstore

Pure HTTP resource store with render-local initial admission and two-version (pending/accepted) refresh caching.

## Overview

`HTTPStore` fetches arbitrary HTTP content (IP blocklists, JSON config, anything templates need from outside the cluster). Periodic refreshes use a two-version pattern: changed content lands in **pending** until the controller validates it, then it's promoted to **accepted**. New authoritative sources use render-local candidates instead. The controller accepts the complete candidate set only after the exact rendered output passes every validator, so an upstream serving garbage can't break the live HAProxy config.

This is the **pure** half of the design. Periodic refresh timers, validation event handling, eviction, and the template-callable wrapper all live in `pkg/controller/httpstore`. Reach for the pure store directly only from tests; in production the event adapter is what you want.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"

store := httpstore.New(logger, 2*time.Minute) // maxAge for eviction; 0 disables

opts := httpstore.FetchOptions{
    Timeout:  30 * time.Second,
    Retries:  3,
    Critical: true,
    Delay:    5 * time.Minute,
}
source, err := store.ReconcileSource("https://example.com/blocklist.txt", opts,
    &httpstore.AuthConfig{Type: "bearer", Token: "secret"})

// Fetch without exposing the response through accepted or pending state.
content, candidate, err := store.PrepareInitial(ctx,
    "https://example.com/blocklist.txt", source.State)

// The caller validates the complete rendered output, then commits every
// candidate from that render in one operation.
err = store.CommitInitialCandidates(ctx, []*httpstore.InitialCandidate{candidate})

// Later: refresh into pending (returns true if content changed)
changed, err := store.RefreshURL(ctx, "https://example.com/blocklist.txt")

// Production render reads accepted only
got, ok := store.Get(url)

// Validation render reads pending if available, otherwise accepted
got, ok = store.GetForValidation(url)

// After periodic-refresh validation returns:
store.PromotePending(url) // success → pending becomes accepted
store.RejectPending(url)  // failure → pending discarded, accepted preserved

// Test fixtures bypass HTTP entirely
store.LoadFixture(url, "mock content")
```

`Fetch` remains a convenience for isolated stores that may accept their first successful response immediately. The controller uses it only for render-local read-only stores; authoritative live renders use `PrepareInitial` and `CommitInitialCandidates` through `pkg/controller/httpstore`.

## Authentication

Three modes via `AuthConfig.Type`:

- `"basic"` — `Username` + `Password`
- `"bearer"` — `Token`
- `"header"` — `Headers` map (e.g. `X-API-Key: ...`)

## Conditional Requests

Refreshes use ETag and Last-Modified automatically; a 304 returns `changed=false` so the existing accepted/pending state is preserved. Repeated refreshes against an unchanged upstream cost one round-trip and zero cache churn.

## Source authority

The URL, effective fetch options, and authentication form one source declaration. An authoritative declaration change invalidates the accepted body and advances the source generation. The generation fences refreshes and render-local candidates that started under the previous declaration. The controller returns the candidate response to its render but accepts it only when the exact full pipeline succeeds; one stale candidate rejects the complete multi-URL commit. Admission and source-map renders fetch a different declaration into a render-local store instead of calling this shared-store transition.

## Size Limit

Responses are capped at `MaxContentSize` (10 MiB, declared in `types.go` and enforced in `fetcher.go` via `io.LimitReader`). Larger payloads fail with an explicit `response body exceeds maximum size of N bytes` error rather than being silently truncated, and the limited reader means the store never buffers more than ~10 MiB even for a runaway upstream. There is no per-call override; if you legitimately need larger blobs, change the constant.

## Trust Model

Content fetched here becomes HAProxy configuration and Coraza WAF rules, so it is a supply-chain path into the data plane. What the store does and does not guarantee:

**TLS.** The client uses `http.DefaultTransport`, so server certificates are verified against the system roots. There is no configuration, values key, or environment variable that disables verification and no custom-CA hook — `InsecureSkipVerify` appears nowhere outside test helpers. To trust a private CA, mount it into the container's system trust store.

**Redirects.** Up to 10 hops are followed. A redirect from an `https://` source to a plaintext target is refused: the fetched bytes become HAProxy config, so anyone on a plaintext hop could rewrite them. A source configured as `http://` was never confidential, so it may redirect within `http://`. On a host change every header except `User-Agent`, `Referer`, `If-None-Match`, `If-Modified-Since` and `Accept-Encoding` is dropped — `net/http` strips only `Authorization`, which would otherwise leak `AuthTypeHeader` API keys to the redirect target.

**Size.** Bodies are capped per entry at `MaxContentSize` (10 MiB); the cap is not aggregate, so worst-case memory scales with the number of cached URLs. Archives expanded by the template-side `untar_gz` are bounded separately (4096 entries, 8 MiB per entry, 32 MiB total).

**Integrity.** The SHA-256 in each cache entry is change *detection* against the previously accepted body — it drives the pending/accepted validation cycle. It is **not** a pin: there is no way to declare an expected checksum, so an upstream that is silently replaced with content that still renders to valid HAProxy config is validated and adopted. Operators who need stronger guarantees should serve fetched content from infrastructure they control rather than a third-party URL.

**Residual risks.** A compromised upstream chooses the final host, so a redirect can point the controller at an in-cluster address it would not otherwise reach; the response only becomes config if it passes validation, but the request itself is made. `http.DefaultTransport` also honours `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`, so an operator-set proxy in `controller.extraEnv` sees the whole fetch path.

## Eviction

Entries that haven't been read in `maxAge` are evicted on the next `EvictUnused()` call. Entries with pending content are never evicted (pending must always have a place to land or be rejected). The event adapter calls `EvictUnused` periodically; for tests, pass `maxAge: 0` to disable eviction entirely.

## See Also

- [`pkg/controller/httpstore`](../controller/httpstore/) — event adapter that drives refresh timers, handles validation lifecycle, and exposes the wrapper to templates
- [`pkg/controller/testrunner`](../controller/testrunner/) — uses `LoadFixture` to mock HTTP content during validation tests
- `pkg/httpstore/CLAUDE.md` — design notes (two-version pattern, eviction tuning, conditional-request mechanics)

## License

Apache-2.0 — see root `LICENSE`.
