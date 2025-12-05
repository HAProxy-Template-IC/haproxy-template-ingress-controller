# pkg/controller/dryrunvalidator - Dry-Run Validation Component

Development context for the dry-run validation component used in admission webhooks.

## When to Work Here

Modify this package when:

- Changing dry-run validation logic
- Adding support for new resource types in validation
- Modifying overlay store usage
- Improving error messages for validation failures
- Adding new validation phases

**DO NOT** modify this package for:

- Template rendering logic → Use `pkg/templating`
- HAProxy validation → Use `pkg/dataplane`
- Resource storage → Use `pkg/controller/resourcestore`
- Webhook server → Use `pkg/webhook`

## Package Purpose

This package implements validation-by-rendering: it validates Kubernetes resources (like Ingresses) by attempting to render the HAProxy configuration with those resources included, then validating the resulting configuration with HAProxy.

**Key Features:**

- Dry-run validation (no actual deployment)
- Overlay store pattern for simulating resource changes
- Template rendering errors → user-friendly messages
- HAProxy validation errors → user-friendly messages
- Support for CREATE, UPDATE, DELETE operations

## Overlay Store Pattern

The overlay store pattern allows the validator to simulate "what-if" scenarios without modifying the actual resource stores. This is crucial for validating resources before they're admitted to the cluster.

### Problem Statement

When validating a CREATE or UPDATE operation via admission webhook:

1. The resource **doesn't exist yet** in the actual store (CREATE)
2. The resource exists but with **old version** (UPDATE)
3. We need to **validate** the config that would result from the change
4. We **cannot modify** the actual stores (dry-run only)

### Solution: Overlay Stores

Create temporary store overlays that:

- Include the new/updated resource being validated
- Reference the actual stores for all other resources
- Are discarded after validation

```
┌────────────────────────────────────────┐
│     Actual Resource Stores             │
│  (shared, read-only during validation) │
│                                        │
│  ingresses: [...existing ingresses...] │
│  services:  [...existing services...]  │
└────────────────┬───────────────────────┘
                 │
                 │ Reference (read-only)
                 ▼
┌────────────────────────────────────────┐
│      Overlay Store (temporary)         │
│                                        │
│  ingresses: [                          │
│    ...existing from actual store...    │
│    + NEW/UPDATED resource being tested │
│  ]                                     │
│  services: [read from actual store]    │
└────────────────────────────────────────┘
                 │
                 │ Used for rendering
                 ▼
        Template Rendering
                 │
                 ▼
       HAProxy Config (dry-run)
                 │
                 ▼
      Validation (accept/reject)
```

### Implementation

The overlay creation is handled by StoreManager (utility component):

```go
// pkg/controller/dryrunvalidator/component.go
func (c *Component) handleValidationRequest(req *events.WebhookValidationRequest) {
    // Determine operation type
    operation := overlaystore.OperationCreate
    if req.OldObject != nil {
        operation = overlaystore.OperationUpdate
    }

    // Create overlay stores with the resource being validated
    overlayStores, err := c.storeManager.CreateOverlayMap(
        resourceType,      // e.g., "ingresses"
        req.Namespace,     // Resource namespace
        req.Name,          // Resource name
        req.Object,        // New/updated resource
        operation,         // CREATE or UPDATE
    )

    // Use overlay stores for template rendering
    templateContext := map[string]interface{}{
        "ingresses": overlayStores["ingresses"],  // Includes the test resource
        "services":  overlayStores["services"],   // References actual store
        // ... other resource types
    }

    // Render template with overlay context
    haproxyConfig, err := c.engine.Render("haproxy.cfg", templateContext)

    // Validate the resulting configuration
    // ... (resource is discarded after validation)
}
```

### StoreManager Direct Call

The DryRunValidator calls StoreManager directly (not via events) because:

1. **Utility Component**: StoreManager is infrastructure, not domain logic
2. **Synchronous Operation**: Overlay creation is immediate, no async coordination needed
3. **Performance**: Avoiding event overhead for performance-critical validation path
4. **Scoped Lifetime**: Overlays are ephemeral, exist only during validation

This is documented in the main CLAUDE.md as an acceptable exception to event-driven patterns.

### Operation Types

