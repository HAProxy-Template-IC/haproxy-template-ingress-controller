# HAProxy Deployment

## Overview

The chart can deploy HAProxy pods alongside the controller, or you can manage HAProxy separately.

## Resource limits

Controller-pod sizing — the chart's request/limit defaults, the sizing table, and the GOMAXPROCS/GOMEMLIMIT container awareness — is covered in [Performance — Controller Resource Sizing](operations/performance.md#controller-resource-sizing). HAProxy and the Dataplane API sidecar have their own resource blocks in the chart values: `haproxy.resources` and `haproxy.dataplane.resources`.

## Service Architecture

The chart deploys separate Services for the controller and HAProxy so data-plane traffic and operational endpoints never cross. The controller Service is for cluster-internal monitoring only; the HAProxy Service is what external traffic hits.

### Controller Service

A single `ClusterIP` Service named after the chart's `fullname` (for example `<release>-haptic`) that exposes the controller's ports defined in `controller.ports`:

| Name | Container port | Values key | Purpose |
|------|----------------|------------|---------|
| `healthz` | 8080 | `controller.ports.healthz` | Single source for the process listener, liveness/readiness probes, Service, and `/debug/*` introspection endpoints |
| `metrics` | 9090 | `controller.ports.metrics` | Single source for the process listener, Service, and Prometheus monitors; `0` disables metrics |
| `webhook` | 9443 | `controller.ports.webhook` | Admission-webhook HTTPS endpoint |

Override Service type, annotations, etc. under the `controller.service` block:

```yaml
controller:
  service:
    type: ClusterIP
    annotations: {}
```

### HAProxy Service

A Service (`<fullname>-haproxy`, for example `<release>-haptic-haproxy`, `NodePort` by default) that fronts the HAProxy pods. Port structure comes from `haproxy.service.*` and container ports from `haproxy.ports.*`:

| Name | Service port | Container port | nodePort default |
|------|--------------|----------------|------------------|
| `http` | 80 | 80 | 30080 |
| `https` | 443 | 443 | 30443 |
| `stats` | 8404 | 8404 | 30404 |

The Dataplane API sidecar gets its own internal-only `ClusterIP` Service (`<fullname>-haproxy-dataplane`, for example `<release>-haptic-haproxy-dataplane`) on port 5555. Its type comes from `haproxy.dataplane.service.type`.

**Development (kind cluster)** — NodePort default works out of the box; switch to LoadBalancer if you want `localhost` mapping via kind's port-forward:

```yaml
haproxy:
  service:
    type: LoadBalancer
```

**Cloud provider LoadBalancer**:

```yaml
haproxy:
  service:
    type: LoadBalancer
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
```

**External / self-managed HAProxy** — turn off the chart's HAProxy deployment and manage pods yourself (see [HAProxy Pod Requirements](#haproxy-pod-requirements)):

```yaml
haproxy:
  enabled: false
```

### PROXY protocol

Behind a layer-4 load balancer — an edge HAProxy, a cloud network load balancer,
a firewall that port-forwards and rewrites the source address — HAProxy sees the
load balancer as the client. Every request then logs the same `client_ip`,
IP-keyed rate limiting shares one bucket across the internet, and the WAF and any
IP-based access control list see a single client.

The load balancer fixes this by adding a PROXY protocol header that carries the
original address. Enable the matching listeners:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        proxyProtocol:
          enabled: true
          httpPort: 8081
          httpsPort: 8444
