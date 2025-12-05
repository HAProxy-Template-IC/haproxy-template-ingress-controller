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

```
ReconciliationTriggeredEvent
    ↓
Executor
    ├─→ Publish ReconciliationStartedEvent
    ├─→ (TODO) Orchestrate Renderer
    ├─→ (TODO) Orchestrate Validator
    ├─→ (TODO) Orchestrate Deployer
    └─→ Publish ReconciliationCompletedEvent
```

**Current State**: Minimal stub implementation. Establishes event flow but doesn't yet call pure components.

**Future**: Will orchestrate:

1. Renderer - Generate HAProxy config from templates
2. Validator - Validate generated configuration
3. Deployer - Deploy to HAProxy instances

## Event Flow

```
Reconciler → ReconciliationTriggeredEvent
    ↓
Executor → ReconciliationStartedEvent
    ↓
(TODO) Call Renderer
    ↓
Executor → TemplateRenderedEvent
    ↓
(TODO) Call Validator
    ↓
Executor → ValidationCompletedEvent
    ↓
(TODO) Call Deployer
    ↓
Executor → DeploymentCompletedEvent
    ↓
Executor → ReconciliationCompletedEvent
```

## Resources

- Reconciler: `pkg/controller/reconciler/CLAUDE.md`
- Events: `pkg/controller/events/CLAUDE.md`
