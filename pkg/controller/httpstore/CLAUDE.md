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
- Periodic eviction of unused cache entries to prevent memory growth

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
│   - Publishes ProposalValidationRequestedEvent on refresh    │
│   - Subscribes to ProposalValidationCompletedEvent           │
│     (branches on event.Valid → promote or reject)            │
│   - Publishes HTTPResourceUpdated/Accepted/RejectedEvent     │
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
        ├── triggerProposalValidation(url):
        │     ├── Publish ProposalValidationRequestedEvent
        │     │   (records its ID as pendingValidationID)
        │     └── Publish HTTPResourceUpdatedEvent (observability sibling)
        └── Reset timer
```

### On Proposal Validation Complete

The component subscribes to a *single* event — `ProposalValidationCompletedEvent`
— and branches on `event.Valid`. There is no separate "validation failed"
subscription:

```
ProposalValidationCompletedEvent received
    │  (dropped if event.RequestID doesn't match the component's pendingValidationID)
    ▼
event.Valid?
    │
    ├── true  → handleValidationSuccess()
    │           For each URL with pending content:
    │             ├── PromotePending() - pending → accepted
    │             └── Publish HTTPResourceAcceptedEvent
    │           Then publish ReconciliationTriggeredEvent("http_content_validated")
    │
    └── false → handleValidationFailure(event.Phase, event.Error)
                For each URL with pending content:
                  ├── RejectPending() - discard pending
                  └── Publish HTTPResourceRejectedEvent (reason from event.Error)
```

## Template Usage

The `HTTPStoreWrapper` provides a `Fetch()` method callable from templates:

```scriggo
{# Basic fetch — Scriggo declares variables with {% var %} (or {% x := y %}),
   not Jinja's {% set %}. #}
{% var content = http.Fetch("https://example.com/blocklist.txt") %}

{# With refresh interval #}
{% var content = http.Fetch("https://api.example.com/data", {"delay": "5m"}) %}

{# With authentication. There is no top-level `secrets` variable — read the
   Secret like any other watched resource via the `resources` map. The token
   value comes back base64-encoded from the API server. #}
{% var apiSecret = resources.secrets.GetSingle("kube-system", "my-api-secret") %}
{% var token = apiSecret | dig("data", "token") | b64decode %}
{% var content = http.Fetch("https://api.example.com/protected",
    {"delay": "10m"},
    {"type": "bearer", "token": token}
) %}

{# With all options #}
{% var ips = http.Fetch("https://blocklist.example.com/ips.txt",
    {"delay": "1h", "timeout": "30s", "retries": 3, "critical": true},
    {"type": "basic", "username": "user", "password": "pass"}
) %}
```

### Validation vs Production Render

The wrapper's behaviour depends on the `overlay stores.HTTPContentOverlay`
argument passed at construction (`NewHTTPStoreWrapper(ctx, component, logger, overlay)`),
not a `bool isValidation` flag:

- **Validation render** (`overlay != nil`): The wrapper consults the overlay
  first, then falls back to the store's pending content for URLs the overlay
  knows about. This lets the dryrun pipeline see content that hasn't been
  promoted yet, plus any test-fixture overrides.
- **Production render** (`overlay == nil`): The wrapper returns accepted content
  only — pending refreshes don't leak into the live HAProxy config until
  validation has signed off.

Both call paths register the URL for periodic refresh when `delay > 0`, so the
production renderer doesn't need to do anything special to start the timer.

## Component Lifecycle

```go
// Created during controller Stage 5
// evictionMaxAge is typically 2x drift prevention interval
driftInterval := cfg.Dataplane.GetDriftPreventionInterval()
httpStoreEvictionMaxAge := 2 * driftInterval
httpStoreComponent := httpstore.New(eventBus, logger, httpStoreEvictionMaxAge)

// Attached to renderer for template access
rendererComponent.SetHTTPStoreComponent(httpStoreComponent)

// Started as all-replica component
go httpStoreComponent.Start(ctx)
```

The eviction interval runs at the same cadence as `evictionMaxAge`. Entries not accessed within `evictionMaxAge` are evicted (unless they have pending validation content).

## Event Types

Published events (defined in `pkg/controller/events/`):

| Event | When | Purpose |
|-------|------|---------|
| `ProposalValidationRequestedEvent` | Refresh produced new content; before promoting it | Asks the proposal pipeline to validate the pending HTTP content via `HTTPOverlay`. The component records `event.ID` as `pendingValidationID` so it can correlate the response |
| `HTTPResourceUpdatedEvent` | Same call as above — sibling event for observability | Lets `commentator` / metrics see that content changed without subscribing to validation events |
| `HTTPResourceAcceptedEvent` | After a matching `ProposalValidationCompletedEvent` with `Valid == true` | Observability that pending → accepted promotion happened |
| `HTTPResourceRejectedEvent` | After a matching `ProposalValidationCompletedEvent` with `Valid == false` | Observability that pending was discarded; carries the reason from the validation error |
| `ReconciliationTriggeredEvent("http_content_validated", true)` | After a successful promotion (in `handleValidationSuccess`) | Coalescible reconciliation request so HAProxy picks up the new content |

Subscribed events:

| Event | Action |
|-------|--------|
| `ProposalValidationCompletedEvent` | Match against `pendingValidationID`; branch on `event.Valid` to either promote or reject pending content |

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
component := httpstore.New(bus, logger, 2*time.Minute) // eviction maxAge

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