```

That adds two binds, adds them to the HAProxy Service and the NetworkPolicy, and
leaves `haproxy.ports.http` / `haproxy.ports.https` exactly as they were. Point
the balancer at the new ports:

```haproxy
# On the upstream load balancer
server k8s-https 10.0.0.50:8444 send-proxy-v2
```

Requests arriving on the PROXY ports carry the real client through the access
log's `client_ip`, `src`-keyed rate limiting, the WAF, and IP access control
lists. Terminated HTTPS on `httpsPort` uses the same certificates, ciphers, and
protocol negotiation as the plain HTTPS bind; with TLS-Passthrough configured,
`httpsPort` attaches to the SNI-routing frontend instead so passthrough hosts
keep working.

!!! warning "Send the header, or the connection is dropped"
    HAProxy has no "PROXY header optional" mode. A connection reaching
    `httpPort` or `httpsPort` without the header is rejected, so only the
    upstream balancer may target these ports. Everything else — direct access,
    in-cluster clients, NodePort traffic, probes — keeps using the regular
    `haproxy.ports.http` / `haproxy.ports.https`, which is why these are
    additional ports rather than a flag on the existing ones.

This is separate from the
[`haproxy-haptic.org/proxy-protocol`](libraries/haptic-annotations.md)
annotation, which makes HAProxy *send* a PROXY header to a backend.

### Full HAProxy Service reference

```yaml
haproxy:
  enabled: true
  ports:
    http: 80         # HAProxy container HTTP bind
    https: 443       # HAProxy container HTTPS bind
    stats: 8404      # Stats/health page
    dataplane: 5555  # Dataplane API
  service:
    type: NodePort   # ClusterIP, NodePort, or LoadBalancer
    annotations: {}
    loadBalancerIP: ""
    loadBalancerSourceRanges: []
    externalTrafficPolicy: ""   # Cluster | Local
    http:
      port: 80
      nodePort: 30080           # Only honored for NodePort/LoadBalancer
    https:
      port: 443
      nodePort: 30443
    stats:
      port: 8404
      nodePort: 30404
```

## Replicas and autoscaling

The chart runs 2 HAProxy replicas by default. Set `haproxy.replicaCount` to change the fixed count:

```yaml
haproxy:
  replicaCount: 3
```

For traffic-driven autoscaling, enable [KEDA](https://keda.sh/) under `haproxy.keda`. When `haproxy.keda.enabled` is true, the chart creates a `ScaledObject` and stops writing a fixed `replicas` onto the Deployment (KEDA owns it), scaling between `minReplicaCount` and `maxReplicaCount` from the triggers you define:

```yaml
haproxy:
  keda:
    enabled: true
    minReplicaCount: 2
    maxReplicaCount: 10
    triggers:
      - type: cpu
        metricType: Utilization
        metadata:
          value: "70"
```

KEDA must be installed in the cluster, and `haproxy.keda.triggers` must list at least one trigger — it's empty by default. Any [KEDA scaler](https://keda.sh/docs/latest/scalers/) works; the block above uses CPU utilization.

## Initial bootstrap config

When the chart manages HAProxy, the pod boots with a minimal `haproxy.cfg` rendered from `haproxy.initialConfig` into the `<release>-haptic-haproxy-config` ConfigMap. The controller replaces this config via the Dataplane API on its first reconcile, so the bootstrap only matters during the seconds between pod start and controller handoff (and on pod restart before the controller reconciles again).

The default keeps `/healthz` returning 200 on the stats port and `/ready` returning 503 ("waiting for controller config"), so the pod stays NotReady until the controller pushes its first real config. To customise (for example, to add cluster-internal ACLs, an extra logging directive, or pre-bind a port the controller doesn't manage), copy the default from `values.yaml` into your own values file and edit it:

```yaml
haproxy:
  initialConfig: |
    global
        log stdout len 4096 local0 info
        {{- with include "haptic.haproxy.nbthread" . }}
        nbthread {{ . }}
        {{- end }}
    defaults
        mode http
        timeout connect 5s
    frontend status
        bind *:{{ .Values.haproxy.ports.stats }}
        http-request return status 200 content-type text/plain string "OK" if { path /healthz }
        http-request return status 503 content-type text/plain string "Not ready" if { path /ready }
    frontend http_frontend
        bind *:{{ .Values.haproxy.ports.http }}
        default_backend default_backend
    backend default_backend
        http-request return status 404
