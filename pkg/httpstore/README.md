# pkg/httpstore

Pure HTTP resource store with two-version (pending/accepted) caching for safe content updates.

## Overview

`HTTPStore` fetches arbitrary HTTP content (IP blocklists, JSON config, anything templates need from outside the cluster) and caches it under a two-version pattern: refreshed content lands in **pending** until the controller validates it, then it's promoted to **accepted**. Production renders only ever see the accepted version, so an upstream serving garbage can't break the live HAProxy config.

This is the **pure** half of the design. Periodic refresh timers, validation event handling, eviction, and the template-callable wrapper all live in `pkg/controller/httpstore`. Reach for the pure store directly only from tests; in production the event adapter is what you want.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"

store := httpstore.New(logger, 2*time.Minute) // maxAge for eviction; 0 disables

// Initial fetch (synchronous)
content, err := store.Fetch(ctx, "https://example.com/blocklist.txt",
    httpstore.FetchOptions{
        Timeout:  30 * time.Second,
        Retries:  3,
        Critical: true, // false → return "" + warn on failure
        Delay:    5 * time.Minute, // refresh interval (the event adapter drives the timer)
    },
    &httpstore.AuthConfig{Type: "bearer", Token: "secret"},
)

// Later: refresh into pending (returns true if content changed)
changed, err := store.RefreshURL(ctx, "https://example.com/blocklist.txt")

// Production render reads accepted only
got, ok := store.Get(url)

// Validation render reads pending if available, otherwise accepted
got, ok = store.GetForValidation(url)

// After the validation pipeline returns:
store.PromotePending(url) // success → pending becomes accepted
store.RejectPending(url)  // failure → pending discarded, accepted preserved

// Test fixtures bypass HTTP entirely
store.LoadFixture(url, "mock content")
```

## Authentication

Three modes via `AuthConfig.Type`:

- `"basic"` — `Username` + `Password`
- `"bearer"` — `Token`
- `"header"` — `Headers` map (e.g. `X-API-Key: ...`)

## Conditional Requests

Refreshes use ETag and Last-Modified automatically; a 304 returns `changed=false` so the existing accepted/pending state is preserved. Repeated refreshes against an unchanged upstream cost one round-trip and zero cache churn.

## Size Limit

Responses are capped at `MaxContentSize` (10 MiB, declared in `types.go` and enforced in `fetcher.go` via `io.LimitReader`). Larger payloads fail with an explicit `response body exceeds maximum size of N bytes` error rather than being silently truncated, and the limited reader means the store never buffers more than ~10 MiB even for a runaway upstream. There is no per-call override; if you legitimately need larger blobs, change the constant.

## Eviction

Entries that haven't been read in `maxAge` are evicted on the next `EvictUnused()` call. Entries with pending content are never evicted (pending must always have a place to land or be rejected). The event adapter calls `EvictUnused` periodically; for tests, pass `maxAge: 0` to disable eviction entirely.

## See Also

- [`pkg/controller/httpstore`](../controller/httpstore/) — event adapter that drives refresh timers, handles validation lifecycle, and exposes the wrapper to templates
- [`pkg/controller/testrunner`](../controller/testrunner/) — uses `LoadFixture` to mock HTTP content during validation tests
- `pkg/httpstore/CLAUDE.md` — design notes (two-version pattern, eviction tuning, conditional-request mechanics)

## License

Apache-2.0 — see root `LICENSE`.
