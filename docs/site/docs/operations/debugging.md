# Debugging

The controller serves a debug HTTP server that exposes internal state, recent events, and Go profiling. Use it when logs aren't enough — you can see exactly what config is loaded, what it rendered to, and what's happened in the last ~1000 events without having to correlate timestamps.

## Accessing the server

The Helm chart enables the debug server on port `8080` (same port as `/healthz`, same mux). Port-forward to reach it:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 8080:8080
curl http://localhost:8080/debug/vars
```

`/healthz` lives on the same listener. `controller.ports.healthz` is the single
source for the process, container, Service, probes, and NetworkPolicy, so changing
it moves every consumer together. The listener is required by the probes; restrict
access to `/debug/*` with a NetworkPolicy instead of disabling it (see
[Security](./security.md#network-exposure)).

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
| `/debug/vars/pipeline` | Per-phase status keyed `last_trigger`, `rendering`, `validation`, `deployment` (each carries its own status / timestamp / duration / error) — useful for "is reconciliation stuck?" checks. Config-parse failures don't show up here or on `/debug/vars/errors` — check the controller logs and `kubectl get htplcfg … -o yaml` status. |
| `/debug/vars/validated` | Last successful render+validate output (`config`, `timestamp`, `config_bytes`, `validation_duration_ms`) |
| `/debug/vars/errors` | Last error per phase, keyed by `template_render_error` / `haproxy_validation_error` / `deployment_errors`, plus `last_error_timestamp` |
| `/debug/vars/events` | Ring buffer of the most recent controller events |
| `/debug/vars/state` | Aggregate of the above — large; prefer the specific paths for scripting |
| `/debug/vars/uptime` | Process uptime since last reinitialization |

Every endpoint supports JSONPath field selection via `?field={...}`:

```bash
# Current config version
curl 'http://localhost:8080/debug/vars/config?field={.version}'

# Just the rendered haproxy.cfg text
curl 'http://localhost:8080/debug/vars/rendered?field={.config}' | jq -r

# Specific resource type count
curl 'http://localhost:8080/debug/vars/resources?field={.ingresses}'
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

## Go profiling

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

Analyse with `go tool pprof -http=:8081 cpu.pprof`.

!!! note
    `/debug/pprof/block` and `/debug/pprof/mutex` are registered but always return empty profiles: the controller never calls `runtime.SetBlockProfileRate` / `runtime.SetMutexProfileFraction`, so no data is collected. Enabling them requires a custom build that turns on sampling, which carries measurable runtime overhead — do it only for a targeted investigation.

## Common recipes

**Did your config actually load?**

```bash
curl -s 'http://localhost:8080/debug/vars/config?field={.version}'
# empty / error → check `kubectl logs … | grep -i error` and `kubectl get htplcfg -n haptic`
```

**Is the current HAProxy config what you expect?**

```bash
curl -s 'http://localhost:8080/debug/vars/rendered?field={.config}' | jq -r > current.cfg
diff expected.cfg current.cfg
```

Note that `haproxy -c` on the fetched file fails on a workstation: the rendered config sets `default-path origin /etc/haproxy` and references auxiliary files (`maps/*`, `general/*`) that only exist in the HAProxy pod — run the check inside the pod instead.

Or read the last *published* config straight from the `HAProxyCfg` CRD — this works even when the debug port is disabled, and the controller binary decompresses it for you (a raw `kubectl get haproxycfg -o yaml` shows a zstd+base64 blob for configs above the 1 MiB compression threshold; smaller ones are stored as plaintext):

```bash
kubectl exec -n haptic deployment/haptic-controller -- haptic-controller config view > current.cfg
```

**What configuration is the controller actually using?**

A Helm install splits the configuration across one `HAProxyTemplateConfig` per
enabled template library plus one for your own `controller.config`, so no single
object shows the whole picture. `--input` fetches every object named in the
Deployment's `CRD_NAME` and prints the merged spec — the input side, as opposed
to the rendered HAProxy output above:

```bash
kubectl get haproxytemplateconfig -n haptic            # which objects exist
kubectl exec -n haptic deployment/haptic-controller -- \
  haptic-controller config view --input > current-input.yaml
```

If a snippet isn't behaving as you expect, check the controller's startup logs
for `Template snippet overridden by a later config` — it names the snippet and
both objects involved, which is how you find out that a library (or your own
config) is shadowing a definition from an earlier one.

**Why did the last reconciliation fail?**

```bash
curl -s http://localhost:8080/debug/vars/errors | jq '.'
# Inspect just one phase, e.g. semantic validation:
curl -s 'http://localhost:8080/debug/vars/errors?field={.haproxy_validation_error}'
```

The keys (`template_render_error`, `haproxy_validation_error`, `deployment_errors`) tell you which phase rejected the change; pair with `/debug/vars/pipeline` to see whether the controller has retried since.

**Is reconciliation happening?**

```bash
curl -s http://localhost:8080/debug/vars/events \
  | jq '[.[] | select(.type | test("reconciliation|deployment"))] | .[-20:]'
```

If the stream is quiet for minutes even though Ingresses are changing, check `haptic_reconciliation_total` in Prometheus and the per-watcher debounce logs (`pkg/k8s/watcher` — the only debounce layer; the reconciler itself fires immediately).

**Where's memory going?**

```bash
curl http://localhost:8080/debug/pprof/heap > heap.pprof
go tool pprof -top heap.pprof          # biggest retainers
curl http://localhost:8080/debug/vars/resources   # any watched type growing unexpectedly?
```

High counts on a `full`-store resource type are usually the answer; see [Watching Resources](../watching-resources.md) for switching to `on-demand`.

**Why is CPU elevated?**

```bash
curl 'http://localhost:8080/debug/pprof/profile?seconds=30' > cpu.pprof
go tool pprof -top cpu.pprof
# If the hot frames are templating, count recent reconciliation triggers.
# /debug/vars/events returns a JSON list (no wrapper object) so just iterate it.
curl -s http://localhost:8080/debug/vars/events \
  | jq '[.[] | select(.type == "reconciliation.triggered")] | length'
```

More than a few reconciliations per second under stable input usually means a watcher's debounce is undersized for the cluster's resource churn — see [Performance — Reconciliation Tuning](./performance.md#reconciliation-tuning) for the levers.

**Which snippet produced a given config line?**

There's no per-line origin mapping in a running controller. The two production tools stop short of line-level attribution:

- `haptic-controller validate -f config.yaml --trace-templates` lists which templates and snippets rendered and how long each took (render order plus per-template timing), not which output line came from which snippet — see [Performance — Template debugging](./performance.md#template-debugging).
- `/debug/vars/rendered` returns the final `haproxy.cfg` text with no attribution back to the snippets that produced it.

Per-line "this config line came from snippet X" mapping is a feature of the [interactive playground](../templating.md) — its **provenance** control highlights, for a rendered line, the template snippet that emitted it. That mapping runs in the browser and isn't exposed by the in-cluster controller. To trace a line in production, match its content against the snippet names from `--trace-templates` and read that snippet in your `HAProxyTemplateConfig`.

## Security reminders

- `/debug/vars/credentials` returns metadata only — the controller never exposes the actual DataPlane passwords here, the state dump, or any other endpoint.
- `/debug/vars/state` includes the full rendered `haproxy.cfg` (which may reference internal hostnames and backend addresses). Restrict reachability, don't forward the port from CI systems you wouldn't trust with the rendered output.
- See [Security — Network Exposure](./security.md#network-exposure) for a NetworkPolicy pinning the debug port to your observability namespace.

## See also

- [Monitoring](./monitoring.md) — Prometheus-side view of the same signals
- [Troubleshooting](../troubleshooting.md) — symptom → fix table
- [Templating Guide](../templating.md) — for rendering errors surfaced in `/debug/vars/events`
