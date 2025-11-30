# pkg/controller/httpstore - HTTP Store Event Adapter

Development context for the HTTP store event adapter component.

## When to Work Here

Work in this package when:
- Modifying refresh timer behavior
- Changing validation event handling (promote/reject)
- Updating the template-callable wrapper interface
- Adding new event types for HTTP resources

**DO NOT** work here for:
- Core HTTP fetching logic → Use `pkg/httpstore`
- Template rendering → Use `pkg/controller/renderer`
- Reconciliation triggers → Use `pkg/controller/reconciler`

## Package Purpose

Event adapter wrapping the pure HTTP store (`pkg/httpstore`) with event bus coordination. This is a **Stage 5 component** that runs on all replicas.

Responsibilities:
- Manages periodic refresh timers for URLs with `delay > 0`
- Listens for validation events to promote/reject pending content
- Publishes HTTP resource events when content changes
- Provides template-callable wrapper for `http.Fetch()`

## Architecture

```
Template calls http.Fetch()
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│               HTTPStoreWrapper (wrapper.go)                  │
│   - Callable from templates                                  │
│   - Delegates to pure HTTPStore                              │
│   - Registers URLs for periodic refresh                      │
└─────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│                 Component (component.go)                     │
│   - Manages refresh timers                                   │
│   - Handles ValidationCompletedEvent                         │
│   - Handles ValidationFailedEvent                            │
│   - Publishes HTTPResourceUpdatedEvent                       │
└─────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│             pkg/httpstore.HTTPStore (pure)                   │
│   - HTTP fetching with retries                               │
│   - Two-version cache (pending/accepted)                     │
│   - Conditional requests (ETag)                              │
└─────────────────────────────────────────────────────────────┘
```

## Event Flow

### On Content Refresh

```
Timer expires
    │
    ▼
Component.refreshURL()
    │
    ├── Content unchanged (304 Not Modified)
    │   └── Reset timer, no event
    │
    └── Content changed
        ├── Store in pending
        ├── Publish HTTPResourceUpdatedEvent
        └── Reset timer
```

### On Validation Complete

```
ValidationCompletedEvent received
    │
    ▼
For each URL with pending content:
    ├── PromotePending() - pending → accepted
    └── Publish HTTPResourceAcceptedEvent
```

### On Validation Failed

```
ValidationFailedEvent received
    │
    ▼
For each URL with pending content:
    ├── RejectPending() - discard pending
    └── Publish HTTPResourceRejectedEvent
```

## Template Usage

The `HTTPStoreWrapper` provides a `Fetch()` method callable from templates:

```jinja2
{# Basic fetch #}
{% set content = http.Fetch("https://example.com/blocklist.txt") %}

{# With refresh interval #}
{% set content = http.Fetch("https://api.example.com/data", {"delay": "5m"}) %}

{# With authentication #}
{% set token = secrets.my_api_token | b64decode %}
{% set content = http.Fetch("https://api.example.com/protected",
    {"delay": "10m"},
    {"type": "bearer", "token": token}
) %}

{# With all options #}
{% set ips = http.Fetch("https://blocklist.example.com/ips.txt",
    {"delay": "1h", "timeout": "30s", "retries": 3, "critical": true},
    {"type": "basic", "username": "user", "password": pass}
) %}
```

### Validation vs Production Render

The wrapper behaves differently depending on render context:

- **Validation render** (`isValidation=true`): Returns pending content if available
- **Production render** (`isValidation=false`): Returns accepted content only

This ensures validation tests new content while production uses validated content.

## Component Lifecycle

```go
// Created during controller Stage 5
httpStoreComponent := httpstore.New(eventBus, logger)

// Attached to renderer for template access
rendererComponent.SetHTTPStoreComponent(httpStoreComponent)

// Started as all-replica component
go httpStoreComponent.Start(ctx)
```

## Event Types

Published events (defined in `pkg/controller/events/types.go`):

| Event | When | Purpose |
|-------|------|---------|
| `HTTPResourceUpdatedEvent` | Content changed on refresh | Triggers reconciliation |
| `HTTPResourceAcceptedEvent` | Pending promoted | Observability |
| `HTTPResourceRejectedEvent` | Pending rejected | Observability |

Subscribed events:

| Event | Action |
|-------|--------|
| `ValidationCompletedEvent` | Promote all pending content |
| `ValidationFailedEvent` | Reject all pending content |

## Common Pitfalls

### Timer Leak on Shutdown

**Problem**: Refresh timers keep running after context cancelled.

**Solution**: `stopAllRefreshers()` is called in the event loop's `ctx.Done()` case.

### Missing URL Registration

**Problem**: URL fetched but never refreshes.

**Solution**: `RegisterURL()` must be called after successful fetch. The wrapper does this automatically when `delay > 0`.

### Validation Event Race

**Problem**: Validation event arrives before refresh completes.

**Solution**: Store uses mutex protection. Pending content is only promoted/rejected if it exists.

## Testing

The component uses the EventBus for all coordination, making it easy to test:

```go
// Create components
bus := events.NewEventBus(100)
component := httpstore.New(bus, logger)

// Start component
go component.Start(ctx)

// Simulate validation completion
bus.Publish(events.NewValidationCompletedEvent(...))

// Verify accepted content is now available
```

## Resources

- Pure store: `pkg/httpstore/CLAUDE.md`
- Events catalog: `pkg/controller/events/types.go`
- Controller startup: `pkg/controller/controller.go`