```

The string is processed through Helm's `tpl`, so chart helpers and `.Values` references are available. Editing this value bumps the bootstrap-config checksum on the HAProxy Deployment, which rolls HAProxy pods on the next `helm upgrade`.

!!! warning "Keep /ready returning 503 until the controller takes over"
    An override that returns 200 on `/ready` lets the Service route traffic to HAProxy before any backends exist — clients see 404 responses. Replicate the 503 behaviour, or accept the gap.

## Access logging

HAProxy writes its access logs to the container's stdout, so `kubectl logs` shows them directly:

```bash
kubectl logs -n haptic -l app.kubernetes.io/component=loadbalancer -c haproxy
```

Every frontend emits one JSON object per request (or per connection, for the
TCP-mode frontends), using HAProxy's native JSON log encoding:

```json
{"ts":"2026-07-25T19:05:19.615Z","req_id":"019f9ae9-3a61-7814-8601-774735249ecd","trace_id":"","client_ip":"10.244.0.1","frontend":"https","backend":"default_echo_echo_80","server":"SRV_1","method":"GET","host":"echo.example.com","listener_port":"443","path":"/api/v1","http_version":"HTTP/1.1","status":200,"bytes":73,"request_time_ms":0,"queue_time_ms":0,"connect_time_ms":1,"response_time_ms":3,"total_time_ms":4,"retries":0,"term":"----","resource":"default/echo","denied_by":"","tls_version":"TLSv1.3","tls_sni":"echo.example.com"}
```

The log target is `log stdout len 16384 format raw local0 info`. `format raw`
means records carry no syslog prefix, so a collector parses lines directly; each
record carries its own `ts` instead.

Two kinds of line on that stream are **not** JSON, so configure your collector to
tolerate them: HAProxy's own process and health-check messages, and the few lines
the HAProxy pod emits from its bootstrap config before the controller's first
render.

### Core fields

| Field | Meaning |
|-------|---------|
| `ts` | Request accept time, Coordinated Universal Time (UTC), with milliseconds |
| `req_id` | Identifies **one request through this proxy**. HAPTIC generates it and forwards it upstream as `X-Request-ID`, so it's the join key to your application's own logs. Always present. See [Request IDs](#request-ids) |
| `server_pod` | Name of the backend **pod** that served the request. HAProxy picks a pre-allocated server slot (`SRV_1`), so the slot name in `server` can't identify the pod; this resolves the connection's destination address through a map instead. Empty when HAProxy answered the request itself. The map's content is updated over the runtime API, so pod churn doesn't reload HAProxy |
| `namespace` | Namespace of the Kubernetes Service behind the chosen backend. Separate from `service` so both read like `server_pod`, matching OpenTelemetry and Elastic Common Schema (ECS) conventions. A cross-namespace Gateway API route makes this differ from the routing resource's namespace, which `resource` carries |
| `service` | The Kubernetes Service behind the chosen backend, as a bare name. Set by the backend that served the request, so it never depends on the backend *name* — a generated identifier that Ingress and Gateway API build differently. Empty when HAProxy answered the request itself |
| `destination_ip` | The address the client connected **to** — which entry point served the request. What it resolves to depends on how traffic reaches the pod: the LoadBalancer's virtual IP address with MetalLB-style routing, the node IP behind `externalTrafficPolicy: Local`, the pod IP behind a load balancer that rewrites the destination |
| `instance_pod`, `instance_node` | Which HAPTIC pod and node served the request. Read once at startup from the downward API into a process-scoped variable, so it costs nothing per request. Empty if you run HAProxy without those environment variables |
| `trace_id` | Identifies **one distributed transaction across every service**, taken from an inbound W3C `traceparent`; empty when the client sends none. It's deliberately *not* a substitute for `req_id`: every hop and every service in a trace shares one `trace_id`, so it can't identify a single request — and `req_id` doesn't exist in your tracing backend, so it can't open a trace. Keep both if you run tracing. If you don't and never plan to, `trace_id` costs about 14 bytes per record, and you can drop it by overriding `log-fields-100-core` through `controller.config.templateSnippets` |
| `client_ip` | Client address, after any `src-ip-header` rewrite |
| `frontend`, `backend`, `server` | Which listener served it, where it went, which pod |
| `method`, `host`, `path`, `http_version` | Request identity. `path` excludes the query string |
| `listener_port` | The port the routing lookup was keyed on, as a string. Host and path map keys are scoped by it (`<host>:<port>`), so it distinguishes a request that matched no route from one that matched the wrong listener's routes. For a Gateway listener this is the per-Gateway pod port the chart allocated, not the Gateway's `spec.listeners[].port`. Empty on frontends that run no routing logic (`status`, the cache-origin leg) |
| `status`, `bytes` | Response status and bytes sent to the client (JSON numbers) |
| `request_time_ms`, `queue_time_ms`, `connect_time_ms`, `response_time_ms`, `total_time_ms` | Timers in **milliseconds** — the `_ms` suffix is part of the name because other proxies report seconds. In order: receiving the request, waiting in the queue, establishing the backend connection, the backend's response, and the total. A timer is **`-1`** when its phase never happened, which is HAProxy's own convention: `connect_time_ms: -1` means the connection was never established, so a `-1` is a signal, not a bad reading. `total_time_ms` excludes idle time between keep-alive requests on HTTP frontends, and is the whole session duration on TCP frontends |
| `retries` | Connection retries, which `option redispatch` makes routine during a rolling update |
| `term` | HAProxy's 4-character termination state — separates a client abort from a server abort, a timeout, and a response HAProxy generated itself |
| `resource` | `<namespace>/<name>` of the Ingress, HTTPRoute or custom resource that owns the matched route — the join key back to Kubernetes |
| `denied_by` | Which gate blocked the request; empty when the backend answered |

Template libraries add fields for the features you configure, each only when
that feature is in use: `waf_action`, `waf_rule_id` and `waf_score`;
`rate_limit_allowed` and `rate_limit_remaining`; `cache` (`HIT`/`MISS`) and
`app_backend`; `auth_status` and `consumer`; `schema_outcome`; `tls_version`,
`tls_sni` and `tls_resumed`; `mtls_verify` and `mtls_cn`; `gw_route`;
`captured_headers`; `client_ip_peer`.

`denied_by` names the gate rather than leaving you to guess from a status code —
six mechanisms can produce a 401, three a 403, and three a 429. Values include
`rate_limit_local`, `rate_limit_shared`, `rate_limit_shared_unavailable`, `waf`,
`jwt_signature`, `jwt_expired`, `api_key`, `hmac`, `basic_auth`,
`consumer_groups`, `body_too_large`, `schema_invalid` and the `*_unavailable`
fail-closed variants.

### Add your own fields

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        accessLog:
          fields:
            tenant: req.hdr(X-Tenant)
            region: str(prod-eu)
```

