# pkg/controller/webhook - Webhook Adapter Component

Development context for the webhook adapter component.

**API Documentation**: See `pkg/controller/webhook/README.md`
**Pure Library**: See `pkg/webhook/CLAUDE.md`

## When to Work Here

Modify this package when:

- Changing webhook lifecycle management
- Modifying certificate rotation logic
- Wiring webhook to controller validators
- Adding webhook-specific event handling
- Changing webhook configuration management

**DO NOT** modify this package for:

- Webhook server implementation → Use `pkg/webhook`
- Certificate generation → Use `pkg/webhook`
- Event type definitions → Use `pkg/controller/events`
- Validation business logic → Use validator components

## Package Purpose

This is an **event adapter** that bridges the pure webhook library (`pkg/webhook`) to the controller's event-driven architecture.

**Key Responsibilities:**

- Manage webhook lifecycle (start, stop, rotation)
- Publish webhook events to EventBus
- Bridge webhook ValidationFunc to controller validators
- Coordinate certificate rotation with server restart

## Architecture Pattern

```
Pure Components          Event Adapter
(pkg/webhook/)          (pkg/controller/webhook/)

CertificateManager ─┐
Server             ─┼──wrapped by──→ Component
ConfigManager      ─┘                    ↓
                                   Publishes events
                                   Manages lifecycle
                                   Bridges validators
```

This follows the established pattern used throughout the controller:

- `pkg/templating` → `pkg/controller/renderer`
- `pkg/k8s` → `pkg/controller/resourcewatcher`
- `pkg/webhook` → `pkg/controller/webhook`

## Component Lifecycle

### Startup Sequence

1. **Generate Certificates**: Create CA and server certificates
2. **Create Server**: Initialize HTTPS server with certificates
3. **Register Validators**: Register ValidationFunc for each webhook rule
4. **Create Configuration**: Create ValidatingWebhookConfiguration in cluster
5. **Start Server**: Start HTTPS server in background goroutine
6. **Monitor Rotation**: Start certificate rotation monitor

### Shutdown Sequence

1. **Stop Server**: Cancel server context
2. **Delete Configuration**: Remove ValidatingWebhookConfiguration
3. **Publish Event**: Notify observers of shutdown

## Certificate Rotation

### Rotation Flow

```
Periodic Check (24h)
    ↓
Check expiry (< 30 days?)
    ↓
Generate new certs
    ↓
Update ValidatingWebhookConfiguration (new CA bundle)
    ↓
Stop old server
    ↓
Create new server (new certs)
    ↓
Re-register validators
    ↓
Start new server
    ↓
Publish rotation event
```

### Why Server Restart?

The webhook server holds certificates in memory. Go's `http.Server` doesn't support hot-reloading certificates, so we must restart the server with new certificates.

**Downtime**: Minimal (~100ms between stop and start). Kubernetes API server will retry failed requests.

## Validator Bridge Pattern

The component bridges webhook `ValidationFunc` to controller validators:

```go
// Webhook library expects this signature
type ValidationFunc func(obj interface{}) (allowed bool, reason string, err error)

// Component creates bridge function
func (c *Component) createValidator(gvk string) webhook.ValidationFunc {
    return func(obj interface{}) (bool, string, error) {
        // TODO Phase 2: Use request-response pattern
        // Request validation from controller validators
        // Aggregate results
        // Return decision

        // For now: allow all (fail-open)
        return true, "", nil
    }
}
```

### Phase 2: Full Validator Integration

When wiring to actual validators:

1. Publish `WebhookValidationRequestEvent` with full AdmissionRequest context
2. Use EventBus.Request() scatter-gather to collect validation results
3. Aggregate results from all validators
4. Return decision to webhook server
5. Publish result event (Allowed/Denied/Error)

## Event Publishing

The component publishes events at key lifecycle points for observability:

```go
// Certificate events
c.eventBus.Publish(events.NewWebhookCertificatesGeneratedEvent(validUntil))
c.eventBus.Publish(events.NewWebhookCertificatesRotatedEvent(oldExpiry, newExpiry))

// Server events
c.eventBus.Publish(events.NewWebhookServerStartedEvent(port, path))
c.eventBus.Publish(events.NewWebhookServerStoppedEvent(reason))

// Configuration events
c.eventBus.Publish(events.NewWebhookConfigurationCreatedEvent(name, ruleCount))
```

These events are logged by the commentator and can be used by other components.

## Testing Strategies

### Unit Tests

Test component initialization and configuration:

```go
func TestComponent_New(t *testing.T) {
    component := New(kubeClient, eventBus, logger, Config{
        Namespace:   "test",
        ServiceName: "test-webhook",
    })

    assert.Equal(t, "test", component.config.Namespace)
    assert.Equal(t, 9443, component.config.Port) // Default
    assert.Equal(t, "/validate", component.config.Path) // Default
}
```

### Integration Tests

Test full lifecycle with mock Kubernetes API:

