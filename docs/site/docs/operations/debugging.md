# Inspect configuration and deployment

<a id="debugging"></a>

Start with [fleet diagnostics](diagnostics.md) to identify a failed validation
or deployment. Use the commands below to inspect its configuration and the
HAProxy pod involved. They assume release `haptic` in namespace `haptic`.

Output can contain certificate keys, configuration, and application data.
Keep exported files private and review excerpts before sharing them.

## Inspect the generated configuration

Read the latest published HAProxy configuration:

```bash
umask 077
kubectl exec -n haptic deployment/haptic-controller -c controller -- \
  haptic config view > current.cfg
```

This is the controller's output; it doesn't prove every HAProxy pod has applied
it. Check [deployment health](diagnostics.md) and test the route. To change it,
edit your templates or Helm values rather than the generated `HAProxyCfg`.

## Inspect the active templates

Export the complete input, including all referenced template libraries:

```bash
umask 077
kubectl exec -n haptic deployment/haptic-controller -c controller -- \
  haptic config view --input > current-input.yaml
```

If a snippet differs from your values, check the startup logs for
`Template snippet overridden by a later config`. The message names the
snippet and the objects defining it. See [library merge order](../template-libraries.md#library-merge-order).

<a id="common-recipes"></a>

## Check whether a change needs a reload

Use the `haptic` CLI from your controller release and prepare the
[offline schemas](../validation-tests.md#prepare-schemas). Compare two complete
configuration files:

```bash
haptic diff --from current-input.yaml --to candidate.yaml --schema-dir ./schemas
```

The result is `runtime`, `file_only`, or `reload`, with reasons and planned
commands. A successful comparison exits with status 0, including a `reload`
result. Use `--output json` for automation.

By default, the comparison renders without watched resources. Add
`--test <name>` to use a validation test's fixtures on both sides. Use `--all`
to list every planned operation.

## Inspect one HAProxy pod

Select a pod and verify its current files and deployment state:

```bash
POD=$(kubectl get pod -n haptic \
  -l app.kubernetes.io/instance=haptic,app.kubernetes.io/component=loadbalancer \
  -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n haptic "$POD" -c agent -- haptic agent state --verify
```

The last apply result identifies a failed stage and HAProxy's error. A different
`running` and `applied` plan can result from runtime updates since the last reload;
check `reload_pending_at` for a scheduled reload. Add `--files` to list the files
with their sizes and checksums.

<a id="haproxy-refused-the-config-the-fleet-was-given-configvalidatedfalse"></a>

## Resolve a rejected configuration

For `ConfigValidated=False`, read HAProxy's error from the published configuration:

```bash
kubectl get haproxycfg -n haptic -o jsonpath='{.items[0].status.conditions}' | jq '.'
```

The controller restores the last accepted files on affected pods. Fix the
input named in the error; a successful render clears the condition and deploys
the new configuration.

`ConfigPinned=True` means two consecutive renders were refused. New output is
held until the input changes. Read `ConfigValidated` for the reason and inspect
the affected pod with `haptic agent state --verify`.

## Find a template error

Read recent controller logs:

```bash
kubectl logs --namespace haptic \
  --selector app.kubernetes.io/instance=haptic,app.kubernetes.io/component=controller \
  --container controller --tail=100
```

For errors in your own templates, run [validation tests](../validation-tests.md).
Add `--trace-templates` to list rendered snippets and their timings. In the
[browser playground](https://haproxy-haptic.org/playground/), the **provenance**
control links an output line to its template. That line mapping isn't available
in the running controller.

If resources change but no render follows, check [watch selectors](../watching-resources.md#narrowing-the-watch)
and logs for watch errors. For slow rendering or high memory use, follow
[resource sizing](performance.md#when-to-adjust-the-budget).

<a id="security-reminders"></a>
<a id="accessing-the-server"></a>
<a id="debug-variables"></a>
<a id="event-search-debugevents"></a>
<a id="health-checks-during-configuration-changes"></a>
<a id="go-profiling"></a>
<a id="see-also"></a>

For a controller bug investigation, the [developer debug reference](../development/debug-endpoints.md)
covers raw endpoints, event correlation, and profiling.
