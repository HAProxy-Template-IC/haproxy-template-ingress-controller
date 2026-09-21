# Upgrade to HAPTIC 0.2

Use this guide to upgrade from 0.1.0 or a 0.2.0 alpha to 0.2.0. The controller
and chart share one version. The chart replaces the HAProxy Data Plane API with
the HAPTIC agent and changes several values paths.

Confirm that 0.2.0 is available on the [releases page](https://gitlab.com/haproxy-haptic/haptic/-/releases)
before running the upgrade commands. Development documentation can describe a
release before its artifacts are published.

## Check requirements

- Kubernetes 1.33 or newer. The HAProxy pod uses native sidecars to keep its
  agent, Stream Processing Offload Agent (SPOA) hub, and log collector running
  until HAProxy exits.
- Capacity for the chart's memory defaults: 1 GiB per controller replica and a
  1 GiB limit for the pre-rollout validation Job. Size larger installations using
  the [resource sizing guidance](operations/performance.md#controller-resource-sizing).
- Access to the controller registry from HAProxy pods: their agent now uses
  the HAPTIC image. Set image-pull credentials if your cluster requires them.

The chart supports HAProxy 3.0–3.4. Keep your selected `haproxyVersion` unless
you intend to change it; the default is 3.4. See
[HAProxy versions](operations/haproxy-versions.md) for runtime-update coverage.

## Save your values

Set the installed Helm release and namespace:

```bash
HAPTIC_RELEASE=haptic
HAPTIC_NAMESPACE=haptic
```

Export the values you supplied to Helm:

```bash
helm get values "$HAPTIC_RELEASE" --namespace "$HAPTIC_NAMESPACE" \
  --output yaml > haptic-values-before.yaml
```

If you already maintain a values file, copy that file to `haptic-values-0.2.yaml`.
Otherwise, copy the exported values:

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

## Migrate 0.1.0 values

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

## Review changed defaults and integrations

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
  snippets; see [monitoring](operations/monitoring.md).
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
and update dashboards using the [metric migration table](operations/monitoring.md#where-the-old-metrics-went).

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

## Validate the candidate

Use the 0.2.0 `haptic` binary and its chart to run
[preflight validation](operations/validate-before-deploy.md):

```bash
haptic preflight --values ./haptic-values-0.2.yaml \
  --namespace "$HAPTIC_NAMESPACE" --release "$HAPTIC_RELEASE"
```

Keep the default CRD upgrade and pre-rollout validation hooks enabled. If your
deployment manages CRDs separately, apply the 0.2.0 schemas before upgrading:

```bash
helm show crds oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0 | kubectl apply --server-side --force-conflicts -f -
```

GitOps diff tools can need these CRDs before they can map the new library
resources, because diff runs before Helm's hooks.

## Upgrade the release

Pass the migrated values explicitly:

```bash
helm upgrade "$HAPTIC_RELEASE" \
  oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --namespace "$HAPTIC_NAMESPACE" --version 0.2.0 \
  --values haptic-values-0.2.yaml
```

If validation rejects the candidate, fix the reported value or template and
repeat the upgrade. Keep validation enabled.

## Verify the deployment

Wait for both controller and HAProxy deployments:

```bash
kubectl --namespace "$HAPTIC_NAMESPACE" get deployments \
  --selector "app.kubernetes.io/instance=$HAPTIC_RELEASE" --output name | \
while read -r deployment; do
  kubectl --namespace "$HAPTIC_NAMESPACE" rollout status "$deployment" --timeout=7m
done
```

Inspect configuration validation and per-pod deployment status:

```bash
kubectl --namespace "$HAPTIC_NAMESPACE" get haproxytemplateconfig,haproxycfg -o yaml
```

Test existing HTTP and HTTPS routes, including authentication and custom
annotations. A successful Helm command alone doesn't prove traffic has converged.
See [troubleshooting](troubleshooting.md) if a pod or route remains unavailable.
