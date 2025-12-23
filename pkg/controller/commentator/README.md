# pkg/controller/commentator

Event Commentator - domain-aware event logging with correlation.

## Overview

Observability component that subscribes to all controller events and produces structured logs with domain insights and event correlation.

## Features

- **Domain-aware logging**: Adds business context to events
- **Event correlation**: Uses ring buffer to relate events temporally
- **Automatic log levels**: Error, Warn, Info, Debug based on event type
- **Ring buffer**: Stores recent events for correlation (default: 1000)

## Quick Start

```go
import "haptic/pkg/controller/commentator"

commentator := commentator.NewEventCommentator(bus, logger, 1000)
go commentator.Start(ctx)
```

## Example Logs

```
INFO  Configuration validated successfully version=12345 templates=2
DEBUG Resource index updated type=ingresses added=5 updated=2 deleted=1
INFO  Reconciliation started trigger=config_change since_last=5.2s
INFO  Reconciliation completed duration_ms=1234
ERROR Deployment failed instance=haproxy-1 error="connection refused"
```

## Ring Buffer

Stores last N events for correlation:

```go
// Find recent event of specific type
lastRecon := commentator.ringBuffer.FindLast("reconciliation.started")

// Calculate time since last occurrence
timeSince := currentEvent.Timestamp.Sub(lastRecon.Timestamp)
```

## License

See main repository for license information.
