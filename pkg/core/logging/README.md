# pkg/core/logging

Thin wrapper around `log/slog` that the controller's entry points use to build the root logger. Emits logfmt by default. No JSON-output switch — everything reads through `kubectl logs` or a log aggregator that handles logfmt fine.

## API

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/core/logging"

// Static: level set once at construction.
logger := logging.NewLogger("INFO")

// Dynamic: level stored in a package-global slog.LevelVar that SetLevel()
// updates at runtime. The controller uses this so the CRD's
// logging.level field can change verbosity without a pod restart.
logger := logging.NewDynamicLogger("INFO")

logging.SetLevel("DEBUG")      // updates the dynamic level
current := logging.GetLevel()  // "ERROR" | "WARNING" | "INFO" | "DEBUG" | "TRACE"
```

Level parsing is case-insensitive. Empty or unknown strings fall back to `INFO`.

`TRACE` is not a native slog level; this package maps it to `slog.Level(-8)` (below `DEBUG`), matching what the rest of the controller passes through to filter-debug / per-resource-iteration logging.

## Runtime Rewiring

The controller wires up a `NewDynamicLogger` at startup using the `LOG_LEVEL` environment variable, then calls `SetLevel` from the `configloader` when a new `HAProxyTemplateConfig` CRD arrives with a non-empty `spec.logging.level`. The CRD value wins over `LOG_LEVEL` once it has been successfully loaded. See `docs/controller/docs/troubleshooting.md#enable-debug-logging` for the user-facing view.

## Log-Line Style

This package doesn't enforce anything beyond "logfmt via slog". The conventions the codebase follows (lowercase messages, structured `key=value` attributes, component-tagged child loggers) live in `pkg/core/CLAUDE.md`.

## See Also

- `pkg/core/CLAUDE.md` — log-line style conventions and examples
- `docs/controller/docs/troubleshooting.md` — operator view (`LOG_LEVEL` env var + CRD `logging.level` field)
- [`log/slog`](https://pkg.go.dev/log/slog) — upstream standard library

## License

Apache-2.0 — see root `LICENSE`.