```go
type OverlayOperation int

const (
    OperationCreate OverlayOperation = iota
    OperationUpdate
    OperationDelete  // Not yet implemented
)
```

**CREATE**: Add resource to overlay (not in actual store)
**UPDATE**: Replace existing resource in overlay
**DELETE**: Remove resource from overlay (future)

### Memory Management

Overlay stores are:

- Created per validation request
- Garbage collected after validation completes
- Do not persist beyond the request
- Share underlying data from actual stores (copy-on-write semantics)

## Validation Flow

```
Webhook Admission Request
    ↓
1. Parse GVK → Determine resource type
    ↓
2. Create overlay stores (includes test resource)
    ↓
3. Build template context (use overlays)
    ↓
4. Render HAProxy config
    ├─ Success → Continue
    └─ Error → SimplifyRenderingError → Deny
    ↓
5. Validate HAProxy config
    ├─ Valid → Allow
    └─ Invalid → SimplifyValidationError → Deny
    ↓
Publish WebhookValidationResponse
```

## Error Handling

The component uses error simplification at component boundaries:

### Template Rendering Errors

```go
haproxyConfig, err := c.engine.Render("haproxy.cfg", templateContext)
if err != nil {
    // Simplify template errors (extract fail() messages)
    simplified := dataplane.SimplifyRenderingError(err)

    c.eventBus.Publish(events.NewWebhookValidationResponse(
        req.RequestID,
        "dryrun",
        false,
        simplified,  // User-friendly: "Service 'api' not found"
    ))
    return
}
```

### HAProxy Validation Errors

```go
_, err := c.validator.ValidateConfig(ctx, haproxyConfig, nil, 0)
if err != nil {
    // Simplify HAProxy errors (extract meaningful parts)
    simplified := dataplane.SimplifyValidationError(err)

    c.eventBus.Publish(events.NewWebhookValidationResponse(
        req.RequestID,
        "dryrun",
        false,
        simplified,  // User-friendly: "maxconn must be >= 1 (got 0)"
    ))
    return
}
```

## Direct Component Calls Pattern

The DryRunValidator demonstrates the acceptable pattern of calling pure components directly within a reconciliation context:

```go
type Component struct {
    engine       *templating.TemplateEngine  // Pure component - called directly
    validator    *dataplane.Validator        // Pure component - called directly
    storeManager *resourcestore.Manager      // Utility component - called directly
    eventBus     *busevents.EventBus         // Event coordination
}

func (c *Component) handleValidationRequest(req *events.WebhookValidationRequest) {
    // Direct utility call - acceptable
    overlayStores, err := c.storeManager.CreateOverlayMap(...)

    // Direct pure component call - acceptable within reconciliation context
    haproxyConfig, err := c.engine.Render("haproxy.cfg", templateContext)

    // Direct pure component call - acceptable
    _, err := c.validator.ValidateConfig(ctx, haproxyConfig, nil, 0)

    // Event publishing - coordination with other components
    c.eventBus.Publish(events.NewWebhookValidationResponse(...))
}
```

**Why direct calls are acceptable here:**

1. **Single reconciliation context**: All calls happen within one validation request
2. **No cross-component coordination**: No other components need to observe these operations
3. **Performance critical**: Webhook timeouts are tight (10 seconds)
4. **Stateless**: Each validation is independent

This pattern is documented in `/CLAUDE.md` and `pkg/controller/CLAUDE.md` as an exception to strict event-driven patterns for performance-critical paths.

## Testing Strategy

### Unit Tests

Test error simplification:

```go
func TestSimplifyRenderingError(t *testing.T) {
    rawErr := errors.New("failed to render: invalid call to function 'fail': Service not found")
    simplified := dataplane.SimplifyRenderingError(rawErr)
    assert.Equal(t, "Service not found", simplified)
}
```

### Integration Tests (Future)

Test full validation flow with overlay stores:

```go
func TestValidateIngress_WithOverlay(t *testing.T) {
    // Setup actual stores
    actualStores := setupActualStores(t)

    // Create test ingress
    testIngress := &unstructured.Unstructured{...}

    // Create validator
    validator := NewComponent(storeManager, engine, validator, eventBus)

    // Create validation request
    req := &events.WebhookValidationRequest{
        Object:    testIngress,
        Namespace: "default",
        Name:      "test-ingress",
    }

    // Validate (should create overlay internally)
    validator.handleValidationRequest(req)

    // Verify validation result
    // ... check WebhookValidationResponse event
}
```

