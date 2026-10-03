# Upgrade notes

Check the notes for every version between your installed version and the version
you plan to deploy. Each section lists configuration changes and the steps needed
to keep your existing routes working. For routine chart upgrades, see
[Upgrading with Helm](deploying-with-helm.md#upgrading).

## Unreleased: Varnish cache permissions

The next chart version uses a bundled non-root Varnish image and grants its
container `IPC_LOCK` to keep shared logs in memory. If you enable
`cache.varnish.enabled`, your cluster policy must allow this capability before
upgrading. Kubernetes Baseline and Restricted Pod Security Standards reject it;
arrange a policy exception with your cluster administrator first.

If you override `cache.varnish.image`, remove the override to use the bundled
image, or supply an image with `cap_ipc_lock=ep` on `varnishd`. See
[shared-log memory](operations/response-cache.md#shared-log-memory).

## Upgrading to 0.3

0.3 renames or removes several values and template features. The chart refuses
to install while your values still use a removed key, and names the
replacement in the error.

### Rename the timeout, SSL redirect, and HSTS keys

Under `controller.config.templatingSettings.extraContext`, rename these keys:

| 0.2 key | 0.3 key |
|---------|---------|
| `timeout_connect` | `timeoutConnect` |
| `timeout_client` | `timeoutClient` |
| `timeout_server` | `timeoutServer` |
| `timeout_http_request` | `timeoutHttpRequest` |
| `timeout_http_keep_alive` | `timeoutHttpKeepAlive` |
| `ssl_redirect_default` | `sslRedirectDefault` |
| `hapticHstsMaxAge` | `tls.hsts.maxAge` |

`sslRedirectDefault` is a boolean. Write `true`, not `"true"`:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        timeoutConnect: "5000"
        sslRedirectDefault: true
```

### Review the HSTS max-age default

`haproxy-haptic.org/hsts` without `haproxy-haptic.org/hsts-max-age` now sends
the `max-age` from `tls.hsts.maxAge`, which defaults to one year (`31536000`)
instead of two. The same value sets the global HSTS header. To keep two years,
set it explicitly:

```yaml
controller:
  config:
    templatingSettings:
      extraContext:
        tls:
          hsts:
            maxAge: "63072000"
```

### Regenerate basic-auth hashes HAPTIC now refuses

Every `auth-secret` annotation, including the nginx-ingress ones, accepts only
bcrypt, SHA-256/SHA-512 crypt, and yescrypt hashes by default. A Secret that
holds a `$apr1$`, `{SHA}`, `$1$`, Data Encryption Standard (DES) crypt, or
plaintext credential stops HAPTIC from applying configuration changes, and the
error names the Ingress, Secret, and user. Before you upgrade, replace each such
hash with one from `htpasswd -nB <user>` or `openssl passwd -6`. See
[accepted formats](operations/security.md#basic-auth-password-hashes), which
also shows how to widen the patterns while you migrate.

### Update templates

- Rename the `http.Fetch` option `delay` to `interval`. A call that still sets
  `delay` fails the render.
- Replace `strings_replace(s, old, new)` with `replace(s, old, new)`.
- `trim` is the `trim(s, cutset)` builtin everywhere. Replace a one-argument
  `trim(s)` with `strip(s)`.

### Update monitoring

The `haptic_events_dropped_total` metric is removed. Query
`haptic_events_dropped_critical_total` instead, which counted the same drops.

### Update custom agent clients

If you call the agent's `/v1/apply` from your own code, set
`identity_version: 1` in the manifest. The agent rejects a manifest without it
with `400`.

### Allow UDP for HTTP/3

0.3 enables [HTTP/3](haproxy-deployment.md#http3-quic) by default. The HAProxy
Service and each Gateway's Service gain a UDP port with the same number as
their HTTPS port (`https-quic` on the HAProxy Service), and HTTPS responses
advertise it with `alt-svc`.

Before upgrading, allow UDP 443 (or your HTTPS port or nodePort) through
firewalls, security groups, and load balancers in front of HAProxy, and check
that your load-balancer implementation accepts Services that mix TCP and UDP
ports. Clients that can't reach UDP fall back to TCP. To upgrade without
HTTP/3, set
`controller.config.templatingSettings.extraContext.http3.enabled: false`.

If you define `haproxy.service.extraPorts` with an entry named `https-quic`,
rename it; the chart rejects the duplicate name.

## Upgrading to 0.2

Use this guide to upgrade from 0.1.0 or a 0.2.0 alpha to a stable 0.2 release. The controller
and chart share one version. The chart replaces the HAProxy Data Plane API with
the HAPTIC agent and changes several values paths.

Confirm that the target version is available on the [releases page](https://gitlab.com/haproxy-haptic/haptic/-/releases)
before running the upgrade commands. Development documentation can describe a
release before its artifacts are published.

### Check requirements

- Kubernetes 1.33 or newer. The HAProxy pod uses native sidecars to keep its
  agent, Stream Processing Offload Agent (SPOA) hub, and log collector running
  until HAProxy exits.
- Capacity for the whole installation and its rolling upgrade. The defaults
  request about 5.4 GiB of memory, rising to 8.1 GiB during rollout. The
  pre-rollout validation Job has a separate 1 GiB limit. Use the
  [resource sizing guidance](operations/performance.md#controller-resource-sizing)
  for larger installations or customized pod resources.
- Access to the controller registry from HAProxy pods: their agent now uses
  the HAPTIC image. Set image-pull credentials if your cluster requires them.

The chart supports HAProxy 3.0–3.4. Keep your selected `haproxyVersion` unless
you intend to change it; the default is 3.4. See
[HAProxy versions](operations/haproxy-versions.md) for runtime-update coverage.

### Save your values

Set the installed Helm release and namespace:

```bash
HAPTIC_RELEASE=haptic
HAPTIC_NAMESPACE=haptic
```

Export the values you supplied to Helm:

```bash
umask 077
helm get values "$HAPTIC_RELEASE" --namespace "$HAPTIC_NAMESPACE" \
  --output yaml > haptic-values-before.yaml
```

Copy the exported values, which include settings supplied with `--set`, into a
working file for the upgrade:

```bash
cp haptic-values-before.yaml haptic-values-0.2.yaml
```

If the export contains only `null`, replace it with an empty map:

```bash
if [ "$(cat haptic-values-0.2.yaml)" = null ]; then
  printf '{}\n' > haptic-values-0.2.yaml
fi
```

Keep `haptic-values-before.yaml` as the record of your previous configuration.
If you manage values in Git, also apply the migrations below to that source so
subsequent deployments keep them.

### Migrate 0.1.0 values

For an alpha installation, some migrations may already be applied. Check the
values you use against the table and review the changed defaults before validation.

For 0.1.0, update the paths you use in `haptic-values-0.2.yaml`:

| Previous value | Replacement |
| --- | --- |
| Root controller workload settings, such as `image`, `replicaCount`, `resources`, `webhook`, `monitoring`, `networkPolicy`, probes, and autoscaling | The same setting under `controller.*` |
| Root pod settings, such as `imagePullSecrets`, `nodeSelector`, `tolerations`, `affinity`, `podAnnotations`, and `podSecurityContext` | The same setting under `controller.podSpec.*` |
| HAProxy pod settings, such as `haproxy.nodeSelector`, `.tolerations`, `.affinity`, `.podAnnotations`, and `.podSecurityContext` | The same setting under `haproxy.podSpec.*` |
| `controller.crdName` | `controller.configName` |
| `controller.debugPort` | `controller.ports.healthz` |
| `controller.defaultSSLCertificate` | `defaultSSLCertificate` |
| `controller.config.dataplane.port` | `haproxy.ports.dataplane` |
| `controller.config.routing.regexMatchOrder` | `controller.config.templatingSettings.extraContext.routing.regexMatchOrder` |
| `controller.templateLibraries.pathRegexLast.enabled: true` | `controller.config.templatingSettings.extraContext.routing.regexMatchOrder: last` |
| `haproxy.dataplane.logLevel`, `.resources`, `.extraEnv`, and `.service` | The same setting under `haproxy.agent.*` |

For example, controller replicas and a custom default certificate now use:

```yaml
controller:
  replicaCount: 2
defaultSSLCertificate:
  secretName: my-default-certificate
  certManager:
    enabled: false
```

Remove Data Plane API-only values (`haproxy.dataplane.validateConfig`,
`debugSocketPath`, `aclFormat`, and `haproxy.dataplaneBin`),
`haproxy.enterprise.version`, and
`controller.config.templatingSettings.extraContext.serverSlots`.
`haproxyVersion` selects the Enterprise revision when Enterprise is enabled.

Remove `spoaHub.plugins.otel`. Configure tracing through
[the tracing values](reference.md#logging-and-templating) instead.

The chart rejects legacy paths with their replacements. Run the validation step
below to find remaining paths, including renamed `extraContext` settings.

### Review changed defaults and integrations

The default IngressClass and GatewayClass names change from `haproxy` to
`haptic`. To preserve routes that use the 0.1.0 defaults, add these values:

```yaml
ingressClass:
  name: haproxy
gatewayClass:
  name: haproxy
```

Keep your existing class names if you already customized them. Alternatively,
update the class references on your Ingress and Gateway resources to `haptic`.

Enable each vendor annotation library that your existing routes use. For example,
to retain support for ingress-nginx annotations:

```yaml
controller:
  templateLibraries:
    nginxIngress:
      enabled: true
```

Use `haproxyIngress.enabled` for `haproxy-ingress.github.io/*` and
`haproxytech.enabled` for `haproxy.org/*` under the same `templateLibraries` map.
The native `haproxy-haptic.org/*` library remains enabled by default.

Review these behavior changes before upgrading:

- Access logs use JSON. Adapt custom log parsers or override the log-format
  snippets; see [access logging](operations/access-logging.md).
- Ingress serves HTTPS using the default certificate. Set
  `controller.config.templatingSettings.extraContext.ingressDefaultHTTPS: false`
  if you require plaintext-only Ingress without an explicit TLS declaration.
- HAProxy response compression is opt-in. If you used an alpha version's automatic
  compression, set `haproxy-haptic.org/compress-enable: "true"` on appropriate
  routes. Avoid compressing responses that combine secrets with attacker-controlled
  input; see [compression settings](libraries/haptic-annotations.md#compression).
- Hash-based balancing uses consistent hashing. Existing key distribution can
  change during the upgrade.
- Request retries that replay a request apply to idempotent methods by default.
  Review custom retry settings before restoring retries for other methods.
- The backend connection timeout defaults to 100 ms instead of 5 s. For slower
  networks, set `controller.config.templatingSettings.extraContext.timeout_connect`
  to `"5000"` to retain the previous timeout in milliseconds.
- HAProxy's HTTP/HTTPS container ports default to 80/443 instead of 8080/8443.
  Update integrations that address pod ports directly, or retain the previous
  ports through `haproxy.ports.http` and `haproxy.ports.https`.

The webhook uses a chart-managed self-signed certificate by default. To retain
cert-manager renewal, set `controller.webhook.certManager.enabled: true` and
keep cert-manager installed. The chart uses a new default webhook Secret name
to avoid conflicting with the old cert-manager-owned Secret.

Update integrations that invoke `haptic-controller` to invoke `haptic`. Replace
Data Plane API integrations with the [agent interface](development/agent.md)
and update dashboards using the [metric migration table](operations/metrics-reference.md#where-the-old-metrics-went).

If you maintain custom templates, replace reads of parsed `currentConfig`
sections with `currentConfig.ServerIndex` for previous servers, and use
`currentServers` in validation fixtures. Use `toJSON` instead of implicit string
conversion for maps and slices. Pass the watched resource object to
[`statusPatch(resource, variants)`](template-reference.md#statuspatch) instead of
separate namespace, name, API version, and kind arguments.

If you process rendered manifests, accept one `HAProxyTemplateConfig` and its
referenced `HAProxyTemplateLibrary` objects. `haptic config view --input` merges
them for inspection. Helm owns these objects; put persistent overrides in your
values file.

### Validate the candidate

Use the `haptic` binary matching the chart version below to run
[preflight validation](operations/validate-before-deploy.md):

```bash
helm pull oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.2 --untar --untardir ./haptic-0.2-chart
haptic preflight --values ./haptic-values-0.2.yaml \
  --chart ./haptic-0.2-chart/haptic --expect-chart-version 0.2.2 \
  --namespace "$HAPTIC_NAMESPACE" --release "$HAPTIC_RELEASE"
```

Keep the default CRD upgrade and pre-rollout validation hooks enabled. If your
deployment manages CRDs separately, apply the target chart's schemas before upgrading:

```bash
helm show crds oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.2 | kubectl apply --server-side --force-conflicts -f -
```

GitOps diff tools can need these CRDs before they can map the new library
resources, because diff runs before Helm's hooks.

### Upgrade the release

Pass the complete migrated values file. `--reset-values` starts from the new
chart's defaults before applying that file, so removed legacy settings aren't
carried forward from the installed release:

```bash
helm upgrade "$HAPTIC_RELEASE" \
  oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace "$HAPTIC_NAMESPACE" --version 0.2.2 \
  --reset-values \
  --values haptic-values-0.2.yaml
```

If validation rejects the candidate, fix the reported value or template and
repeat the upgrade. Keep validation enabled. For rollout failures and rollback
limitations, see [Recover a failed upgrade](deploying-with-helm.md#recover-a-failed-upgrade).

### Verify the deployment

Wait for both controller and HAProxy deployments:

```bash
kubectl --namespace "$HAPTIC_NAMESPACE" get deployments \
  --selector "app.kubernetes.io/instance=$HAPTIC_RELEASE" --output name | \
while read -r deployment; do
  kubectl --namespace "$HAPTIC_NAMESPACE" rollout status "$deployment" --timeout=7m
done
```

Inspect configuration validation and per-pod deployment status. The
`HAProxyTemplateConfig` should report `Validated=True`; check any `False`
condition on `HAProxyCfg` for a rejected configuration or failed deployment:

```bash
kubectl --namespace "$HAPTIC_NAMESPACE" get haproxytemplateconfig,haproxycfg -o yaml
```

Test existing HTTP and HTTPS routes, including authentication and custom
annotations. A successful Helm command alone doesn't prove traffic has converged.
See [troubleshooting](troubleshooting.md) if a pod or route remains unavailable.
