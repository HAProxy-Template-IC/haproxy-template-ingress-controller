# pkg/controller/reconciler - Reconciliation Debouncer

Development context for the Reconciler component.

## When to Work Here

Work in this package when:

- Modifying debounce logic
- Changing reconciliation triggering behavior
- Adjusting debounce interval
- Adding reconciliation triggers

**DO NOT** work here for:

- Reconciliation orchestration → Use `pkg/controller/executor`
- Template rendering → Use `pkg/controller/renderer`
- Deployment → Use `pkg/controller/deployer`

## Package Purpose

Stage 5 component that debounces resource changes and triggers reconciliation events. Prevents excessive reconciliations by waiting for quiet periods.

## Architecture

```
ResourceIndexUpdatedEvent → Debounce Timer (500ms default)
ConfigValidatedEvent → Immediate Trigger

    ↓
ReconciliationTriggeredEvent → Executor
```

## Debounce Behavior

### Resource Changes (Debounced)

```
t=0:    Resource change → Reset timer to 500ms
t=100:  Resource change → Reset timer to 500ms
t=300:  Resource change → Reset timer to 500ms
t=800:  Timer expires → Trigger reconciliation
```

Multiple rapid changes batched into single reconciliation.

### Config Changes (Immediate)

```
ConfigValidatedEvent → Immediate ReconciliationTriggeredEvent
```

No debouncing for configuration changes - apply immediately.

## Configuration

```go
reconciler := reconciler.New(bus, logger, &reconciler.Config{
    DebounceInterval: 500 * time.Millisecond,  // Customizable
})
```

## Common Patterns

### Default Configuration

```go
// Uses DefaultDebounceInterval (500ms)
reconciler := reconciler.New(bus, logger, nil)
go reconciler.Start(ctx)
```

### Custom Debounce

```go
// Longer debounce for high-volume environments
reconciler := reconciler.New(bus, logger, &reconciler.Config{
    DebounceInterval: 2 * time.Second,
})
```

## Common Pitfalls

### Too Short Debounce

**Problem**: Reconciliation triggered too frequently, wasting resources.

**Solution**: Increase debounce interval (default 500ms is usually good).

### Too Long Debounce

**Problem**: Slow to react to changes.

**Solution**: Decrease debounce interval or handle critical changes immediately.

## Integration

Controller creates Reconciler in Stage 5:

```go
// Stage 5: Reconciliation
reconciler := reconciler.New(bus, logger, nil)
go reconciler.Start(ctx)
```

## Resources

- Executor: `pkg/controller/executor/CLAUDE.md`
- Events: `pkg/controller/events/CLAUDE.md`
