# pkg/events/ringbuffer

Generic thread-safe ring buffer implementation using Go generics.

## Overview

A ring buffer (circular buffer) is a fixed-size data structure that overwrites the oldest items when full. This implementation is generic (works with any type) and thread-safe for concurrent access.

**Use cases:**

- Event history/buffering
- Sliding window metrics
- Recent log storage
- Any bounded collection with automatic old-item eviction

## Installation

```go
import "haproxy-template-ic/pkg/events/ringbuffer"
```

## Quick Start

```go
// Create buffer with capacity of 1000
buffer := ringbuffer.New[int](1000)

// Add items
buffer.Add(1)
buffer.Add(2)
buffer.Add(3)

// Retrieve items
all := buffer.GetAll()           // All items (up to 1000)
recent := buffer.GetLast(10)     // Last 10 items
count := buffer.Len()            // Current count
```

## API Reference

### Creating a Buffer

```go
func New[T any](size int) *RingBuffer[T]
```

Creates a new ring buffer with the specified capacity.

**Parameters:**

- `size` - Maximum number of items to store

**Example:**

```go
// Buffer for last 1000 events
eventBuffer := ringbuffer.New[Event](1000)

// Buffer for last 60 metrics
metricsBuffer := ringbuffer.New[Metric](60)
```

### Adding Items

```go
func (rb *RingBuffer[T]) Add(item T)
```

Adds an item to the buffer. If buffer is full, overwrites the oldest item.

**Thread-safe**: Yes

**Example:**

```go
buffer := ringbuffer.New[string](100)
buffer.Add("event1")
buffer.Add("event2")
// When full, oldest items are automatically removed
```

### Retrieving Items

```go
func (rb *RingBuffer[T]) GetLast(n int) []T
```

Returns the last N items in chronological order (oldest first).

If N exceeds the current count, returns all available items.

**Thread-safe**: Yes
**Returns**: New slice (does not expose internal buffer)

**Example:**

```go
// Get last 100 events
recent := buffer.GetLast(100)

// Get last 10
last10 := buffer.GetLast(10)
```

```go
func (rb *RingBuffer[T]) GetAll() []T
```

Returns all items currently in the buffer in chronological order.

**Thread-safe**: Yes
**Returns**: New slice

**Example:**

```go
allItems := buffer.GetAll()
for _, item := range allItems {
    // Process items in chronological order
}
```

### Getting Count

```go
func (rb *RingBuffer[T]) Len() int
```

Returns the current number of items in the buffer (never exceeds capacity).

**Thread-safe**: Yes

**Example:**

```go
count := buffer.Len()
capacity := 1000
utilization := float64(count) / float64(capacity)
```

## Behavior

### Circular Overwriting

When the buffer is full, new items overwrite the oldest items:

```go
buffer := ringbuffer.New[int](3)

buffer.Add(1)  // [1, _, _]
buffer.Add(2)  // [1, 2, _]
buffer.Add(3)  // [1, 2, 3] - full!
buffer.Add(4)  // [4, 2, 3] - overwrites 1
buffer.Add(5)  // [4, 5, 3] - overwrites 2

all := buffer.GetAll()  // [3, 4, 5] in chronological order
```

### Chronological Order

Items are always returned in chronological order (oldest first):

```go
buffer := ringbuffer.New[string](5)
buffer.Add("first")
buffer.Add("second")
buffer.Add("third")

all := buffer.GetAll()
// Returns: ["first", "second", "third"]
// NOT: ["third", "second", "first"]
```

### Thread Safety

All operations are thread-safe:

```go
buffer := ringbuffer.New[int](1000)

// Safe to use from multiple goroutines
go func() {
    for i := 0; i < 100; i++ {
        buffer.Add(i)
    }
}()

go func() {
    for i := 0; i < 100; i++ {
        recent := buffer.GetLast(10)
        fmt.Println(len(recent))
    }
}()
```

## Common Use Cases

### Event History

```go
type Event struct {
    Timestamp time.Time
    Type      string
    Data      map[string]interface{}
}

eventBuffer := ringbuffer.New[Event](1000)

// Store events
eventBuffer.Add(Event{
    Timestamp: time.Now(),
    Type:      "user.login",
    Data:      map[string]interface{}{"user_id": 123},
})

// Query recent events
recentEvents := eventBuffer.GetLast(100)
```

### Sliding Window Metrics

```go
type Metric struct {
    Timestamp time.Time
    Value     float64
}

// Keep last 60 seconds of metrics
metricsWindow := ringbuffer.New[Metric](60)

// Record every second
ticker := time.NewTicker(1 * time.Second)
for range ticker.C {
    metricsWindow.Add(Metric{
        Timestamp: time.Now(),
        Value:     getCurrentMetric(),
    })

    // Calculate rolling average
    metrics := metricsWindow.GetAll()
    sum := 0.0
    for _, m := range metrics {
        sum += m.Value
    }
    avg := sum / float64(len(metrics))
    fmt.Printf("Rolling avg: %.2f\n", avg)
}
```

### Debug Log Buffer

```go
type LogEntry struct {
    Level     string
    Message   string
    Timestamp time.Time
}

debugLogs := ringbuffer.New[LogEntry](500)

// Log function
func Log(level, message string) {
    debugLogs.Add(LogEntry{
        Level:     level,
        Message:   message,
        Timestamp: time.Now(),
    })
}

// Dump recent logs
func DumpLogs() {
    logs := debugLogs.GetAll()
    for _, entry := range logs {
        fmt.Printf("[%s] %s: %s\n",
            entry.Timestamp.Format(time.RFC3339),
            entry.Level,
            entry.Message)
    }
}
```

## Choosing Buffer Size

Buffer size depends on:

1. **Event rate** - How many items per second?
2. **Retention time** - How much history do you need?
3. **Memory constraints** - How much memory can you use?

**Formula:**

```
buffer_size = event_rate × retention_seconds
```

**Examples:**

```go
// 10 events/second, keep 5 minutes (300s)
buffer := ringbuffer.New[Event](10 * 300)  // 3000

// 100 events/second, keep 1 minute (60s)
buffer := ringbuffer.New[Event](100 * 60)  // 6000

// 1 event/second, keep 1 hour (3600s)
buffer := ringbuffer.New[Event](1 * 3600)  // 3600
```

## Memory Usage

Memory usage is fixed based on capacity:

```go
buffer := ringbuffer.New[Event](1000)
// Uses: 1000 × sizeof(Event) bytes
// Plus small overhead for: head, count, size fields
```

For large types, consider storing pointers:

```go
// Instead of
buffer := ringbuffer.New[LargeStruct](1000)  // 1000 × large size

// Use pointers
buffer := ringbuffer.New[*LargeStruct](1000)  // 1000 × 8 bytes (pointer size)
```

## Performance

- **Add**: O(1) - constant time
- **GetLast(n)**: O(n) - linear in items requested
- **GetAll**: O(size) - linear in buffer capacity
- **Len**: O(1) - constant time

No allocations during Add (except initial buffer creation).

## Examples

See:

- Event buffering: `pkg/controller/commentator/commentator.go`
- Debug events: `pkg/controller/debug/events.go`
- Tests: `pkg/events/ringbuffer/ringbuffer_test.go`

## License

See main repository for license information.
