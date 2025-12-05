# pkg/controller/executor

Executor component - orchestrates reconciliation cycles.

## Overview

Stage 5 component that coordinates Renderer, Validator, and Deployer components via event subscriptions. It observes the reconciliation pipeline and publishes lifecycle events for observability.

## Quick Start

```go
import "haproxy-template-ic/pkg/controller/executor"

executor := executor.New(bus, logger)
go executor.Start(ctx)
```

## Events

### Subscribes To

- **ReconciliationTriggeredEvent**: Starts reconciliation cycle
- **TemplateRenderedEvent**: Template rendering completed successfully
- **TemplateRenderFailedEvent**: Template rendering failed
- **ValidationCompletedEvent**: Configuration validation passed
- **ValidationFailedEvent**: Configuration validation failed

### Publishes

- **ReconciliationStartedEvent**: Reconciliation cycle begins
- **ReconciliationCompletedEvent**: Reconciliation cycle completes successfully
- **ReconciliationFailedEvent**: Reconciliation cycle failed at render or validation stage

## License

See main repository for license information.
