# pkg/controller/executor - Reconciliation Orchestrator

Development context for the Executor component.

## When to Work Here

Work in this package when:

- Implementing reconciliation orchestration logic
- Coordinating Renderer, Validator, Deployer components
- Adding reconciliation stages
- Modifying error handling during reconciliation

**DO NOT** work here for:

- Template rendering → Use Renderer component
- Configuration validation → Use Validator component
- HAProxy deployment → Use Deployer component

## Package Purpose

Stage 5 component that orchestrates reconciliation cycles. Coordinates pure components (Renderer, Validator, Deployer) via events.

## Architecture

The Executor coordinates the reconciliation pipeline via event subscriptions. It subscribes to events from upstream components (Reconciler, Renderer, Validator) and publishes events for observability.

```
ReconciliationTriggeredEvent
    ↓
Executor
    ├─→ Publish ReconciliationStartedEvent
    ├─→ Subscribe to TemplateRenderedEvent / TemplateRenderFailedEvent
    ├─→ Subscribe to ValidationCompletedEvent / ValidationFailedEvent
    └─→ Publish ReconciliationCompletedEvent / ReconciliationFailedEvent
```

## Event Flow

The Executor observes the reconciliation pipeline and publishes lifecycle events:

```
Reconciler → ReconciliationTriggeredEvent
    ↓
Executor → Publishes ReconciliationStartedEvent
    ↓
Renderer → TemplateRenderedEvent (or TemplateRenderFailedEvent)
    ↓
Executor → Logs rendering result, publishes ReconciliationFailedEvent on failure
    ↓
Validator → ValidationCompletedEvent (or ValidationFailedEvent)
    ↓
Executor → Logs validation result, publishes ReconciliationFailedEvent on failure
    ↓
Deployer → DeploymentCompletedEvent
    ↓
Executor → Publishes ReconciliationCompletedEvent
```

Components coordinate via events - the Executor observes and reports on the pipeline rather than directly calling components.

## Resources

- Reconciler: `pkg/controller/reconciler/CLAUDE.md`
- Events: `pkg/controller/events/CLAUDE.md`
