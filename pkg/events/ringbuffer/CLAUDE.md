# pkg/events/ringbuffer - Generic Ring Buffer

Development context for the ring buffer package.

**API Documentation**: See `pkg/events/ringbuffer/README.md`

## When to Use This Package

Use this package when you need to:

- Store a fixed-size sliding window of recent items
- Implement event history/buffering with automatic old-item eviction
- Thread-safe circular buffer with Go generics
- Efficient memory usage for bounded collections

**DO NOT** use this package for:

- Unbounded collections → Use slice or channel
- Persistent storage → Use database
- Priority queues → Use heap data structure

## Package Purpose

Provides a generic, thread-safe ring buffer implementation using Go generics. When the buffer fills up, new items automatically overwrite the oldest items (circular behavior).

Key features:

- **Generic** - Works with any type via Go generics
- **Thread-safe** - Uses sync.RWMutex for concurrent access
- **Fixed size** - Memory-bounded, won't grow indefinitely
- **Efficient** - O(1) add, O(n) retrieval

## Architecture

```
RingBuffer[T]
    ├── items []T          (fixed-size array)
    ├── size int           (capacity)
    ├── head int           (write position)
    ├── count int          (current items)
    └── mu sync.RWMutex    (thread safety)
```

Circular buffer behavior:

```
Initial:  [ ][ ][ ][ ][ ]  head=0, count=0
Add(1):   [1][ ][ ][ ][ ]  head=1, count=1
Add(2):   [1][2][ ][ ][ ]  head=2, count=2
Add(3,4,5): [1][2][3][4][5]  head=0, count=5 (full!)
Add(6):   [6][2][3][4][5]  head=1, count=5 (overwrites oldest)
```

## Key Types

### RingBuffer

```go
type RingBuffer[T any] struct {
    items []T
    size  int
    head  int
    count int
    mu    sync.RWMutex
}

// Create buffer with capacity of 1000
buffer := ringbuffer.New[Event](1000)
```

### Operations

```go
// Add item (thread-safe)
buffer.Add(item)

// Get last N items (chronological order)
recent := buffer.GetLast(100)

// Get all items (up to capacity)
all := buffer.GetAll()

// Get current count
n := buffer.Len()
```

## Usage Patterns

### Event History Buffer

```go
type Event struct {
    Timestamp time.Time
    Type      string
    Message   string
}

// Create buffer for last 1000 events
eventBuffer := ringbuffer.New[Event](1000)

// Add events as they occur
go func() {
    for event := range eventStream {
        eventBuffer.Add(event)
    }
}()

// Query recent events
recent := eventBuffer.GetLast(100)  // Last 100 events
```

### Debug Log Buffer

```go
type LogEntry struct {
    Level   string
    Message string
    Time    time.Time
}

logBuffer := ringbuffer.New[LogEntry](500)

// Log entries
logBuffer.Add(LogEntry{
    Level:   "INFO",
    Message: "Server started",
    Time:    time.Now(),
})

// Dump recent logs
recentLogs := logBuffer.GetAll()
for _, entry := range recentLogs {
    fmt.Printf("[%s] %s: %s\n", entry.Time, entry.Level, entry.Message)
}
```

### Metrics Sliding Window

```go
type Metric struct {
    Timestamp time.Time
    Value     float64
}

metricsBuffer := ringbuffer.New[Metric](60)  // Last 60 seconds

// Record metric every second
ticker := time.NewTicker(1 * time.Second)
for range ticker.C {
    metricsBuffer.Add(Metric{
        Timestamp: time.Now(),
        Value:     getCurrentValue(),
    })
}

// Calculate average over window
metrics := metricsBuffer.GetAll()
sum := 0.0
for _, m := range metrics {
    sum += m.Value
}
avg := sum / float64(len(metrics))
```

## Integration with Other Packages

This is a **pure generic data structure** with no dependencies on other application packages.

Used by:

- `pkg/controller/debug` — backs the `/debug/vars/events` endpoint via `EventBuffer` (see `pkg/controller/debug/events.go`).
- Any component needing bounded event/log storage.

NOT used by `pkg/controller/commentator`. The commentator has its own
non-generic `RingBuffer` (`pkg/controller/commentator/ring_buffer.go`) with
`FindByCorrelationID` / `FindByTypeInWindow` / `FindRecent` queries that this
package doesn't expose. Don't conflate the two.

## Common Pitfalls

### Not Understanding Wrap-Around

**Problem**: Expecting items to stay in buffer indefinitely.

