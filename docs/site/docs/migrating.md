# Migrating to HAPTIC

Move Ingresses from **ingress-nginx**, **haproxy-ingress**, or
**haproxytech/kubernetes-ingress** to HAPTIC. Check annotation compatibility,
install HAPTIC alongside the existing controller, and test routes before
transferring production traffic.

With default values, HAPTIC uses the `haptic` IngressClass and its own HAProxy
Service. It watches Ingresses that select that class. Review class names and
watch filters if you've customized an existing HAPTIC installation.

Keep the old controller available until routing and DNS changes are verified.
See [troubleshooting](#troubleshooting) for differences that commonly affect a migration.

## Before you start

- Your incumbent controller (ingress-nginx / haproxy-ingress / haproxytech kubernetes-ingress) is still running and serving traffic. **Leave it running** until cutover is complete.
- You have Helm and cluster access. Step 1 includes commands for a new installation and for an existing HAPTIC release.
- You can edit Ingress manifests (to change `ingressClassName`) or you accept renaming HAPTIC's class to match — see below.

<a id="step-0-check-what-will-change"></a>

## Step 0: Check what changes

Run the migration report before changing routes. It classifies source-controller
annotations as supported, different, or dropped, and renders the Ingress through
HAPTIC's template engine.

It runs in your browser, on your own manifests. Paste the Ingresses you want to
audit into the **Resources** panel — `kubectl get ingress -A -o yaml` output
works as-is — and read the **migration** tab. Nothing leaves your machine and
nothing touches your cluster.

The same report runs live below on a preset ingress-nginx setup:

<div class="pg-embed" markdown data-scenario="nginx-ingress" data-facade="resources" data-input="resources" data-input-focus="nginx.ingress.kubernetes.io/proxy-connect-timeout" data-tab="migration" data-controls="tabs,resources" data-title="ingress-nginx annotation migration report" data-height="440">

<p class="pg-task" markdown>In the **Resources** panel, add <code>nginx.ingress.kubernetes.io/server-snippet: "more_set_headers X-From: nginx;"</code> to the `shop` Ingress, then watch a new **dropped** verdict appear in the **migration** report.</p>

<details class="pg-hint" markdown>
<summary>What to expect</summary>

The report marks `server-snippet` as **dropped** because nginx server-level
directives have no HAProxy equivalent. Replace that behavior before migrating
a route that depends on it.

</details>

</div>

Read the verdicts the same way whatever you paste in:

- **supported** — works the same after migrating.
- **different** — carries over, but behaves differently; read the note.
- **dropped** — has no HAProxy equivalent and doesn't carry over.
- A **render failure** means HAPTIC can't build a configuration for that
  Ingress as-is. Fix those before cutover.

For the full editor — more resources, every template, the rendered HAProxy
config — open the [playground](/playground/).

## The cutover, step by step

!!! warning "Plan the traffic transition"
    Changing `ingressClassName` removes the route from the old controller before
    DNS changes take effect. This procedure can interrupt traffic. Use a test
    Ingress first and schedule production changes for an acceptable interruption
    window. For continuous service, keep a separate route on the old controller
    until clients have moved to HAPTIC.

1. **Install HAPTIC alongside** your existing controller. Give HAProxy a real
   external address. This example uses `LoadBalancer`, which requires a load-balancer
   implementation in your cluster. A NodePort behind your existing load balancer
   also works; see [Service access](haproxy-deployment.md#haproxy-service):

    ```bash
    helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --version 0.2.0-alpha.3 \
      --namespace haptic --create-namespace \
      --set haproxy.service.type=LoadBalancer \
      --set controller.config.templatingSettings.extraContext.statusPatches.enabled=false   # Hold route status until verification
    ```

    If HAPTIC is already installed, apply the same flags with `helm upgrade`:

    ```bash
    helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --version 0.2.0-alpha.3 \
      --namespace haptic --reuse-values \
      --set haproxy.service.type=LoadBalancer \
      --set controller.config.templatingSettings.extraContext.statusPatches.enabled=false   # Hold route status until verification
    ```

2. **Enable the right annotation library** for your source controller — see
   [From ingress-nginx](#from-ingress-nginx),
   [From haproxy-ingress](#from-haproxy-ingress), or
   [From haproxytech/kubernetes-ingress](#from-haproxytechkubernetes-ingress).

3. **Move one test Ingress** to HAPTIC by changing only its class:

    ```bash
    kubectl patch ingress my-test-app \
      --type merge -p '{"spec":{"ingressClassName":"haptic"}}'
    ```

    The incumbent controller drops it; HAPTIC picks it up. Verify routing
    with a local port forward before changing DNS:

    ```bash
    kubectl port-forward --namespace haptic service/haptic-haproxy 8080:80
    ```

    In another terminal, enter the hostname from the test Ingress:

    ```bash
    read -r -p "Test Ingress hostname: " test_hostname
    curl -i --header "Host: $test_hostname" http://127.0.0.1:8080/
    ```

    Check the application response and any authentication, redirects, or headers
    your route requires. Stop the port forward when you finish.

4. **Bulk cut over** once you're confident: change `ingressClassName` on the
   remaining Ingresses (in batches you can roll back).

5. **Enable status writes, then flip DNS.** Turn status patches back on (step 1
   installed with them off) so `external-dns` and dashboards see HAPTIC's address:

    ```bash
    helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      --version 0.2.0-alpha.3 \
      --namespace haptic --reuse-values \
      --set controller.config.templatingSettings.extraContext.statusPatches.enabled=true
    ```

    Then repoint DNS to HAPTIC's load balancer if you manage it manually. Watch
    traffic.

6. **Decommission** the old controller once all Ingresses are served by HAPTIC
   and traffic is stable.

!!! tip "Rolling back"
    Restore the original `ingressClassName` to return route ownership to the old
    controller. If DNS has changed, restore its previous address too and account
    for cached records. Keep the old controller installed until step 6.

## Key settings that affect migration

| Setting | Default | Why it matters |
|---------|---------|----------------|
| `ingressClass.name` | `haptic` | Only Ingresses with this exact `ingressClassName` are served. |
| `ingressClass.default` | `false` | Class-less Ingresses **aren't** adopted; leave `false` during migration. |
| `controller.templateLibraries.hapticAnnotations.enabled` | `true` | Native `haproxy-haptic.org/*` annotations — on by default and the recommended target vocabulary once you've migrated. |
| `controller.templateLibraries.nginxIngress.enabled` | `false` | Opt-in: turn on to keep `nginx.ingress.kubernetes.io/*` annotations working. |
| `controller.templateLibraries.haproxyIngress.enabled` | `false` | Opt-in: turn on to keep `haproxy-ingress.github.io/*` annotations working. |
| `controller.templateLibraries.haproxytech.enabled` | `false` | Opt-in: turn on to keep `haproxy.org/*` annotations working. |
| `controller.config.templatingSettings.extraContext.statusPatches.enabled` | `true` | Writes Ingress/Gateway status. Disable temporarily if a DNS controller would move traffic before verification; this affects all routes in the release. |
| `haproxy.service.type` | `NodePort` | Set to `LoadBalancer` for a routable external address. |

---

## From ingress-nginx

### Match the IngressClass

HAPTIC serves Ingresses whose `spec.ingressClassName` matches its configured
class. For a gradual migration, change each selected Ingress to `haptic`:

```bash
kubectl patch ingress my-test-app --type merge \
  -p '{"spec":{"ingressClassName":"haptic"}}'
```

Keep HAPTIC's class distinct while both controllers run. Reusing an incumbent's
class name also requires transferring ownership of the existing IngressClass;
changing the Helm value alone doesn't perform that transfer.

Keep `ingressClass.default: false` during migration. A
[default IngressClass](https://kubernetes.io/docs/concepts/services-networking/ingress/#default-ingress-class)
is assigned to new Ingresses that omit a class; it doesn't migrate existing
class-less resources.

### Enable the annotation library

```bash
helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 --namespace haptic --reuse-values \
  --set controller.templateLibraries.nginxIngress.enabled=true
```

!!! warning "The flag is `camelCase`"
    It's `nginxIngress`, not `nginx-ingress`. `--set …nginx-ingress.enabled=true`
    silently has no effect.

Enabling it also auto-enables the Coraza Web Application Firewall (WAF) and external-auth plugins in the
[Stream Processing Offload Agent (SPOA) hub sidecar](operations/spoa-hub.md). The gateway library (on by
default) already auto-enables the `mirror` plugin, so a default install may already be running the sidecar.
Basic host/path routing works without this library; only the `nginx.ingress.kubernetes.io/*` annotations need it.

### Control the DNS cutover

Keep `controller.config.templatingSettings.extraContext.statusPatches.enabled=false` until you've verified routing.
With it on, the moment HAPTIC's HAProxy Service has an address it stamps
`.status.loadBalancer` onto every adopted Ingress, and `external-dns`
switches DNS to it — a premature, unverified cutover.

### Metrics

HAPTIC provides compatible request metric names and labels for many
ingress-nginx dashboards. Set the prefix and controller class below, then review
the differences before reusing your alerts:

```yaml
vector:
  requestMetrics:
    prefix: nginx_ingress_controller
    controllerClass: k8s.io/ingress-nginx
```

That reproduces `nginx_ingress_controller_requests` and the six histograms, with
the label set `ingress-nginx` uses — `status`, `method`, `path`, `namespace`,
`ingress`, `service`, `host`, `controller_class`, `controller_namespace`,
`controller_pod`. See [Request metrics](operations/monitoring.md#request-metrics)
for what each family measures.

You gain one label: `term`, HAProxy's termination state, which distinguishes a
backend that refused the connection (`SC--`) from one that timed out without
sending headers (`sH--`) from a client that gave up (`cD--`). Turn it off with
`vector.requestMetrics.terminationStateLabel: false` if the extra cardinality
isn't worth it.

Four differences to expect:

- **Two scrape ports, not one.** Byte sizes and latencies can't share one set of
  histogram buckets, so the size families are exported on `9599` and everything
  else on `9598`. The bundled `PodMonitor` declares both — set
  `haproxy.monitoring.podMonitor.enabled: true`.
- **No `canary` label.** HAPTIC has no per-request canary marker. PromQL
  `{canary=""}`, which the stock dashboards use, matches a series without the
  label, so those queries are unaffected.
- **Different bucket boundaries.** Durations are a strict superset of
  `--time-buckets`, so a query hardcoding `le="0.5"` still resolves. Sizes
  use different buckets. Queries that name a particular size bucket may need
  updating; `_sum` and `_count` are unaffected. To use these ingress-nginx
  bucket lists:

    ```yaml
    vector:
      requestMetrics:
        durationBuckets: [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]
        sizeBuckets: [10, 20, 30, 40, 50, 60, 70, 80, 90, 100]
    ```

- **`request_size` reads lower.** It counts request **body** bytes; nginx's
  `$request_length` also counts the request line and headers. HAProxy exposes no
  headers-inclusive counter.

!!! warning "These are counted from the access log, not in the data path"
    They report fewer requests than were served whenever HAProxy drops log records under back-pressure. Keep
    the `HAProxyAccessLogRecordsDropped` alert on. If a request count has to stay
    exact through a drop, set
    `extraContext.prometheusExporter.excludeMetrics.httpRequestCounters.enabled: false`
    to keep HAProxy's own counters alongside these.

### Annotation support

The library covers the common `nginx.ingress.kubernetes.io/*` annotations —
backend timeouts, `load-balance`, `proxy-body-size`, `rewrite-target`,
`ssl-redirect`/`force-ssl-redirect`, HTTP Strict Transport Security (HSTS), Cross-Origin Resource Sharing (CORS), `whitelist`/`denylist-source-range`,
custom headers, basic + external auth, `ssl-passthrough`, canary, client mTLS,
request mirroring (`mirror-target`), and ModSecurity. Full per-annotation reference:
[nginx-ingress library docs](libraries/nginx-ingress.md).

See [differences from ingress-nginx](annotation-compatibility.md#ingress-nginx) before moving routes.

---

## From `haproxy-ingress`

Enable the compatibility library on your existing HAPTIC release:

```bash
helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 --namespace haptic --reuse-values \
  --set controller.templateLibraries.haproxyIngress.enabled=true
```

Review the linked annotation differences, [match the IngressClass](#match-the-ingressclass),
and [control the DNS cutover](#control-the-dns-cutover). You can then migrate to
native `haproxy-haptic.org/*` annotations at your own pace.

Most routing, SSL, session-affinity, redirect, HSTS, CORS, access-control,
basic/external auth, client-mTLS, and WAF annotations are supported. Full
reference:
[haproxy-ingress library docs](libraries/haproxy-ingress.md).

See [differences from haproxy-ingress](annotation-compatibility.md#haproxy-ingress) before moving routes.

---

## From `haproxytech/kubernetes-ingress`

Enable the compatibility library on your existing HAPTIC release:

```bash
helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 --namespace haptic --reuse-values \
  --set controller.templateLibraries.haproxytech.enabled=true
```

Review the linked annotation differences, [match the IngressClass](#match-the-ingressclass),
and [control the DNS cutover](#control-the-dns-cutover).

HAPTIC reads these annotations on **Ingress** resources only. haproxytech's
controller also reads many of them on Service and ConfigMap resources; that
Service/ConfigMap-level configuration doesn't carry over. Full reference:
[haproxytech library docs](libraries/haproxytech.md).

See [differences from HAProxy Technologies](annotation-compatibility.md#haproxytech) before moving routes.

---

## Troubleshooting

Check these settings when a migrated route behaves differently:

- **Existing Ingresses aren't being routed.** HAPTIC only serves Ingresses whose
  `spec.ingressClassName` equals `ingressClass.name` (default **`haptic`**) —
  an `ingressClassName: nginx` Ingress is filtered out before it reaches the
  controller's store. Fix: [match the IngressClass](#match-the-ingressclass).

- **Annotations seem to be ignored.** All three vendor compatibility libraries
  (`nginx.ingress.kubernetes.io/*`, `haproxy-ingress.github.io/*`, `haproxy.org/*`)
  are **disabled by default** — only the native `haproxy-haptic.org/*` library is
  on. A vendor annotation (timeouts, auth, CORS, rate-limits, redirects) is a
  silent no-op until you enable its matching library. Fix: [enable the annotation
  library](#enable-the-annotation-library) for the controller you're migrating from.

- **DNS cut over before you were ready.** Ingress status writes are **on by
  default** — install with them off and enable them only after you've verified
  routing. Fix: [control the DNS cutover](#control-the-dns-cutover).

For symptoms beyond these three, see the general
[troubleshooting](troubleshooting.md) guide.

## See also

- [Getting Started](getting-started.md) — install HAPTIC and route your first Ingress.
- [Playground](/playground/) — render an annotated Ingress live and see per-annotation migration warnings.
- [Watching Resources](watching-resources.md) — how `ingressClassName` scoping and field selectors work.
