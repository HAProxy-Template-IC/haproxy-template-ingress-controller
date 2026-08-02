# SPOA hub

## Overview

HAPTIC ships a `spoa-hub` container image that bundles the [haproxy-spoa-hub](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) plus a curated set of plugin shared libraries. Deployed as a sidecar to each HAProxy pod, the hub is a Stream Processing Offload Agent (SPOA): it speaks the [Stream Processing Offload Protocol (SPOP) wire protocol](https://docs.haproxy.org/spoe.html) over a shared Unix domain socket and delegates per-request work to plugins: Web Application Firewall (WAF) inspection, geoip, JA3/JA4 fingerprinting, OpenID Connect (OIDC) / Security Assertion Markup Language (SAML) auth, request mirroring, nginx-style external auth, and shared request-rate limiting.

This page documents the exact components bundled with the version of HAPTIC you are reading docs for, how to verify them end-to-end, and how to tune the HAProxy-side Stream Processing Offload Engine (SPOE) wiring the chart emits when the sidecar is enabled.

## Enabling the hub

The sidecar renders whenever at least one plugin is enabled: with the default `spoaHub.enabled: null`, the chart derives the master switch from the per-plugin `spoaHub.plugins.<name>.enabled` values. To enable a plugin directly:

```bash
--set spoaHub.plugins.fingerprinting.enabled=true
```

Some plugins auto-enable with the template library that consumes them — each per-plugin `enabled` default is a chart-evaluated template string:

- **api-gateway** follows `controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled`,
- **coraza** follows a non-empty WAF policy catalog, `waf.dispatch.mode=default-on`, `controller.templateLibraries.nginxIngress.enabled`, or `controller.templateLibraries.haproxyIngress.enabled`,
- **external-auth** follows `controller.templateLibraries.nginxIngress.enabled`,
- **mirror** follows `controller.templateLibraries.gateway.enabled`,
- **rate-limit** follows `rateLimit.shared.enabled`.

The gateway library is on by default and auto-enables the `mirror` plugin, so a default install already runs the hub with `mirror`. The `coraza` plugin auto-enables when you turn on the opt-in haproxy-ingress or nginx-ingress annotation library, and `external-auth` when you turn on nginx-ingress; `fingerprinting`, `maxmind`, and `sso-auth` stay off until you enable them.

Adding an inline policy, a trusted ConfigMap reference, or a default policy auto-enables Coraza; no redundant policy enable flag is required. All template behavior—dispatch, policy catalogs, permissions, body contracts, and custom-rule bounds—shares the structured `extraContext.waf` tree documented in the [native annotation reference](../libraries/haptic-annotations.md#reusable-waf-policies). Coraza execution belongs only to `spoaHub.plugins.coraza`: `timeoutMs`, `maxConcurrency`, `maxQueue`, directives, and plugin parameters have no feature-level aliases.

An explicit boolean on `spoaHub.enabled` always wins: `false` forces the sidecar off even with plugins enabled; `true` renders it with none. See the [Chart Values Reference](../reference.md#spoa-hub-sidecar) for every `spoaHub.*` value.

## Bundled components

The image is published at `registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<HAPTIC version>` and is built from the following pinned upstream releases:

<!-- BEGIN: spoa-hub-bundle -->

| Component       | Pinned version                          |
| --------------- | --------------------------------------- |
| Hub               | `v0.12.0`                     |
| `api-gateway`    | `v0.1.0`      |
| `coraza`          | `v0.10.0`           |
| `external-auth`   | `v0.5.0`    |
| `fingerprinting`  | `v0.3.0`   |
| `maxmind`         | `v0.4.0`          |
| `mirror`          | `v0.6.0`           |
| `rate-limit`      | `v0.2.0`       |
| `sso-auth`        | `v0.3.0`         |

Plugin `.so` files target glibc `2.36` (Debian bookworm).

<!-- END: spoa-hub-bundle -->

The table is generated from `versions-spoa.env` at the repository root. CI fails if the rendered output drifts from the source of truth.

### Reload and upgrade behavior

Plugin configuration and instance state are hot-reloadable. When a reload
retires a plugin generation, the hub drains its in-flight work and calls its
`shutdown` and `destroy` hooks. The native plugin library itself remains mapped
until the hub process exits: a plugin can embed a foreign runtime or retain
process-global threads, callbacks, statics, and thread-local cleanup routines that a
generic host can't prove are safe to unload. This prevents reload-time crashes
when a plugin such as Coraza is removed.

Replacing a plugin `.so` therefore requires a hub process restart, not only a
configuration reload. HAPTIC chart upgrades do this normally by rolling the
HAProxy pods when the bundled `spoa-hub` image changes.

## What each plugin does

- **api-gateway** — performs bounded JSON request validation against schemas compiled at plugin initialization/reload.
- **coraza** — embeds the [Coraza WAF](https://coraza.io/) engine and runs HTTP request inspection against the Open Worldwide Application Security Project (OWASP) Core Rule Set v4. HAPTIC wires the request phase only — there's no response-body inspection stage, so response compression doesn't interact with the WAF.
- **external-auth** — implements nginx-style `auth_request` semantics: makes an HTTP subrequest to an upstream auth service and returns allow/deny plus identity headers to HAProxy.
- **fingerprinting** — computes JA3, JA3N, and JA4 TLS fingerprints from the ClientHello.
- **maxmind** — performs in-memory MaxMind MMDB lookups against operator-provided database files: City, Country, Autonomous System Number (ASN), and so on.
- **otel** — emits OpenTelemetry traces, metrics, and log records via OpenTelemetry Protocol (OTLP) gRPC or HTTP.
- **mirror** — mirrors HTTP requests to a secondary backend for traffic shadowing; used by the gateway library to implement the Gateway API `HTTPRouteFilter` of type `RequestMirror`.
- **rate-limit** — enforces shared request-rate budgets for native `haproxy-haptic.org/rate-limit-*` annotations. By default, `rateLimit.shared.managedStore.enabled=true` deploys a chart-managed HA Valkey store: three StatefulSet pods, one writable primary, replicas, Sentinel failover, a PodDisruptionBudget, and a store NetworkPolicy. You can also disable the managed store and provide your own Redis/Valkey/Sentinel/Cluster endpoints via `rateLimit.shared.externalStore.urls`. Shared mode requires one of those stores; HAPTIC fails the render rather than silently falling back to per-pod limiting. If the hub/plugin produces no verdict for an annotated route, HAProxy fails closed with 429 to avoid bypassing the configured limit. The default token-bucket mode bounds local key state and disables optimistic cold-starts under capacity pressure. Exact `gcra` mode performs a synchronous store check with a short default store timeout, so store trouble fails closed instead of adding a long request tail. The managed store is HA but intentionally fixed-size; use bring-your-own infrastructure for horizontal Valkey scaling.
- **sso-auth** — handles OIDC and SAML2 single sign-on flows with encrypted session cookies.

When several plugins are enabled, cheap source-IP shared rate limiting runs first (`025`) so rejected floods don't consume WAF CPU. Coraza follows (`050`), then external auth (`100`), then JSON request validation (`200`). Authenticated-consumer rate limits run in the selected backend after native authentication establishes the consumer identity.

## Update the WAF rule set

The Coraza plugin embeds an Open Worldwide Application Security Project (OWASP)
Core Rule Set (CRS) v4 release, so the WAF has rules the moment you enable it.
That embedded ruleset only moves when the plugin image does. To pick up a CRS
release without waiting for a HAPTIC release, point HAPTIC at the release
tarball:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        waf:
          crs:
            url: https://github.com/coreruleset/coreruleset/releases/download/v4.19.0/coreruleset-4.19.0-minimal.tar.gz
```

The URL must be `https://`. The ruleset decides what the WAF blocks, so a
plaintext fetch could be replaced in transit and the substituted rules would
still validate.

HAPTIC fetches the archive, expands it, and writes the rule files to the HAProxy
pods' general storage. The `.conf` files are prefixed `crs-`; the `.data` files
keep their exact upstream names, because rules reference them by bare name
(`@pmFromFile lfi-os-files.data`). Your `spoaHub.plugins.coraza.directives` are
left alone apart from the two embedded-CRS includes, which are replaced by an
include of the fetched files — so the rest of the block, including the order of
`SecRuleEngine` and any `SecRule` you added, keeps working as written.

### What a refresh costs

Nothing you notice. Adopting or refreshing a ruleset reloads neither HAProxy nor
the SPOA hub:

1. HAPTIC re-fetches on `waf.crs.refreshInterval` (default `1h`) with a
   conditional request. An unchanged ruleset answers `304` and stops there — no
   re-render, no push, no recompile.
2. A changed ruleset is pushed to the HAProxy pods as general files. Those files
   carry `reloadOnPush: false`, because HAProxy itself never reads them.
3. The Coraza plugin notices the new files, rebuilds its rules in place, and
   swaps them in. Requests in flight finish against the rules they started with.

Step 3 needs coraza plugin v0.10.0 or later, which is what this HAPTIC version
bundles. If you pin an older SPOA hub bundle through `spoaHub.image`, the files
arrive but nothing rebuilds, and the WAF keeps running the previous rules until
something else reloads the hub.

### Confirm which ruleset is running

The plugin logs its rule count whenever it compiles — at startup and on every
successful refresh. The embedded ruleset compiles to 661 rules:

```console
$ kubectl -n haptic logs -l app.kubernetes.io/component=loadbalancer -c spoa-hub | grep -i coraza
Coraza default WAF initialized with 661 rules
Coraza reloaded its rule set after an on-disk change
```

The rule count alone doesn't identify the ruleset, since a CRS release can
compile to a similar number. List the files to see which one is on the pods —
`crs-` prefixed files are there only when a fetched ruleset is in use:

```bash
kubectl -n haptic exec deploy/haptic-haproxy -c haproxy -- ls /etc/haproxy/general/ | grep '^crs-'
```

`plugin_coraza_rule_reloads_total{result="failed"}` counts refreshes that didn't
compile. It should be `0`; anything else means the WAF is still serving the
previous ruleset — see below.

### If the ruleset can't be obtained

The WAF is never left without rules. HAPTIC falls back in order:

1. **The fetched ruleset**, when the fetch and expansion both succeed.
2. **The ruleset already deployed to the fleet**, read back from the published
   file resources. This survives a controller restart while the upstream is
   down, which an in-memory cache wouldn't.
3. **The plugin's embedded ruleset.** Older than upstream, but it's in the
   binary and can't be absent.

A failed fetch never fails the render, so an upstream outage can't block
unrelated configuration changes. It also can't pass unnoticed: an archive that
downloads but contains no `.conf` rule files is rejected outright, because a WAF
compiling zero rules looks healthy while blocking nothing.

The same rule applies at the last step. If a refreshed ruleset reaches the pods
but doesn't compile, the plugin keeps the rules it's already running and counts
the failure rather than dropping to an unarmed WAF.

## Tune a WAF policy from detect to deny

A new WAF policy starts in `enforcement: detect`: the full ruleset runs and records what it *would* block, but nothing is denied. The workflow below uses the OWASP Core Rule Set (CRS) blocking-evaluation rules as the would-block signal and shows how to confirm a clean baseline from data and then flip the policy to `deny`.

### Read the per-rule hit metrics

The hub serves Prometheus metrics on `spoaHub.hub.metricsAddr` (default `127.0.0.1:9095` inside the HAProxy pod). The coraza plugin (v0.7.0+) exports:

| Metric | Labels | Meaning |
| ------ | ------ | ------- |
| `plugin_coraza_rule_hits_total` | `phase`, `rule_id`, `severity`, `app` | Every rule that matched, on every evaluation — including traffic that was allowed. This is the detect-mode signal. |
| `plugin_coraza_denials_total` | `phase`, `rule_id`, `app` | Requests denied, labeled with the single interrupting rule. Stays flat in detect mode. |
| `plugin_coraza_evaluations_total` | `phase`, `action`, `app` | All evaluations by outcome. |

The `app` label is the Coraza application: `policy:<name>` for a trusted-catalog policy, `policy:<namespace>/<name>` for a self-service policy, and `<namespace>/<name>` for route-local rules. Rules that declare no severity (the ruleset's administrative and reporting rules) carry `severity="none"`.

The metrics address binds to the pod loopback, so scrape it with a PodMonitor targeting the HAProxy pods, or check it directly. The command execs into the `haproxy` container deliberately: all containers in the pod share one network namespace, so `127.0.0.1:9095` is reachable from any of them — and the `haproxy` container ships `curl`, while the `spoa-hub` image carries no HTTP client at all:

```console
kubectl exec -n <namespace> <haproxy-pod> -c haproxy -- \
  sh -c 'command -v curl >/dev/null && curl -s 127.0.0.1:9095/metrics || wget -qO- 127.0.0.1:9095/metrics' \
  | grep plugin_coraza_rule_hits_total
```

### Identify would-block rules

In detect mode, a request is "would block" when its accumulated anomaly score crosses the ruleset's threshold — visible as hits on the blocking-evaluation rules `949110`/`949111`. Over a representative traffic window (a week that includes your batch jobs and deploys is a good default):

```promql
# How often would this policy have blocked?
sum by (app) (increase(plugin_coraza_rule_hits_total{rule_id=~"94911[01]"}[7d]))

# Which rules fired at all, worst first?
sort_desc(sum by (rule_id, severity) (
  increase(plugin_coraza_rule_hits_total{app="policy:my-policy", severity!="none"}[7d])
))
```

Zero `949110`/`949111` hits over a representative window is your clean baseline: flip `enforcement: detect` to `deny` and you're done. Nonzero hits need classification first.

### Classify hits from the access log

Rule-hit metrics tell you *which* rules fire. To tie a rule hit to one request, read HAProxy's JSON access log — every request already carries the WAF verdict, correlated with `req_id`:

| Field | Answers |
| ----- | ------- |
| `waf_rule_id` | which CRS rule interrupted, for the one 403 a user complained about |
| `waf_score` | the anomaly score, so you can see how far from the threshold this request sat |
| `waf_rules_hit` | how many rules matched on this request — one noisy rule, or twenty |
| `waf_matched_var` | **which request fields** the rules matched on, as names: `ARGS_GET:id,REQUEST_LINE`. Never the values |
| `waf_action` | `allow` or `deny`, so detect-mode traffic is distinguishable |
| `denied_by` | `waf` when the WAF blocked, so a 403 from the WAF is distinguishable from the five other gates that also return 403 |

Those fields cost nothing extra: they're on by default whenever the coraza plugin is enabled, and the access log can be routed to an access-controlled destination instead of the container's stdout. See [Access logging](../haproxy-deployment.md#access-logging).

`waf_matched_var` closes the loop: it names the request fields the rules matched on, so `waf_rule_id` tells you *which rule* and `waf_matched_var` tells you *on what*. That's everything a scoped `ruleExclusions` entry needs, from the access log alone.

The names are ordered with the reported rule's own targets first, then the rest most-severe-first, deduplicated and capped at five. Only request-derived variables appear (`ARGS*`, `REQUEST_*`, `QUERY_STRING`, `PATH_INFO`, `FILES*`, `MULTIPART*`, `XML`, `JSON`) — Coraza's internal `TX:*` scoring collection is filtered out, since it names nothing an exclusion can target. Requires coraza plugin v0.8.0 or later.

```json
{"waf_action":"deny","waf_rule_id":942100,"waf_score":5,"waf_rules_hit":3,
 "waf_matched_var":"ARGS_GET:id,REQUEST_LINE","denied_by":"waf"}
```

Read that as: rule 942100 fired on the `id` query argument. The exclusion follows directly:

```yaml
my-policy:
  ruleExclusions:
    - rules: [942100]
      excludeTarget: "ARGS:id"
```

### See the value a rule matched on

You rarely need this — the field name is what an exclusion targets. When you do, prefer a hash over the raw value. Set `rule_match_log` with `matched_data_log = "hash"` and the plugin logs one rule-match line per match carrying a hash of the matched value — enough to recognize the same false positive recurring across requests, with no request content recorded anywhere. Reproduce the whole `params` block, because your value replaces it:

```yaml
spoaHub:
  plugins:
    coraza:
      params: |
        detect_only = false
        transaction_ttl_ms = 10000
        max_cached_transactions = 1024
        rule_match_log = true
        matched_data_log = "hash"
        expose_matched_data = false
```

The line carries `sha256:<hex>` in place of the value, plus the rule id and the rule's own message.

!!! warning "Don't use `matched_data_log = \"truncate\"` to work around a hash you can't read"
    `truncate` logs the matched bytes themselves — up to `matched_data_max_bytes`, at `WARN`, on the sidecar's stdout. The plugin's own source calls this out: `MatchedRule.Data` may contain credentials or arbitrary request bodies. A hash you can group by is almost always the answer; when you genuinely need the value, use the SPOE route below so it lands in the access log, whose destination you control.

When you need the literal value, return it through SPOE rather than logging it in the hub. Set `expose_matched_data = true` (the value is capped by `matched_data_max_bytes`, default 128 bytes) and the plugin hands the matched data back to HAProxy in `txn.hub.coraza.data`:

```yaml
spoaHub:
  plugins:
    coraza:
      params: |
        detect_only = false
        transaction_ttl_ms = 10000
        max_cached_transactions = 1024
        rule_match_log = false
        matched_data_log = "none"
        expose_matched_data = true
```

Then add it to the access log with a `log-fields-*` snippet. Contribute it as a snippet rather than through `accessLog.fields`, because a log-format item is evaluated when the line is written — after the WAF has run — while `accessLog.fields` captures at request time:

```yaml
controller:
  config:
    templateSnippets:
      log-fields-900-waf-matched-data:
        template: |-
          %(waf_data)[var(txn.hub.coraza.data)]
```

This puts the matched value in the access log, which you can route away from stdout — so the sensitive field lands in the one stream whose destination you control.

### Last resort: the Coraza audit log

When you need the full transaction — every matched rule with its target and the request metadata together — enable Coraza's own audit engine through the trusted policy's `secLang`. A self-service catalog can't: ask the administrator to adopt the policy, or to enable the log in the shared directives.

Nothing is written today: the audit engine is enabled by Coraza's recommended configuration, but `SecAuditLog` has no target, so the writer is a no-op. Setting a target is what turns the stream on — and **set `SecAuditLogParts` in the same breath.** Coraza's default part set is `ABIJDEFHZ`, which includes the request body (`I`) and the response body (`E`); the narrower set below deliberately leaves both out.

```yaml
my-policy:
  enforcement: detect
  secLang: |
    SecAuditEngine RelevantOnly
    SecAuditLogParts ABFHKZ
    SecAuditLog /dev/stdout
    SecAuditLogFormat JSON
```

Records land on the spoa-hub container's stdout as JSON, one per request that matched a rule:

```console
kubectl logs -n <namespace> <haproxy-pod> -c spoa-hub | grep '"transaction"'
```

Understand what you're turning on. With parts `ABFHKZ` each record carries the client IP and ports (`A`), the full request line **including the query string** and every request header — `Cookie` and `Authorization` among them (`B`), and per matched rule the bytes that matched (`K`). The request body (`C`) isn't included, which is the one thing this set leaves out.

Two properties make this the last resort rather than the default. These records are personal data, in volume. And Coraza's audit writer opens its target directly, so it bypasses the hub's and the plugin's log configuration completely: no log level, and no `accessLog.targets`-style routing, applies to it. `SecAuditLog` accepts a file path, so you can point it at a mounted volume instead of `/dev/stdout` if you need the detail without your general log pipeline collecting it.

Turn it off once the policy is tuned.

You have two structured, self-service-safe ways to tune a false positive: `ruleExclusions` for exclusions and `allowedMethods` for method-driven hits.

`ruleExclusions` covers the full range from a whole attack category down to a single rule on a single path. You supply only rule IDs or CRS tags, an exact target variable, and a literal path; the chart writes the CRS directive:

```yaml
my-policy:
  enforcement: detect
  ruleExclusions:
    # drop a request field from a whole attack category (a search box
    # tripping SQL-injection and XSS):
    - tags: [attack-sqli, attack-xss]
      excludeTarget: "ARGS:q"
    # disable one rule only on matching paths (a git host, where CRS rule
    # 930130 fires on every .git/ git-over-HTTP URL):
    - rules: [930130]
      onPathContains: ".git/"        # or onPathPrefix / onPathExact / onPathSuffix
    # drop one parameter from a single rule (optionally path-scoped):
    - rules: [941320]
      excludeTarget: "ARGS:wp_post"
    # disable a rule everywhere in this app:
    - rules: [913100]
```

`ruleExclusions` works in a self-service catalog without any administrator grant. The chart reserves the CRS setup, anomaly scoring, and correlation rules (900000-901999, 949xxx, 959xxx, 980xxx, 990xxx+) so an exclusion can silence an attack rule that false-positives but can't disable the scoring rule that makes the block decision — you can't turn off your own enforcement through an exclusion. Regex collection keys (`ARGS:/regex/`) are rejected; only exact variable names are allowed.

Widen the method allowlist when a whole class of hits comes from a method the app legitimately uses (`PUT`, `PATCH`, `DELETE` on an HTTP API) — set the policy's `allowedMethods` instead of excluding rule targets one by one.

### Flip to deny

After the exclusions have been in place for another observation window with zero would-block hits, set `enforcement: deny`. Watch `plugin_coraza_denials_total` for the first days — it now counts real blocks — and use the access log's `waf_rule_id` and `denied_by` to justify any individual one. If you turned the audit log on to classify hits, turn it off again here.

## Correlating hub logs with the access log

The hub's log lines carry `span.req_id`, holding the same value as the JSON access
log's `req_id` field for that request. So a hub warning and the HAProxy record for
the request that caused it can be joined on one key:

```console
# the access-log record
kubectl logs -n <namespace> <haproxy-pod> -c haproxy | jq 'select(.req_id=="019f9e64-e9de-7d1b-88c9-76644f0e9b86")'

# and anything the hub said about the same request
kubectl logs -n <namespace> <haproxy-pod> -c spoa-hub | jq 'select(.["span.req_id"]=="019f9e64-e9de-7d1b-88c9-76644f0e9b86")'
```

The chart sends HAProxy's own `unique-id` on every SPOE message, and the hub adopts
it. Nothing to configure. Requires spoa-hub v0.11.0 or later; an older hub ignores
the argument and logs its own internal id instead.

`spoa_request_id_source_total{source}` reports which id each message used —
`adopted`, `generated`, or `rejected`. **Alert on `rejected`**: it means the hub
replaced a supplied id, so its logs and the access log name every request
differently, and no other signal shows that.

The id is a correlation label. Don't build anything that treats it as unique — it
comes off the wire, so a plugin keying state on it could serve one request's state
to another.

## Managed shared rate-limit store

Enable shared rate limiting with:

```yaml
rateLimit:
  shared:
    enabled: true
```

The managed store is enabled by default once shared rate limiting is enabled:

```yaml
rateLimit:
  shared:
    managedStore:
      enabled: true
      replicas: 3
      sentinel:
        quorum: 2
```

HAPTIC renders a fixed-size HA Valkey topology:

- one `StatefulSet` with three pods by default;
- one writable Valkey primary and replicas;
- one Sentinel sidecar per pod for failover;
- a PodDisruptionBudget with `maxUnavailable: 1`;
- a NetworkPolicy that admits HAProxy/SPOA traffic plus store-internal Valkey/Sentinel traffic.

This gives automatic failover for the default shared limiter store without adding a HAPTIC-owned Valkey operator. It's deliberately not an automatically horizontally scaled Valkey Cluster. A hot limiter key still maps to one writable primary, so DoS-facing protection relies on the plugin's bounded local key state, bounded background refresh queue, default store timeout of 10 milliseconds for exact store checks, circuit breaker, and fail-closed HAProxy verdict handling.

If you already run a Redis/Valkey platform, disable the managed store and provide the endpoint directly:

```yaml
rateLimit:
  shared:
    enabled: true
    managedStore:
      enabled: false
    externalStore:
      urls:
        - "redis-sentinel://valkey-sentinel.data.svc:26379/0?sentinelServiceName=mymaster"
```

Listing several URLs generates the plugin's `store_urls = [...]` form for deployments that intentionally shard keys across independent stores. The chart generates the store lines itself and rejects a manual `store_url`/`store_urls` inside `spoaHub.plugins.rate-limit.params`, so overriding that scalar can't drop or duplicate the store wiring.

Both the outer plugin budget and its store-operation budget are plugin execution settings:

```yaml
spoaHub:
  plugins:
    rate-limit:
      timeoutMs: 50
      storeOperationTimeoutMs: 10
```

`storeOperationTimeoutMs` is the important request-latency bound for exact `gcra` mode because that mode performs a synchronous store operation per request. Set it from measured in-cluster Valkey/Sentinel round-trip time plus a small margin; raising it improves tolerance for slow cross-zone or external stores, but also raises the worst-case fail-closed request tail when the store is unhealthy. Keep the default token-bucket mode for DoS-facing edge limits.

## Geolocation lookups

The `maxmind` plugin resolves the client IP against a MaxMind MMDB database and hands the result back to HAProxy as a transaction variable you reference in ACLs, headers, or map keys. Unlike `coraza` and `mirror`, no template library dispatches it for you, so the recipe has two operator-owned halves: configure the plugin (enable, database, lookup), then dispatch the lookup in a frontend snippet and consume the result.

### 1. Enable the plugin and declare the lookup

Turn the plugin on and define, under `params:`, which MMDB files to open and which fields to extract. Each `[[lookups]]` entry sets `output_var` (the variable the hub writes back) and `message` (the SPOE message that triggers it — keep it equal to the plugin's `messages` entry, the default `geoip-enrich`):

```yaml
# values.yaml
spoaHub:
  plugins:
    maxmind:
      enabled: true
      messages: ["geoip-enrich"]   # chart default; drives the generated SPOE group name
      params: |
        [databases]
        country = { path = "/data/GeoLite2-Country.mmdb" }

        [[lookups]]
        name       = "country_code"
        message    = "geoip-enrich"
        database   = "country"
        path       = ["country", "iso_code"]
        output_var = "geo_country"
```

### 2. Mount the MMDB database

The database file lives in the HAProxy pod, where the `spoa-hub` sidecar runs. Declare a pod volume with `haproxy.extraVolumes` and mount it into the sidecar with `spoaHub.extraVolumeMounts` at the path your `params:` references (`/data` above).

MMDB files exceed the 1 MiB `ConfigMap`/`Secret` size limit (GeoLite2-Country alone is several MB), so don't try to mount one from a `Secret`. Use a `PersistentVolumeClaim`, or — as below — an `emptyDir` populated by an init container that downloads the database. The init container needs your MaxMind license key; this example reads it from a `Secret` you create separately:

```yaml
# values.yaml
haproxy:
  extraVolumes:
    - name: maxmind-data
      emptyDir: {}
  initContainers:
    - name: fetch-maxmind
      image: curlimages/curl:latest
      command:
        - sh
        - -c
        - >
          curl -fsSL "https://download.maxmind.com/app/geoip_download?edition_id=GeoLite2-Country&license_key=$LICENSE_KEY&suffix=tar.gz"
          | tar -xz --strip-components=1 -C /data
      env:
        - name: LICENSE_KEY
          valueFrom:
            secretKeyRef:
              name: maxmind-license
              key: license_key
      volumeMounts:
        - name: maxmind-data
          mountPath: /data

spoaHub:
  extraVolumeMounts:
    - name: maxmind-data
      mountPath: /data
      readOnly: true
```

### 3. Dispatch the lookup and use the result

When the plugin is enabled, the chart emits the SPOE plumbing automatically: a `[[plugins]]` block, a `spoe-message geoip-enrich` (sending the client IP as `args ip=src`), and a `spoe-group geoip-enrich-group`. The SPOE agent runs with `option var-prefix hub`, so an `output_var` of `geo_country` lands in HAProxy as `txn.hub.maxmind.geo_country` — the `txn.hub.<plugin>.<output_var>` convention shared by every hub plugin.

The one piece the chart can't infer is *when* to run the lookup and *what* to do with the result. Add a `frontend-spoe-filters-*` snippet through `controller.config.templateSnippets`; it renders right after the `filter spoe engine spoa-hub` directive, so `send-spoe-group` and the variable are both in scope:

```yaml
# values.yaml
controller:
  config:
    templateSnippets:
      frontend-spoe-filters-300-geoip:
        template: |
          http-request send-spoe-group spoa-hub geoip-enrich-group
          # Pass the country to backends as a header...
          http-request set-header X-Country %[var(txn.hub.maxmind.geo_country)]
          # ...or block selected countries at the edge:
          http-request deny deny_status 403 if { var(txn.hub.maxmind.geo_country) -m str RU KP }
```

The snippet name's `300` orders it after the bundled `frontend-spoe-filters-050-coraza` and `-100-external-auth` dispatchers; pick any number that slots it where you want in the request pipeline.

## Verifying the published image

The image is signed by digest with cosign keyless via GitLab OIDC. The CycloneDX Software Bill of Materials (SBOM) is attached as an in-toto attestation.

```bash
# Image signature
cosign verify registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<version> \
  --certificate-identity-regexp '^https://gitlab\.com/haproxy-haptic/haptic//\.gitlab-ci\.yml@refs/tags/.*$' \
  --certificate-oidc-issuer 'https://gitlab.com'

# CycloneDX SBOM
cosign verify-attestation registry.gitlab.com/haproxy-haptic/haptic/spoa-hub:<version> \
  --type cyclonedx \
  --certificate-identity-regexp '^https://gitlab\.com/haproxy-haptic/haptic//\.gitlab-ci\.yml@refs/tags/.*$' \
  --certificate-oidc-issuer 'https://gitlab.com'
```

Each upstream `.so` was independently `sha256sum`-checked and `cosign verify-blob`-ed against its source project's tag identity at image-build time. The SBOM enumerates Rust dependencies via the [`cargo-auditable`](https://github.com/rust-secure-code/cargo-auditable) metadata embedded in every plugin binary.

## Performance tuning

The chart's `spoaHub.haproxy.*` values map directly to HAProxy directives the chart's `spoa-hub` template library emits in the `backend spoa-hub` block and the `spoa-hub-agent` agent inside `spoe.conf`. Defaults are tuned for the typical sidecar deployment (single hub colocated with HAProxy over a Unix domain socket); change them when traffic profile or plugin behavior diverges from that baseline.

| Values key                          | HAProxy directive                                                  | Default              | When to change                                                                                                        |
| ----------------------------------- | ------------------------------------------------------------------ | -------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `spoaHub.haproxy.socketPath`        | `server hub <path>` in `backend spoa-hub`                          | `/run/spoa/hub.sock` | Match a different bind path the sidecar listens on (for example when `securityContext.runAsUser` blocks `/run/spoa`).        |
| `spoaHub.haproxy.modeSpop`          | `mode` line in `backend spoa-hub` — `mode spop` (true) or `mode tcp` (false); the `filter spoe engine` directive on the frontend is emitted either way | `true`               | Auto-falls back to `mode tcp` on HAProxy 3.0 (`mode spop` was introduced in 3.1). Set `false` to force `mode tcp` on 3.1+ as well — rare, mostly compat testing.                                           |
| `spoaHub.haproxy.timeoutHello`      | `timeout hello` on `spoe-agent`                                    | `2s`                 | Raise if the hub regularly logs `HELLO` timeouts under cold-start (for example heavy plugin init like MaxMind DB load).        |
| `spoaHub.haproxy.timeoutIdle`       | `timeout idle` on `spoe-agent` and `timeout server` on the backend | `5m`                 | Lower to free pooled connections faster in low-traffic clusters; raise to match upstream auth-service idle budgets.   |
| `spoaHub.haproxy.timeoutProcessing` | `timeout processing` on `spoe-agent`                               | largest enabled message budget + `100ms` | Leave null to derive a deadline that can honor every plugin handling one message, including sequential dependency stages. Plugins on unrelated messages don't inflate one another. An explicit shorter value fails rendering. |
| `spoaHub.haproxy.timeoutProcessingMarginMs` | derivation margin                                         | `100`                | Scheduling and serialization margin in milliseconds between the hub's largest message budget and HAProxy's outer deadline. |
| `spoaHub.haproxy.poolMaxConn`       | `pool-max-conn` on the `server hub` line                           | `100`                | Tune to peak concurrent in-flight SPOE messages — usually `request-rate × p99-processing-latency`.                    |
| `spoaHub.haproxy.poolPurgeDelay`    | `pool-purge-delay` on the `server hub` line                        | `30s`                | Lower to release idle pooled connections sooner during traffic dips.                                                  |

The `spoaHub.plugins.<name>.timeoutMs` field on the chart side is independent — it sets the per-plugin timeout the hub enforces internally and doesn't appear in the rendered HAProxy config.

## See also

- [haproxy-spoa-hub](https://gitlab.com/haproxy-haptic/haproxy-spoa-hub) — upstream hub binary and SPOP gateway.
- [Chart Values Reference — SPOA Hub Sidecar](../reference.md#spoa-hub-sidecar) — every `spoaHub.*` value.
- [HAProxy versions matrix](./haproxy-versions.md) — supported HAProxy versions for the controller image.
