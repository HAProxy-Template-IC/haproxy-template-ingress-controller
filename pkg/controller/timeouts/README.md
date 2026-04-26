# pkg/controller/timeouts

Shared timeout constants used across controller subpackages.

## Overview

Centralising these prevents the same magic numbers from drifting apart across components. Components that need an unusual timeout should still pick a value appropriate to the operation, but everything that's "the standard Kubernetes API timeout" or "the standard ticker poll interval" reads from here so a tuning change is one edit.

## Constants

| Name | Value | Use for |
|------|-------|---------|
| `KubernetesAPITimeout` | `10 * time.Second` | Standard k8s API calls (status updates, resource cleanup) |
| `KubernetesAPILongTimeout` | `30 * time.Second` | Longer k8s API operations (publishing config, reconciling pod status) |
| `HTTPServerTimeout` | `10 * time.Second` | Read/write timeout for HTTP servers (webhook admission server) |
| `InformerResyncPeriod` | `30 * time.Second` | Resync interval for shared informers |
| `TickerPollInterval` | `5 * time.Second` | Periodic polling (metrics collection, deployment timeout checks) |
| `GracefulStopDelay` | `100 * time.Millisecond` | Brief pause after stopping components to drain in-flight work |

These are deliberately Go constants rather than CRD fields — they're tuning knobs that should travel with the code, not with the deployment.

## License

Apache-2.0 — see root `LICENSE`.
