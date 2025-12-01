# Debugging Guide

This guide explains how to debug and troubleshoot the HAProxy Template Ingress Controller using built-in introspection and debugging tools.

## Overview

The controller provides a debug HTTP server that exposes internal state, event history, and Go profiling endpoints. These tools help you understand controller behavior without requiring log analysis.

**Key features:**
- Real-time controller state inspection
- Event history for tracking controller activity
- JSONPath field selection for targeted queries
- Go profiling for performance analysis

## Enabling Debug Endpoints

The debug HTTP server is configured via Helm values:

```yaml
# values.yaml
controller:
  debugPort: 6060  # Set to 0 to disable
```

By default, the debug port is **6060**. The server binds to all interfaces (`0.0.0.0`) for kubectl port-forward compatibility.

## Accessing Debug Endpoints

### From Outside the Cluster

Use kubectl port-forward to access the debug server:

```bash
# Forward debug port from controller pod
kubectl port-forward -n <namespace> deployment/haproxy-template-ic 6060:6060

# Access endpoints
curl http://localhost:6060/debug/vars
```

### From Inside the Cluster

Access directly via pod IP or service:

```bash
# Get pod IP
POD_IP=$(kubectl get pod -n <namespace> <pod-name> -o jsonpath='{.status.podIP}')
curl http://${POD_IP}:6060/debug/vars
```

## Debug Variables

### List All Variables

```bash
curl http://localhost:6060/debug/vars
```

Returns a list of all available debug variable paths:

```json
{
  "vars": [
    "config",
    "credentials",
    "rendered",
    "auxfiles",
    "resources",
    "events",
    "state",
    "uptime"
  ]
}
```

### Configuration State

Get the current controller configuration:

```bash
# Full configuration
curl http://localhost:6060/debug/vars/config

# Just the version
curl 'http://localhost:6060/debug/vars/config?field={.version}'

# Template names
curl 'http://localhost:6060/debug/vars/config?field={.config.templates}'
```

**Response:**
```json
{
  "config": {
    "templates": {
      "main": "global\n  maxconn {{ maxconn }}\n..."
    },
    "watched_resources": [...]
  },
  "version": "12345",
  "updated": "2025-01-15T10:30:45Z"
}
```

### Rendered HAProxy Config

Get the most recently rendered HAProxy configuration:

```bash
# Full rendered config
curl http://localhost:6060/debug/vars/rendered

# Just the config text (useful for saving to file)
curl 'http://localhost:6060/debug/vars/rendered?field={.config}' | jq -r '.'

# Config size and timestamp
curl 'http://localhost:6060/debug/vars/rendered?field={.size}'
curl 'http://localhost:6060/debug/vars/rendered?field={.timestamp}'
```

**Response:**
```json
{
  "config": "global\n  maxconn 2000\n  log stdout local0\n\ndefaults\n...",
  "timestamp": "2025-01-15T10:30:45Z",
  "size": 4567
}
```

### Resource Counts

Get counts of watched Kubernetes resources:

```bash
# All resource counts
curl http://localhost:6060/debug/vars/resources

# Specific resource type
curl 'http://localhost:6060/debug/vars/resources?field={.ingresses}'
```

**Response:**
```json
{
  "ingresses": 5,
  "services": 12,
  "haproxy-pods": 2
}
```

### Auxiliary Files

Get SSL certificates, map files, and general files used in the last deployment:

```bash
curl http://localhost:6060/debug/vars/auxfiles
```

**Response:**
```json
{
  "files": {
    "ssl_certificates": [
      {
        "name": "tls-cert",
        "path": "/etc/haproxy/ssl/tls-cert.pem"
      }
    ],
    "map_files": [...],
    "general_files": [...]
  },
  "timestamp": "2025-01-15T10:30:45Z",
  "summary": {
    "ssl_count": 2,
    "map_count": 1,
    "general_count": 3
  }
}
```

### Event History

Get recent controller events:

```bash
# Last 100 events (default)
curl http://localhost:6060/debug/vars/events
```

**Response:**
```json
[
  {
    "timestamp": "2025-01-15T10:30:45Z",
    "type": "config.validated",
    "summary": "config.validated"
  },
  {
    "timestamp": "2025-01-15T10:30:46Z",
    "type": "reconciliation.triggered",
    "summary": "reconciliation.triggered"
  }
]
```

### Full State Dump

Get all controller state in a single response:

```bash
curl http://localhost:6060/debug/vars/state
```

!!! warning
    The full state dump can return very large responses. Prefer specific variables or JSONPath field selection for production debugging.

### Controller Uptime

```bash
curl http://localhost:6060/debug/vars/uptime
```

## JSONPath Field Selection

All debug endpoints support JSONPath field selection using kubectl-style syntax:

```bash
# Basic field access
curl 'http://localhost:6060/debug/vars/config?field={.version}'

# Nested field access
curl 'http://localhost:6060/debug/vars/config?field={.config.templates.main}'

# Array indexing
curl 'http://localhost:6060/debug/vars/events?field={[0]}'
```