```go
func TestComponent_Lifecycle(t *testing.T) {
    // Create fake Kubernetes client
    kubeClient := fake.NewSimpleClientset()

    component := New(kubeClient, eventBus, logger, testConfig)

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Start component
    errCh := make(chan error, 1)
    go func() {
        errCh <- component.Start(ctx)
    }()

    // Wait for server to start
    time.Sleep(200 * time.Millisecond)

    // Verify webhook configuration created
    configs, err := kubeClient.AdmissionregistrationV1().
        ValidatingWebhookConfigurations().List(ctx, metav1.ListOptions{})
    require.NoError(t, err)
    assert.Len(t, configs.Items, 1)

    // Stop component
    cancel()

    // Verify graceful shutdown
    select {
    case err := <-errCh:
        assert.NoError(t, err)
    case <-time.After(2 * time.Second):
        t.Fatal("component did not stop in time")
    }
}
```

### Event Flow Tests

Verify events are published correctly:

```go
func TestComponent_EventPublishing(t *testing.T) {
    eventBus := busevents.NewEventBus(100)
    eventChan := eventBus.Subscribe(50)
    eventBus.Start()

    component := New(kubeClient, eventBus, logger, testConfig)

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    go component.Start(ctx)

    // Collect events
    var events []busevents.Event
    timeout := time.After(1 * time.Second)

    collecting:
    for {
        select {
        case event := <-eventChan:
            events = append(events, event)

            // Stop after seeing server started
            if _, ok := event.(*events.WebhookServerStartedEvent); ok {
                break collecting
            }

        case <-timeout:
            break collecting
        }
    }

    // Verify event sequence
    assert.Greater(t, len(events), 0)

    // Should see certificates generated
    hasGenerated := false
    for _, e := range events {
        if _, ok := e.(*events.WebhookCertificatesGeneratedEvent); ok {
            hasGenerated = true
        }
    }
    assert.True(t, hasGenerated)
}
```

## Common Pitfalls

### Forgetting to Create Service

**Problem**: ValidatingWebhookConfiguration references service that doesn't exist.

**Solution**: Ensure Kubernetes Service exists before starting webhook component.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: webhook-service
  namespace: default
spec:
  selector:
    app: controller
  ports:
    - port: 443
      targetPort: 9443
```

### Certificate/CA Bundle Mismatch

**Problem**: CA bundle in ValidatingWebhookConfiguration doesn't match CA that signed server cert.

**Solution**: Always use `certificates.CACert` when creating WebhookConfigSpec.

```go
// Good - CA bundle from certificate manager
WebhookConfigSpec{
    CABundle: c.certificates.CACert,  // Same CA that signed server cert
}
```

### Not Handling Rotation Errors

**Problem**: Certificate rotation fails silently, leaving expired certificates.

**Solution**: Log rotation errors and consider alerting/metrics.

```go
if !c.certManager.NeedsRotation(c.certificates) {
    return
}

if err := c.rotateCertificates(ctx); err != nil {
    c.logger.Error("Certificate rotation failed", "error", err)
    // Consider: Increment error metric, trigger alert
}
```

### Blocking in Validator

**Problem**: Validator takes too long, webhook times out.

**Solution**: Keep validators fast (< 1 second). Use timeouts for external calls.

```go
func (c *Component) createValidator(gvk string) webhook.ValidationFunc {
    return func(obj interface{}) (bool, string, error) {
        // Use timeout for validation
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()

        // Validate with timeout
        return c.validate(ctx, gvk, obj)
    }
}
```

## Implementation Status

### Completed (Phase 1)

- ✅ Component structure and lifecycle management
- ✅ Certificate generation and rotation
- ✅ Server start/stop
- ✅ ValidatingWebhookConfiguration management
- ✅ Event publishing (lifecycle events)

### Pending (Phase 2)

- ⏳ Validator bridge to controller validators
- ⏳ Request-response pattern for validation
- ⏳ Detailed validation events (request/allowed/denied/error)
- ⏳ Integration tests with real validators

### Pending (Phase 3)

- ⏳ Prometheus metrics
- ⏳ Debug endpoints
- ⏳ Commentator integration

## Adding Validation Logic

When implementing Phase 2 validator bridge:

```go
func (c *Component) createValidator(gvk string) webhook.ValidationFunc {
    return func(obj interface{}) (bool, string, error) {
        // 1. Create validation request with full context
        // Note: We need AdmissionRequest context (UID, name, namespace, operation)
        // This requires changes to webhook library to pass context to validator

        // 2. Use scatter-gather to collect validator responses
        req := events.NewWebhookValidationRequest(gvk, obj)
        result, err := c.eventBus.Request(ctx, req, busevents.RequestOptions{
            Timeout: 5 * time.Second,
            ExpectedResponders: c.getValidatorsForGVK(gvk),
        })

        // 3. Aggregate results
        if err != nil {
            c.eventBus.Publish(events.NewWebhookValidationErrorEvent(...))
            return false, "", err
        }

        valid, reason := c.aggregateValidationResults(result.Responses)

        // 4. Publish result event
        if valid {
            c.eventBus.Publish(events.NewWebhookValidationAllowedEvent(...))
        } else {
            c.eventBus.Publish(events.NewWebhookValidationDeniedEvent(...))
        }

        return valid, reason, nil
    }
}
```

## Resources

- Pure webhook library: `pkg/webhook/CLAUDE.md`
- Event types: `pkg/controller/events/CLAUDE.md`
- Controller patterns: `pkg/controller/CLAUDE.md`
- Architecture: `/docs/controller/docs/development/design.md`