Each value is one HAProxy sample expression, captured into a transaction variable
at request time and emitted as a JSON string. Use `str(<value>)` for a constant
label. Field names must match `^[A-Za-z_][A-Za-z0-9_]{0,39}$` and must not
collide with a built-in field; expressions must not contain whitespace, `#`, `"`
or a backslash. A violation fails the render with a message naming the field.

Because the capture happens at request time, a value that doesn't exist yet reads
empty — a WAF verdict, a cache status, an auth outcome, or anything else a
[SPOA hub](operations/spoa-hub.md) message produces later in the transaction. For
those, contribute a [`log-fields-*` snippet](#contribute-a-field-from-your-own-library)
instead: its items are evaluated when the line is written, after every filter has
run.

To log the query string, opt in with `query: query` — it's excluded by default
because query strings are a common accidental carrier of tokens and session ids.

Raise `accessLog.maxLineBytes` (default `16384`, accepted range 1024–65535) if
custom fields or captured request headers push records past it: HAProxy truncates
a longer line mid-byte, which makes the record unparseable. A value outside the
range fails the render rather than silently truncating every record.

### Where the logs go

By default records go to the [Vector sidecar](#vector-sidecar), which prints them
to its own stdout — so `kubectl logs <pod> -c vector` shows the access log, and
`kubectl logs <pod> -c haproxy` shows only HAProxy's startup and error output. With
`vector.enabled=false` the records go to the HAProxy container's stdout instead.

Either way stdout is convenient, but in a typical cluster it's scraped into a
general-purpose log store — and the access log carries `client_ip`, which is
personal data. `accessLog.targets` routes the access log somewhere
access-controlled instead:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        accessLog:
          targets:
            - ring:
                name: accesslog
                address: 127.0.0.1:6514   # a log-shipper sidecar on loopback
```

**HAProxy's own process and alert messages aren't affected.** They keep a
separate stdout target, so `kubectl logs` stays useful for on-call while only the
personal-data-bearing stream moves. That split is why the access-log target lives
in the `defaults` section and the process-log target in `global`.

### The access log is lossy under back-pressure

HAProxy reaches the Vector sidecar over a Unix **datagram** socket. Datagram
delivery is fire-and-forget: HAProxy hands the record to the kernel and moves on.
If Vector stops draining that socket, its receive queue fills and HAProxy
**discards** further records rather than blocking.

That trade-off is deliberate — the alternative is stalling request processing
behind a slow log consumer — but it means **the access log isn't a guaranteed
record of traffic**. Requests are served normally while records vanish.

The socket is the shock absorber, and it's small. At the default
`net.core.rmem_default` of 212992 bytes it holds roughly **167 records** of the
~700-byte JSON shape (the kernel charges per-datagram overhead, not payload).
Converted to time at your request rate, that's how long Vector may stall before
records are lost:

| Request rate | Stall tolerated |
|---|---|
| 1 000 req/s | ~170 ms |
| 5 000 req/s | ~35 ms |

Things that can exceed that window: a Vector topology reload (the sidecar
reloads on config change), a garbage-collection pause, or CPU starvation on a
busy node.

**Loss is exact and observable.** HAProxy counts every discarded record:

```
haproxy_process_dropped_logs_total    # Prometheus, via the vector sidecar's endpoint
DroppedLogs                           # `show info` on the stats socket
```

The chart ships an alert on it (`HAProxyAccessLogRecordsDropped`, enabled with
`controller.monitoring.prometheusRule`). Watch it: a gap in the access log is
least welcome during an incident, which is exactly when load is highest. If it
fires, give Vector more CPU or cut log volume with `accessLog.suppress`.

Each entry renders one HAProxy `log` line, so several entries fan out — which is
what you want while migrating from one collector to another:

| Field | Meaning |
|-------|---------|
| `address` | `stdout`, `stderr`, `fd@<n>`, `<host>:<port>` (UDP), `[<ipv6>]:<port>`, an absolute socket path, or `ring@<name>` |
| `format` | `raw`, `rfc3164`, `rfc5424`, `local`, `priority`, `short`, `timed`, `iso`. Defaults to `raw` for stdout/stderr and `rfc5424` otherwise |
| `facility`, `level` | Syslog facility (default `local0`) and level, either `info` (default) or `debug` |
| `ring` | Send through a buffered TCP ring instead of a bare address |

`level` is a *maximum* severity filter, and HAProxy emits access records at
`info`. Anything stricter — `notice`, `warning`, `err` — therefore drops every
record while `haproxy -c` still reports the config as valid, so the chart accepts
only the two levels that deliver.

An `address` of `ring@<name>` must name a ring some target in this list declares.
HAProxy accepts a dangling reference at config check and then refuses to start
with `unknown ring named`, so the render rejects it instead.

#### Why a ring for a sidecar

A `ring` is a buffered TCP client: records queue in memory when the collector is
unavailable and flush when it reconnects. Measured with the collector stopped,
25 of 25 requests were served in 112 ms total with no HAProxy errors, and all 25
records arrived once it came back. A plain `<host>:<port>` target is UDP and
drops them instead.

Ring fields: `name`, `address` (`<host>:<port>` or `[<ipv6>]:<port>` — HAProxy
3.4 rejects a Unix socket as a ring server, so send to a Unix-socket collector
with a plain-path `address` target instead), `size` (buffer bytes, default 65536 — it must exceed `maxLineBytes` by at least 256, or HAProxy caps the ring's record length to the buffer minus its header and truncates every longer record into invalid JSON, warning but not failing),
`logProto` (`legacy` for newline-delimited RFC 6587, or `octet-count`),
`connectTimeout`, `serverTimeout`, and `serverOptions` — appended verbatim to the
ring's `server` line, which is how you reach TLS or any other server keyword
without the chart modelling each one.

A collector reads this as ordinary syslog carrying a JSON payload. In Vector, a
`syslog` source parses the envelope and one `remap` recovers the record:

```yaml
sources:
  haproxy_access:
    type: syslog
    mode: tcp
    address: 0.0.0.0:6514
transforms:
  parsed:
    type: remap
    inputs: [haproxy_access]
    source: |
      . = parse_json!(string!(.message))
```

Two things to know:

- **A ring server's address is resolved when the config is parsed.** A Service DNS
  name that doesn't resolve at that moment fails the render. Use a loopback
  sidecar address or a literal IP, or pass `resolvers`/`init-addr` through
  `serverOptions`.
- **Any file referenced from `serverOptions`** (a `ca-file`, a client `crt`) must
  exist wherever the config is validated — the controller pod — not only in the
  HAProxy pod. Deliver such material through the chart's file mechanism so both
  see it.
- **A plain-path (Unix socket) target does no buffering.** It's the way to reach a
  collector on a socket, since HAProxy 3.4 rejects a Unix socket as a ring server.
  The socket doesn't have to exist when HAProxy starts, so a sidecar that comes up
  later is fine: measured with the socket absent, 25 of 25 requests were served
  and HAProxy logged one rate-limited `sendmsg()/writev() failed` alert for the
  whole run. But those records are gone — only a ring buffers them for replay.

Redirecting the stream changes who can read the records, not what they contain.

#### Dropping records you don't need

The access log is ~740 bytes per record, so about 700 MB per million requests. If
that volume genuinely forces your hand, you can drop the records for successful
requests:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        accessLog:
          suppress:
            successful: true
```

Denials, 4xx, and 5xx are always kept, so the failures a customer reports are
never the ones you discarded.

**This is off by default, and reaching for it first is usually a mistake.**
Retaining a full access log for weeks is lawful under legitimate interest (GDPR
Art. 6(1)(f)) — data minimisation doesn't require throwing it away. And the
successful requests immediately before and after a failure are exactly what let
you tell "this one request broke" from "everything was broken," or spot the retry
that succeeded. Route the log somewhere access-controlled first; suppress only
when volume, not privacy, is the problem.

The rule is emitted as `http-after-response`, not `http-response`. That matters:
`http-response` rules only run for responses that came from a *server*, so a WAF
deny or any other HAProxy-generated response would never be evaluated. TCP-mode
frontends are unaffected: `http-after-response` is HTTP-only, the internal TCP frontend
already carries `option dontlog-normal`, and the
TLS-passthrough frontend deliberately logs every connection because that record
is the only one it produces.
They still hold personal data, so retention limits and access controls still
apply at the destination.

### Vector sidecar

Every HAProxy pod runs a [Vector](https://vector.dev) container by default
(`vector.enabled`). It does two jobs.

**It receives the access log.** HAProxy writes records to a Unix datagram socket
(`vector.socketPath`, default `/run/vector/haproxy.sock`) on a volume shared with
the HAProxy container, and Vector prints them to stdout. To send them somewhere
else, override the rendered config as shown in
[Change the destination or the whole format](#change-the-destination-or-the-whole-format).

**It merges the metrics endpoints.** Vector scrapes HAProxy's Prometheus exporter
and — when the sidecar is enabled — the SPOA hub's metrics over loopback from
inside the pod, then re-exports both alongside its own on one port
(`vector.metricsPort`, default `9598`). Prometheus therefore scrapes one target per
pod instead of two:

```yaml
vector:
  podMonitor:
    enabled: true
```

Because Vector reaches both endpoints over loopback, neither needs to answer on the
pod IP, and the chart binds them accordingly:

| | `vector.enabled=true` | `vector.enabled=false` |
|---|---|---|
| HAProxy `/metrics` | answered only for connections arriving on `127.0.0.0/8` | answered on any address |
| Hub `/metrics` (`spoaHub.hub.metricsAddr: auto`) | `127.0.0.1:9095` | `0.0.0.0:9095` |
| Prometheus scrapes | `vector.podMonitor` (one target) | `spoaHub.monitoring.podMonitor` (two targets) |

HAProxy's `/healthz` and `/ready` stay reachable on the pod IP in both cases — the
kubelet's probes connect there, so only the exporter is restricted, not the
listener.

Set `vector.enabled=false` to remove the container and restore the previous
behaviour exactly: HAProxy logs to its own stdout and Prometheus scrapes HAProxy
and the hub directly.

#### How the config reaches it

The same path the SPOA hub's config takes. HAPTIC renders the Vector config and
pushes it through the Dataplane API into the shared general-storage volume, where
Vector's file watch picks it up and reloads without a restart. A bootstrap
ConfigMap seeds the file so the log socket exists before HAProxy starts — a Unix
datagram sender gets no error when nothing is listening, so early records would
otherwise be dropped silently.

The sidecar reports Ready only after HAPTIC's config has arrived. Its readiness
probe targets the metrics port, and only the rendered config declares the exporter
that serves it, so a pod never joins the Service advertising metrics it can't yet
produce — the same principle as HAProxy's own `/ready`, which answers only once a
pushed config is live.

### Request IDs

`req_id` is an RFC 9562 **UUIDv7** (`unique-id-format %[uuid(7)]`): opaque, but
time-ordered, which sorts and indexes better in a log store than a random UUIDv4.

It deliberately carries no address. An identifier built from `%ci`/`%fi` — the
shape HAProxy examples often show — puts the client IP (personal data under the
GDPR, Article 4(1) and Recital 30) and the address of the load balancer itself
into a value that's forwarded upstream, echoed back to clients, and copied into
application logs and support tickets. Once the address is inside the id, dropping the `client_ip`
field no longer redacts it.

For UUIDv4 instead, override the directive through a `defaults-settings-*`
snippet with a band above 150:

```yaml
controller:
  config:
    templateSnippets:
      defaults-settings-160-request-id:
        template: |
          unique-id-format %[uuid()]
```

`trace_id` comes from an inbound `traceparent` header, validated against the
[W3C Trace Context](https://www.w3.org/TR/trace-context/) grammar. HAPTIC never
invents a `traceparent` when the client sends none: a root span that no exporter
emits produces a broken trace in the backend. To forward the id upstream in a
header, use the [`haproxy-haptic.org/request-id`](libraries/haptic-annotations.md)
annotation.

### Contribute a field from your own library

`log-fields-*` is the extension point. Use it instead of
[`accessLog.fields`](#add-your-own-fields) when the value only exists at log time,
or when a template library should contribute the field for every install that
enables the feature. A snippet emits named log-format items and nothing else:

```yaml
controller:
  config:
    templateSnippets:
      log-fields-900-my-feature:
        template: |
          %(my_field)[var(txn.my_var)]
```

Only items available at log time are legal. HAProxy rejects `path`, `pathq`,
`req.hdr()`, `res.hdr()` and `req.ssl_sni` inside a `log-format`, so materialise
anything request- or response-scoped into a transaction variable first
(`http-request set-var(txn.my_var) req.hdr(X-Thing)`). Type an item (`:sint`,
`:bool`) only when its fetch always resolves — an unresolved typed item renders
`""` into a numeric slot.

### Change the destination or the whole format

Override `global-settings-100-logging` to change the log destination or facility.
To replace the line format wholesale, override `util-log-format-http` (HTTP-mode
frontends) or `util-log-format-tcp` (TCP-mode frontends). Note that a
`defaults`-section `log-format` can't reference HTTP-scoped fetches at all,
which is why the format is emitted per frontend.

!!! note "This isn't the Dataplane API log"
    `haproxy.dataplane.aclFormat` configures the **Dataplane API sidecar's own** access log, not HAProxy's traffic logs. HAProxy request logging is controlled by the `log` and `log-format` directives above.

## HAProxy Pod requirements

When `haproxy.enabled: false`, you're responsible for deploying HAProxy pods yourself. The controller discovers them via the pod selector at `controller.config.podSelector`, which defaults to:

```yaml
controller:
  config:
    podSelector:
      matchLabels:
        app.kubernetes.io/component: loadbalancer
        app.kubernetes.io/name: haptic        # set dynamically by the chart
        app.kubernetes.io/instance: <release> # set dynamically by the chart
```

If your existing HAProxy pods don't have those exact labels, either relabel them or override `controller.config.podSelector.matchLabels` to match.

Each discovered pod must:

1. **Carry labels matching `podSelector.matchLabels`**
2. **Run HAProxy in master-worker mode** with an admin socket the Dataplane API sidecar can connect to
3. **Run the Dataplane API sidecar** in the same pod, sharing the config volume with HAProxy
4. **Expose Dataplane API** on `haproxy.ports.dataplane` (default 5555)

<a id="example-haproxy-pod-deployment-byo-haproxy"></a>

### Example HAProxy Pod Deployment (bring-your-own HAProxy)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy
spec:
  replicas: 2
  selector:
    matchLabels:
      app.kubernetes.io/component: loadbalancer
      app.kubernetes.io/name: haptic
      app.kubernetes.io/instance: haptic
  template:
    metadata:
      labels:
        app.kubernetes.io/component: loadbalancer
        app.kubernetes.io/name: haptic
        app.kubernetes.io/instance: haptic
    spec:
      containers:
      - name: haproxy
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            mkdir -p /etc/haproxy/maps /etc/haproxy/ssl /etc/haproxy/general
            cat > /etc/haproxy/haproxy.cfg <<EOF
            global
                log stdout len 4096 local0 info
            defaults
                timeout connect 5s
            frontend status
                bind *:8404
                http-request return status 200 if { path /healthz }
                # Note: /ready endpoint intentionally omitted - added by controller
            EOF
            exec haproxy -W -db -S "/etc/haproxy/haproxy-master.sock,level,admin" -- /etc/haproxy/haproxy.cfg
        volumeMounts:
        - name: haproxy-config
          mountPath: /etc/haproxy
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8404
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8404
          initialDelaySeconds: 5
          periodSeconds: 5

      - name: dataplane
        image: haproxytech/haproxy-debian:3.2
        command: ["/bin/sh", "-c"]
        args:
          - |
            # Wait for HAProxy to create the socket
            while [ ! -S /etc/haproxy/haproxy-master.sock ]; do
              echo "Waiting for HAProxy master socket..."
              sleep 1
            done

            # Create Dataplane API config
            cat > /etc/haproxy/dataplaneapi.yaml <<'EOF'
            config_version: 2
            name: haproxy-dataplaneapi
            dataplaneapi:
              host: 0.0.0.0
              port: 5555
              user:
                - name: admin
                  password: adminpass
                  insecure: true
              transaction:
                transaction_dir: /var/lib/dataplaneapi/transactions
                backups_number: 10
                backups_dir: /var/lib/dataplaneapi/backups
              resources:
                maps_dir: /etc/haproxy/maps
                ssl_certs_dir: /etc/haproxy/ssl
                general_storage_dir: /etc/haproxy/general
            haproxy:
              config_file: /etc/haproxy/haproxy.cfg
              haproxy_bin: /usr/local/sbin/haproxy
              master_worker_mode: true
              master_runtime: /etc/haproxy/haproxy-master.sock
              reload:
                reload_delay: 1
                reload_cmd: /bin/sh -c "echo 'reload' | socat stdio unix-connect:/etc/haproxy/haproxy-master.sock"
                restart_cmd: /bin/sh -c "echo 'reload' | socat stdio unix-connect:/etc/haproxy/haproxy-master.sock"
                reload_strategy: custom
            log_targets:
              - log_to: stdout
                log_level: info
            EOF

            # Start Dataplane API
            exec dataplaneapi -f /etc/haproxy/dataplaneapi.yaml
        volumeMounts:
        - name: haproxy-config
          mountPath: /etc/haproxy

      volumes:
      - name: haproxy-config
        emptyDir: {}
```