```go
buffer := ringbuffer.New[int](3)
buffer.Add(1)
buffer.Add(2)
buffer.Add(3)
buffer.Add(4)  // Overwrites 1!

all := buffer.GetAll()  // [2, 3, 4] - item 1 is gone!
```

**Solution**: Size buffer appropriately for your use case.

### Reference Semantics for Pointer Element Types

**Problem**: When `T` is a pointer (or contains pointers), the returned slice
gives you the same underlying objects the buffer holds. Mutating through a
pointer mutates whatever else is holding it.

`GetLast` and `GetAll` both allocate a fresh `[]T` and copy elements into it,
so the **slice itself** is yours to modify (e.g. `result[0] = newEvent` doesn't
poke the buffer). For value-typed `T`, that's the end of the story — you have
your own copies. For pointer-typed `T`, the slice contains pointer values; the
objects they point to are still shared.

```go
// Value-typed T: copy is complete, mutation is local.
buffer := ringbuffer.New[Event](100)
events := buffer.GetAll()
events[0].Message = "MODIFIED" // safe: only this slice's element changes
```

```go
// Pointer-typed T: the slice is fresh but the events are shared.
buffer := ringbuffer.New[*Event](100)
events := buffer.GetAll()
events[0].Message = "MODIFIED" // mutates the same Event the buffer still holds
```

**Solution**: For pointer-typed `T`, deep-copy at the read site if you need
isolation, or store value-typed `T` in the first place — `RingBuffer[Event]`
costs nothing extra over `RingBuffer[*Event]` for small structs.

### Wrong Buffer Size

**Problem**: Buffer too small loses important data, too large wastes memory.

```go
// Too small - frequent overwrites
buffer := ringbuffer.New[Event](10)  // Only keeps last 10 events

// Too large - wastes memory
buffer := ringbuffer.New[Event](1000000)  // Probably excessive
```

**Solution**: Choose size based on:

- Event rate (events/second × retention time)
- Memory constraints
- Query patterns (how much history do you need?)

Example:

```go
// Keep 5 minutes of events at 10 events/second
eventsPerSecond := 10
retentionSeconds := 300
bufferSize := eventsPerSecond * retentionSeconds
buffer := ringbuffer.New[Event](bufferSize)  // 3000 events
```

## Testing Approaches

### Unit Tests

Test buffer behavior:

```go
func TestRingBuffer_WrapAround(t *testing.T) {
    buffer := ringbuffer.New[int](3)

    // Fill buffer
    buffer.Add(1)
    buffer.Add(2)
    buffer.Add(3)

    // Verify full
    assert.Equal(t, 3, buffer.Len())
    assert.Equal(t, []int{1, 2, 3}, buffer.GetAll())

    // Add more - should wrap
    buffer.Add(4)
    assert.Equal(t, []int{2, 3, 4}, buffer.GetAll())

    buffer.Add(5)
    assert.Equal(t, []int{3, 4, 5}, buffer.GetAll())
}

func TestRingBuffer_GetLast(t *testing.T) {
    buffer := ringbuffer.New[int](100)

    for i := 1; i <= 50; i++ {
        buffer.Add(i)
    }

    // Get last 10
    last := buffer.GetLast(10)
    expected := []int{41, 42, 43, 44, 45, 46, 47, 48, 49, 50}
    assert.Equal(t, expected, last)

    // Get more than available
    last = buffer.GetLast(100)
    assert.Equal(t, 50, len(last))
}
```

### Concurrency Tests

Test thread safety:

```go
func TestRingBuffer_Concurrent(t *testing.T) {
    buffer := ringbuffer.New[int](1000)

    // Multiple writers
    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < 100; j++ {
                buffer.Add(id*100 + j)
            }
        }(i)
    }

    // Concurrent readers
    for i := 0; i < 5; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < 100; j++ {
                _ = buffer.GetAll()
                _ = buffer.Len()
            }
        }()
    }

    wg.Wait()

    // Verify final state
    assert.Equal(t, 1000, buffer.Len())  // Buffer full
}
```

## Performance Characteristics

- **Add**: O(1) - constant time, just increments head
- **GetLast(n)**: O(n) - copies n items to new slice
- **GetAll**: O(size) - copies all items
- **Len**: O(1) - just reads count

Memory: O(size) - fixed allocation, no growth

## Resources

- Go generics: <https://go.dev/doc/tutorial/generics>
- Ring buffer algorithm: <https://en.wikipedia.org/wiki/Circular_buffer>
- Usage in debug: `pkg/controller/debug/events.go` (the only consumer in this repo). The commentator package implements its own ring buffer; this generic one is not imported there.
