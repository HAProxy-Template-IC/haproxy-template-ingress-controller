# pkg/httpstore - HTTP Resource Store

Development context for the pure HTTP resource store component.

## When to Work Here

Work in this package when:
- Modifying HTTP fetching logic (retries, timeouts, authentication)
- Changing the two-version caching behavior (pending/accepted)
- Adding new authentication methods
- Modifying conditional request handling (ETag, If-Modified-Since)

**DO NOT** work here for:
- Event coordination (refresh timers, validation events) → Use `pkg/controller/httpstore`
- Template integration → Use `pkg/controller/renderer`
- Reconciliation triggers → Use `pkg/controller/reconciler`

## Package Purpose

Pure HTTP resource store with two-version caching for safe content updates. Provides the core fetching and caching logic without event bus dependencies.

This is a **pure component** following the codebase's architecture pattern - it has no knowledge of the event bus and can be used independently for testing.

## Two-Version Cache Pattern

The store maintains two content versions for each URL:

```
         ┌──────────────────────────────────────────────────┐
         │                    CacheEntry                     │
         │                                                   │
         │  AcceptedContent ◄── Used for production render   │
         │  AcceptedChecksum                                 │
         │  AcceptedTime                                     │
         │                                                   │
         │  PendingContent  ◄── New content from refresh     │
         │  PendingChecksum                                  │
         │  HasPending: true                                 │
         └──────────────────────────────────────────────────┘

Validation Success: PromotePending()
   └── PendingContent → AcceptedContent
       HasPending = false

Validation Failure: RejectPending()
   └── PendingContent discarded
       HasPending = false
       AcceptedContent preserved
```

This pattern ensures that invalid HTTP content (e.g., malformed IP blocklist) never breaks the HAProxy configuration.

## Key Methods

### Fetching

```go
// Initial fetch (synchronous, caches result)
content, err := store.Fetch(ctx, url, FetchOptions{
    Timeout:  30 * time.Second,
    Retries:  3,
    Critical: true,  // Return error if fetch fails
    Delay:    5 * time.Minute,  // For periodic refresh
}, &AuthConfig{
    Type:     "bearer",
    Token:    "secret",
})

// Refresh (stores in pending, returns true if content changed)
changed, err := store.RefreshURL(ctx, url)
```

### Cache Access

```go
// Get accepted content (for production render)
content, ok := store.Get(url)

// Get pending or accepted (for validation render)
content, ok := store.GetForValidation(url)

// Get all URLs with pending content
urls := store.GetPendingURLs()
```

### Validation Lifecycle

```go
// After successful validation - promote pending to accepted
promoted := store.PromotePending(url)

// After failed validation - discard pending, keep accepted
rejected := store.RejectPending(url)
```

### Test Fixtures

```go
// Pre-load content for validation tests (no HTTP request)
store.LoadFixture("http://example.com/data.txt", "mock content")

// Content is immediately available as accepted
content, ok := store.Get("http://example.com/data.txt")
// content = "mock content", ok = true
```

Used by `pkg/controller/testrunner` to mock HTTP resources in validation tests.

## Authentication

Three authentication methods are supported:

```go
// Basic authentication
auth := &AuthConfig{
    Type:     "basic",
    Username: "user",
    Password: "pass",
}

// Bearer token
auth := &AuthConfig{
    Type:  "bearer",
    Token: "secret-token",
}

// Custom headers
auth := &AuthConfig{
    Type: "header",
    Headers: map[string]string{
        "X-API-Key": "my-key",
    },
}
```

## Conditional Requests

The store automatically uses conditional requests when refreshing:

- Stores `ETag` and `Last-Modified` headers from responses
- Sends `If-None-Match` and `If-Modified-Since` on refresh
- Returns `changed=false` on 304 Not Modified responses

This minimizes bandwidth usage for frequently-refreshed resources.

## Common Pitfalls

### Non-Critical Fetch Returns Empty String

**Problem**: Fetch returns empty string without error.

**Solution**: Set `Critical: true` if you need to fail on fetch errors.

```go
// Bad - silently returns empty on failure
content, err := store.Fetch(ctx, url, FetchOptions{}, nil)
// err is nil, content is ""

// Good - returns error on failure
content, err := store.Fetch(ctx, url, FetchOptions{Critical: true}, nil)
// err contains the actual error
```

### Forgetting to Promote/Reject Pending

**Problem**: Pending content stays in pending state forever.

**Solution**: Always call `PromotePending()` or `RejectPending()` after validation.

### Using Get() During Validation Render

**Problem**: Validation render uses old content, passes, but production uses different content.

**Solution**: Use `GetForValidation()` during validation render to see pending content.

## Integration with Event Adapter

The event adapter (`pkg/controller/httpstore`) wraps this pure component:

```go
// Pure component (this package)
store := httpstore.New(logger)

// Event adapter wraps it
component := httpstore.New(eventBus, logger)  // different package!

// Wrapper provides template-callable interface
wrapper := httpstore.NewHTTPStoreWrapper(component, logger, isValidation, ctx)
```

## Testing

Unit tests are in `store_test.go` and use `httptest.NewServer` for HTTP mocking.

```bash
# Run tests
go test ./pkg/httpstore/... -v

# Run specific test
go test ./pkg/httpstore/... -v -run TestHTTPStore_FetchAndGet
```

## Resources

- Event adapter: `pkg/controller/httpstore/CLAUDE.md`
- Architecture: `/docs/development/design.md`
- Controller integration: `pkg/controller/CLAUDE.md`
