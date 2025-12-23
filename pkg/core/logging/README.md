# pkg/core/logging

Structured logging setup using Go's standard log/slog package.

## Overview

Provides utilities for initializing and configuring structured logging throughout the controller.

## Installation

```go
import "haptic/pkg/core/logging"
```

## Quick Start

```go
import (
    "log/slog"
    "haptic/pkg/core/logging"
)

// Initialize logger
logger := logging.New(logging.Config{
    Level:  slog.LevelInfo,
    Format: logging.FormatJSON,
})

slog.SetDefault(logger)

// Use throughout application
slog.Info("controller started",
    "namespace", namespace,
    "watched_resources", len(watchedResources))
```

## Log Levels

- **LevelDebug**: Verbose diagnostic information
- **LevelInfo**: General operational information
- **LevelWarn**: Non-critical issues
- **LevelError**: Error conditions

## Structured Logging

```go
// Good - structured attributes
slog.Info("reconciliation completed",
    "duration_ms", duration.Milliseconds(),
    "resources_processed", count)

// Avoid - unstructured string formatting
slog.Info(fmt.Sprintf("Reconciliation completed in %dms", duration.Milliseconds()))
```

## Context Logger

```go
// Create logger with context
logger := slog.Default().With(
    "component", "reconciler",
    "namespace", namespace,
)

logger.Info("starting")  // Includes component=reconciler
```

## License

See main repository for license information.