See [Kubernetes JSONPath documentation](https://kubernetes.io/docs/reference/kubectl/jsonpath/) for full syntax.

## Go Profiling

The debug server includes Go pprof endpoints for performance analysis:

```bash
# CPU profile (30 second sample)
curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.pprof

# Heap profile
curl http://localhost:6060/debug/pprof/heap > heap.pprof

# Goroutine dump
curl http://localhost:6060/debug/pprof/goroutine?debug=1

# Block profile
curl http://localhost:6060/debug/pprof/block > block.pprof

# Mutex profile
curl http://localhost:6060/debug/pprof/mutex > mutex.pprof

# All profiles summary
curl http://localhost:6060/debug/pprof/
```

Analyze profiles with `go tool pprof`:

```bash
go tool pprof -http=:8080 cpu.pprof
```

## Common Debugging Workflows

### Configuration Not Loading

1. Check if config is loaded:
   ```bash
   curl http://localhost:6060/debug/vars/config
   ```

2. If error "config not loaded yet", check controller logs for parsing errors:
   ```bash
   kubectl logs -n <namespace> deployment/haproxy-template-ic | grep -i error
   ```

3. Verify HAProxyTemplateConfig exists:
   ```bash
   kubectl get haproxytemplateconfig -n <namespace>
   ```

### HAProxy Config Not Updating

1. Check rendered config timestamp:
   ```bash
   curl 'http://localhost:6060/debug/vars/rendered?field={.timestamp}'
   ```

2. Check recent events for reconciliation activity:
   ```bash
   curl http://localhost:6060/debug/vars/events | jq '.[] | select(.type | contains("reconciliation"))'
   ```

3. Verify resource counts match expected:
   ```bash
   curl http://localhost:6060/debug/vars/resources
   ```

### Template Rendering Errors

1. Check if config is valid:
   ```bash
   curl 'http://localhost:6060/debug/vars/config?field={.version}'
   ```

2. Look for template-related events:
   ```bash
   curl http://localhost:6060/debug/vars/events | jq '.[] | select(.type | contains("template"))'
   ```

3. Try rendering with debug output (see [Templating Guide](../templating.md))

### Memory Issues

1. Get current heap usage:
   ```bash
   curl http://localhost:6060/debug/pprof/heap?debug=1 | head -20
   ```

2. Generate heap profile for analysis:
   ```bash
   curl http://localhost:6060/debug/pprof/heap > heap.pprof
   go tool pprof -top heap.pprof
   ```

3. Check resource counts (large counts may indicate memory pressure):
   ```bash
   curl http://localhost:6060/debug/vars/resources
   ```

### High CPU Usage

1. Generate CPU profile:
   ```bash
   curl http://localhost:6060/debug/pprof/profile?seconds=30 > cpu.pprof
   ```

2. Analyze hot spots:
   ```bash
   go tool pprof -top cpu.pprof
   ```

3. Check reconciliation frequency in events:
   ```bash
   curl http://localhost:6060/debug/vars/events | jq '[.[] | select(.type == "reconciliation.triggered")] | length'
   ```

### Deployment Failures

1. Check recent events for deployment status:
   ```bash
   curl http://localhost:6060/debug/vars/events | jq '.[] | select(.type | contains("deployment"))'
   ```

2. Verify HAProxy pods are discovered:
   ```bash
   curl 'http://localhost:6060/debug/vars/resources?field={."haproxy-pods"}'
   ```

3. Check rendered config for syntax errors:
   ```bash
   curl 'http://localhost:6060/debug/vars/rendered?field={.config}' > haproxy.cfg
   haproxy -c -f haproxy.cfg
   ```

## Security Considerations

1. **Credentials**: The `/debug/vars/credentials` endpoint exposes only metadata, NOT actual passwords

2. **Access Control**: Debug endpoints have no built-in authentication - use kubectl port-forward or network policies to restrict access

3. **Large Responses**: Full state dump can expose sensitive configuration - use specific variables instead

4. **Production Usage**: Consider disabling debug port in high-security environments:
   ```yaml
   controller:
     debugPort: 0  # Disabled
   ```

## Scripting Examples

### Monitor Reconciliation Activity

```bash
#!/bin/bash
# Watch reconciliation events
while true; do
    count=$(curl -s http://localhost:6060/debug/vars/events | \
            jq '[.[] | select(.type == "reconciliation.completed")] | length')
    echo "$(date): $count reconciliations completed"
    sleep 10
done
```

### Export Rendered Config

```bash
#!/bin/bash
# Export current HAProxy config with timestamp
TIMESTAMP=$(curl -s 'http://localhost:6060/debug/vars/rendered?field={.timestamp}' | jq -r '.')
curl -s 'http://localhost:6060/debug/vars/rendered?field={.config}' | jq -r '.' > "haproxy-${TIMESTAMP}.cfg"
echo "Exported to haproxy-${TIMESTAMP}.cfg"
```

### Health Check Script

```bash
#!/bin/bash
# Check controller health via debug endpoints
set -e

CONFIG_VERSION=$(curl -s 'http://localhost:6060/debug/vars/config?field={.version}' 2>/dev/null || echo "error")
UPTIME=$(curl -s http://localhost:6060/debug/vars/uptime 2>/dev/null || echo "error")
RESOURCES=$(curl -s http://localhost:6060/debug/vars/resources 2>/dev/null || echo "{}")

echo "Config Version: $CONFIG_VERSION"
echo "Uptime: $UPTIME"
echo "Resources: $RESOURCES"

if [ "$CONFIG_VERSION" = "error" ]; then
    echo "ERROR: Cannot access debug endpoints"
    exit 1
fi
```

## See Also

- [Monitoring Guide](./monitoring.md) - Prometheus metrics and alerting
- [High Availability](./high-availability.md) - Leader election and failover
- [Troubleshooting Guide](../troubleshooting.md) - General troubleshooting
- [Templating Guide](../templating.md) - Template debugging