## Common Pitfalls

### Modifying Actual Stores

**Problem**: Accidentally modifying actual stores during validation.

```go
// Bad - modifies actual store
actualStore := c.storeManager.GetStore("ingresses")
actualStore.Add(req.Object)  // Pollutes actual store!
```

**Solution**: Always use overlay stores.

```go
// Good - uses overlay
overlayStores, err := c.storeManager.CreateOverlayMap(...)
// Overlay is discarded after validation
```

### Forgetting Error Simplification

**Problem**: Returning raw template/validation errors to webhook.

```go
// Bad - raw error exposed to user
if err != nil {
    c.eventBus.Publish(events.NewWebhookValidationResponse(
        req.RequestID,
        "dryrun",
        false,
        err.Error(),  // "failed to render haproxy.cfg: ..."
    ))
}
```

**Solution**: Always simplify errors at boundaries.

```go
// Good - user-friendly message
if err != nil {
    simplified := dataplane.SimplifyRenderingError(err)
    c.eventBus.Publish(events.NewWebhookValidationResponse(
        req.RequestID,
        "dryrun",
        false,
        simplified,  // "Service 'api' not found"
    ))
}
```

### Not Handling All Operations

**Problem**: Only handling CREATE, ignoring UPDATE.

```go
// Bad - UPDATE uses wrong operation type
operation := overlaystore.OperationCreate  // Always CREATE
```

**Solution**: Detect operation from request.

```go
// Good - detect operation type
operation := overlaystore.OperationCreate
if req.OldObject != nil {
    operation = overlaystore.OperationUpdate
}
```

## Validation Tests Integration

**Status**: IMPLEMENTED ✓

The DryRunValidator now integrates the test runner to automatically execute embedded validation tests during webhook admission control.

**Implementation Details**:

1. **Test Runner Creation**: DryRunValidator creates a test runner instance in the constructor with Workers=1 (sequential execution for webhook context)

2. **Test Execution Flow**: After HAProxy configuration validation passes, if `config.ValidationTests` is non-empty:
   - Publishes `ValidationTestsStartedEvent`
   - Runs all validation tests via `testRunner.RunTests()`
   - Publishes `ValidationTestsCompletedEvent` with results
   - If tests fail: publishes `ValidationTestsFailedEvent` and rejects admission with detailed error message
   - If tests pass: continues to admission approval

3. **Error Messages**: Detailed error messages include:
   - Number of failed tests
   - Test names that failed
   - Rendering errors (if any)
   - Assertion failures with descriptions

**Benefits**:

- Automated validation on every admission request
- Prevents invalid configurations from being admitted
- Rich error feedback for debugging
- Metrics and observability through event publishing
- No need to run CLI validation separately

**Configuration**: Add `validationTests` to your HAProxyTemplateConfig CRD to enable automatic webhook validation.

### DELETE Operation Support

Currently DELETE is not fully implemented. To add:

1. Implement OperationDelete in overlay store
2. Handle DELETE in handleValidationRequest
3. Test that config is valid after resource removal

### Parallel Validation

For performance, could validate multiple resources concurrently:

```go
// Future: parallel validation
var wg sync.WaitGroup
results := make(chan *ValidationResult, len(requests))

for _, req := range requests {
    wg.Add(1)
    go func(r *Request) {
        defer wg.Done()
        results <- c.validate(r)
    }(req)
}

wg.Wait()
close(results)
```

### Validation Caching

Cache validation results for identical configurations:

```go
// Future: cache by config hash
configHash := hashConfig(haproxyConfig)
if cached, ok := c.validationCache.Get(configHash); ok {
    return cached.Valid, cached.Reason
}

valid, reason := c.validator.ValidateConfig(...)
c.validationCache.Set(configHash, ValidationResult{valid, reason})
```

## Resources

- StoreManager: `pkg/controller/resourcestore/CLAUDE.md`
- Error simplification: `pkg/dataplane/CLAUDE.md`
- Event-driven patterns: `pkg/controller/CLAUDE.md`
- Webhook integration: `pkg/controller/webhook/CLAUDE.md`
