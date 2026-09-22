# Controller debug endpoints

Use these endpoints when investigating controller behavior or collecting a
profile for a bug report. For installation and routing problems, start with
[fleet diagnostics](../operations/diagnostics.md).

## Accessing the server

The chart serves diagnostics and health checks on port `8080`. Use port forwarding
to reach the debug endpoints:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 8080:8080
```

In another terminal:

```bash
curl http://localhost:8080/debug/vars
```

Debug endpoints accept loopback connections only. Use `kubectl port-forward`
and limit that permission to trusted operators: the output can include rendered
configuration and Secret contents. Health probes remain reachable by Kubernetes.

## Debug variables

`GET /debug/vars` lists the available paths; `GET /debug/vars/<name>` fetches one:

| Path | What you get |
|------|--------------|
| `/debug/vars` | Listing of available names |
| `/debug/vars/config` | Parsed `HAProxyTemplateConfig` and its version (`updated` is the request time, not the load time) |
| `/debug/vars/credentials` | Metadata only (`version`, `has_dataplane_creds`) — **never** the passwords |
| `/debug/vars/rendered` | Last rendered `haproxy.cfg`, its size, and timestamp |
| `/debug/vars/auxfiles` | Last rendered SSL certs, map files, general files + a summary count |
| `/debug/vars/resources` | Per-type counts for every `watchedResources` entry |
| `/debug/vars/effectiveConfigResolution` | How each `apiVersions` candidate list resolved against what the cluster actually serves, and which optional entries were dropped — the first thing to check when a `resources.<name>` lookup is unexpectedly empty |
| `/debug/vars/pipeline` | Per-phase status keyed `last_trigger`, `rendering`, `validation`, `deployment` (each carries its own status / timestamp / duration / error) — useful for "is reconciliation stuck?" checks. Config-parse failures don't show up here or on `/debug/vars/errors` — check the controller logs and `kubectl get htplcfg … -o yaml` status. |
| `/debug/vars/validated` | Last successful render+validate output (`config`, `timestamp`, `config_bytes`, `validation_duration_ms`) |
| `/debug/vars/errors` | Last error per phase, keyed by `template_render_error` / `haproxy_validation_error` / `deployment_errors`, plus `last_error_timestamp` |
| `/debug/vars/events` | Ring buffer of the most recent controller events |
| `/debug/vars/all` | Every registered variable in one path-keyed object — like `state`, but built from the registry, and just as large |
| `/debug/vars/state` | Aggregate of the above — large; prefer the specific paths for scripting |
| `/debug/vars/uptime` | Process uptime since last reinitialization |

Every endpoint supports JSONPath field selection via `?field={...}`:

```bash
# Current config version
curl --globoff 'http://localhost:8080/debug/vars/config?field={.version}'

# Just the rendered haproxy.cfg text
curl --globoff 'http://localhost:8080/debug/vars/rendered?field={.config}' | jq -r

# Specific resource type count
curl --globoff 'http://localhost:8080/debug/vars/resources?field={.ingresses}'
```

The syntax is the same as `kubectl get -o jsonpath='{…}'`; see the [Kubernetes JSONPath reference](https://kubernetes.io/docs/reference/kubectl/jsonpath/).

## Event Search (`/debug/events`)

`/debug/events` is a separate endpoint (not under `/debug/vars/`) for querying the event ring buffer. Useful when chasing a specific reconciliation by `correlation_id`:

```bash
# Last 100 events (default limit)
curl http://localhost:8080/debug/events

# Last 500 events
curl 'http://localhost:8080/debug/events?limit=500'

# All events that share a correlation ID — every event in one reconciliation
curl 'http://localhost:8080/debug/events?correlation_id=<id>'
```

Pull a `correlation_id` out of `/debug/vars/events` (reconciliation-, render-, validation-, and deployment-related entries expose one; lifecycle and resource-index events don't) or out of structured logs, then use it here to fetch every related event in order.

## Health checks during configuration changes

`/healthz` lives on the same listener. Set `controller.ports.healthz` to change its port; the chart updates the Service,
probes, and NetworkPolicy to match. Keep the listener enabled so Kubernetes can
check the controller's health.

During reinitialization, the health check allows up to 165 seconds for the new
configuration to load. A failure that persists beyond this window returns HTTP
503. Repeated failures don't extend the window. Check controller logs and
`HAProxyTemplateConfig` status when a change leaves the controller unhealthy.

## Performance profiles

`/debug/pprof/*` is the standard `net/http/pprof` handler:

```bash
# CPU profile (30s sample)
curl http://localhost:8080/debug/pprof/profile?seconds=30 > cpu.pprof

# Heap snapshot
curl http://localhost:8080/debug/pprof/heap > heap.pprof

# Goroutine dump (human-readable)
curl 'http://localhost:8080/debug/pprof/goroutine?debug=1'

# All profiles + docs
curl http://localhost:8080/debug/pprof/
```

Analyse with `go tool pprof -http=127.0.0.1:8081 cpu.pprof`.

!!! note
    `/debug/pprof/block` and `/debug/pprof/mutex` are registered but always return empty profiles: the controller never calls `runtime.SetBlockProfileRate` / `runtime.SetMutexProfileFraction`, so no data is collected. Enabling them requires a custom build that turns on sampling, which carries measurable runtime overhead — do it only for a targeted investigation.
