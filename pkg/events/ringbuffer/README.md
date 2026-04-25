# pkg/events/ringbuffer

Fixed-size thread-safe ring buffer using Go generics. When it fills up, new items overwrite the oldest. Used by `pkg/controller/debug` for the `/debug/vars/events` endpoint. `pkg/controller/commentator` has its *own* specialised (non-generic) ring buffer with richer queries — if you're looking for how insights / correlation work, look there instead.

No dependencies beyond the standard library — the package could be lifted out verbatim.

## API

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/events/ringbuffer"

buf := ringbuffer.New[Event](1000)   // capacity, not starting length

buf.Add(evt)                          // O(1); overwrites oldest when full

recent := buf.GetLast(100)            // up to 100 newest items, oldest-first
all    := buf.GetAll()                // everything currently held, oldest-first
n      := buf.Len()                   // current count (≤ capacity)
```

All four operations are safe for concurrent use (`sync.RWMutex`). `GetLast` / `GetAll` return freshly allocated slices — mutating the returned slice does **not** affect the buffer, but the elements themselves are shared: if `T` has pointer fields, deep-copy before mutating.

## Behaviour

- The buffer is always returned in chronological order (oldest first), regardless of where the internal write head happens to be.
- `GetLast(n)` where `n > Len()` returns everything — it doesn't pad.
- `Add` never allocates after construction; the backing slice is reused in place.

## Sizing

Target capacity = rate × retention-window:

| Use case | Rate | Window | Size |
|----------|------|--------|------|
| Debug `/debug/vars/events` | event bus throughput | operator-friendly replay | ~1000 |
| Sliding-window metric | 1/s | 60s | 60 |

For large `T`, store pointers so the fixed-size backing array only holds one pointer per slot instead of the full struct.

## See Also

- [`pkg/controller/debug`](../../controller/debug/) — primary consumer; exposes the buffer via `/debug/vars/events`
- [`pkg/controller/commentator`](../../controller/commentator/) — domain-specific ring buffer (separate implementation) used for log-line event correlation
- `pkg/events/ringbuffer/CLAUDE.md` — developer context (wrap-around semantics, concurrency tests)

## License

Apache-2.0 — see root `LICENSE`.
