# Debugging

## Overview

The controller provides an introspection HTTP server that exposes health checks, internal state, and Go profiling endpoints. This page covers how to enable and access these endpoints via the Helm chart.

For the full endpoint reference, JSONPath query syntax, and debugging workflows, see the [Debugging Guide](https://haproxy-haptic.org/controller/latest/operations/debugging/) in the controller documentation.

## Introspection HTTP Server

The controller exposes:

- `/healthz` - Health check endpoint used by Kubernetes liveness and readiness probes
- `/debug/vars` - Internal state and runtime variables
- `/debug/pprof` - Go profiling endpoints

The chart enables the server by default on port 8080 (the same port `/healthz` listens on). Set `controller.debugPort: 0` to disable `/debug/*` in production — `/healthz` then moves to `controller.ports.healthz`. Set it to a non-zero value to move the debug surface to a dedicated port.

```yaml
controller:
  debugPort: 8080  # default; 0 disables /debug/*, any other value moves it
```

## Accessing Endpoints

Access introspection endpoints via port-forward:

```bash
# Forward introspection port from controller pod
kubectl port-forward -n haptic deployment/haptic-controller 8080:8080

# Check health status
curl http://localhost:8080/healthz

# List all available debug variables
curl http://localhost:8080/debug/vars

# Get current controller configuration
curl http://localhost:8080/debug/vars/config

# Get rendered HAProxy configuration
curl http://localhost:8080/debug/vars/rendered

# Get recent events (last 100)
curl http://localhost:8080/debug/vars/events

# Get resource counts
curl http://localhost:8080/debug/vars/resources

# Go profiling (CPU, heap, goroutines)
curl http://localhost:8080/debug/pprof/
go tool pprof http://localhost:8080/debug/pprof/heap
```

## Debug Variables

Available debug variables:

| Endpoint | Description |
|----------|-------------|
| `/debug/vars` | List all available variables |
| `/debug/vars/config` | Current controller configuration |
| `/debug/vars/credentials` | Credentials metadata (not actual values) |
| `/debug/vars/rendered` | Last rendered HAProxy config |
| `/debug/vars/auxfiles` | Auxiliary files (SSL certs, maps) |
| `/debug/vars/resources` | Resource counts by type |
| `/debug/vars/events` | Recent events (default: last 100) |
| `/debug/vars/pipeline` | Per-phase reconciliation status (`config_parse`, `validation`, `deployment`) |
| `/debug/vars/validated` | Last successfully validated HAProxy config |
| `/debug/vars/errors` | Aggregated error summary keyed by phase |
| `/debug/vars/state` | Full state dump (use carefully) |
| `/debug/vars/uptime` | Controller uptime |
| `/debug/events` | Event ring buffer with `?limit=N` and `?correlation_id=<id>` query params (separate from `/debug/vars/events`) |
| `/debug/pprof/` | Go profiling endpoints |

## JSONPath Field Selection

Extract specific fields using JSONPath:

```bash
# Get only the config version
curl 'http://localhost:8080/debug/vars/config?field={.version}'

# Get only template names
curl 'http://localhost:8080/debug/vars/config?field={.config.templates}'

# Get rendered config size
curl 'http://localhost:8080/debug/vars/rendered?field={.size}'
```
