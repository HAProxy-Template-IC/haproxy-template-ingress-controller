# Diagnose a HAPTIC fleet

`haptic doctor` requires HAPTIC 0.2.0 or a development build containing the
command. For 0.2.0-alpha.3 or earlier, use the
[pod and log checks](../troubleshooting.md).

Run `haptic doctor` from a machine with cluster access to check configuration
validation, controller phases, and deployment state on every current HAProxy pod.
[Install the CLI](../cli.md) from the same release or development build as the controller.

## Collect a report

1. Check the default Helm installation:

   ```bash
   haptic doctor --namespace haptic
   ```

   An exit status of 0 means the required checks passed. Status 1 means a check
   failed or required evidence was unavailable. Historical failure events remain in the
   report even after recovery; they don't make the current fleet unhealthy.

2. Save a support bundle:

   ```bash
   bundle="haptic-support-$(date -u +%Y%m%dT%H%M%SZ).zip"
   haptic doctor --namespace haptic --bundle "$bundle"
   ```

   The ZIP contains `report.json` and a description of the collection limits.
   The command creates it with owner-only permissions and refuses to overwrite
   an existing file. It also writes the bundle when health checks fail.

3. Inspect the report before sharing it:

   ```bash
   unzip -p "$bundle" report.json
   ```

   Reports include resource names, UIDs, image references, condition reasons,
   checksums, plan IDs, worker identity, restart counts, and failure correlation
   IDs. These identifiers can reveal information about your infrastructure.
   Reports omit Secret values, rendered configuration, templates, logs, event
   payloads, arbitrary error messages, environment variables, and kubeconfig
   credentials.

## Select another installation

For a release named `edge`, using a configuration named `edge-config`:

```bash
haptic doctor --kubeconfig "$HOME/.kube/config" --namespace edge \
  --release edge --crd-name edge-config --output json
```

`--crd-name` names the `HAProxyTemplateConfig` object set by
`controller.configName` in Helm. Its default is `haptic-config`.
`--release` selects pods by their Helm release and component labels; its default
is `haptic`. Without `--namespace`, the command uses the kubeconfig context or
in-cluster service account namespace.

## Interpret findings

`healthy` covers configuration validation, controller phases, agent workers,
and desired-versus-deployed checksums and plan IDs. `complete` records whether
all required observations were available. Missing permissions, endpoints, or
library revisions produce an incomplete report; they don't imply health.

A reconciliation or rolling update can change state while the report is
collected. For a mismatch, wait for reconciliation to finish,
then rerun the command. Persistent findings identify the pod and a next action.

Agent protocol or operation mismatches appear as warnings when the controller
can use reload fallback. Stopped workers, missing applied plans, failed applies,
and reported invariant violations fail the health check.

Watched-resource conditions appear without their messages, including nested
conditions on custom resources. Their meaning depends on the resource's API:
`False` can describe either a healthy or unhealthy state. These conditions don't
change the fleet health verdict automatically.

## Permissions and collection limits

Your Kubernetes identity needs:

- Read access to the selected `HAProxyTemplateConfig`, its referenced
  `HAProxyTemplateLibrary` objects, and the published `HAProxyCfg`.
- Permission to list the release's pods and to get or create their
  `pods/portforward` and `pods/exec` endpoints.
- Permission to list the configured watched resource APIs across namespaces.

The command reaches controller diagnostics through Kubernetes port forwarding
and executes `haptic agent state --verify` inside agent containers. It doesn't
require direct network access to management ports or access to TLS private keys.

The default deadline is two minutes. Collection inspects at most 10,000 watched
resources and accepts at most 8 MiB per controller or agent response. A limit
produces an incomplete report. For a larger installation:

```bash
haptic doctor --namespace haptic --timeout 5m \
  --max-resources 50000 --max-response-bytes 33554432
```

## Investigate private details

Use the reported pod and correlation ID with the
[debug server](../development/debug-endpoints.md#event-search-debugevents), or inspect that pod's logs
and `haptic agent state --verify` output. These sources can contain configuration
contents, internal addresses, and application data. Keep them private and review
specific excerpts before sharing them.

For the default release, view a controller's recent logs:

```bash
kubectl logs --namespace haptic --selector app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller \
  --container controller --tail=100
```
