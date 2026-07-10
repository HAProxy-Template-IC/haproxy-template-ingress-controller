# Bring your own CRD

HAPTIC's defining feature: it can route on, and report status back to, **any**
Kubernetes resource — including a CRD you invented — with **zero Go code**. The
controller is resource-agnostic; everything resource-specific lives in the
`HAProxyTemplateConfig` template. Ingress and Gateway API are just bundled
examples of this pattern, not special cases.

This example proves it end to end with a made-up `WebApp` CRD.

## Files

| File | What it is |
|------|------------|
| [`crd.yaml`](./crd.yaml) | A tiny `WebApp` CRD (`examples.haptic.dev/v1`) — `host`, `serviceName`, `servicePort`. Nothing HAPTIC-specific. |
| [`haproxytemplateconfig.yaml`](./haproxytemplateconfig.yaml) | The whole integration: watch WebApps → render a backend + host map per app → write an `Accepted` condition back. Includes a `validationTests` block so it self-verifies. |
| [`webapp.yaml`](./webapp.yaml) | A sample `WebApp` instance. |

The `HAProxyTemplateConfig` does three things, each labelled in the file:

1. **Watch** the CRD (`spec.watchedResources.webapps`) — the controller learns
   the shape from the apiserver (or `--schema-dir` offline); no Go knows `WebApp`
   exists.
2. **Route** on it (`spec.haproxyConfig` + `spec.maps`) — a `host → backend` map
   and one `backend` per WebApp, using generic `dig()` access that works whether
   or not a schema is loaded.
3. **Report status** (`statusPatch()`) — an `Accepted` condition written back to
   the WebApp's `/status` via Server-Side Apply, with phase-keyed variants
   (`deployed` / `deployFailed`).

## Try it offline (no cluster)

The config carries its own validation test, so you can prove the whole flow with
just the binary and a local `haproxy`:

```bash
make build
./bin/haptic-controller validate -f examples/byo-crd/haproxytemplateconfig.yaml
```

You should see `test-webapp-routing` pass: it renders the config from the WebApp
fixture, runs `haproxy -c`, and checks the backend, the host map, and the
`Accepted` status patch. (This is also what keeps the example honest — if the
engine changes, the test catches it.)

The templates use `dig()`, which is **schema-agnostic** — it navigates both the
untyped maps you get with no schema and the typed structs you get with one, so
this example needs no `--schema-dir`. That makes `dig()` the most portable choice
for a custom CRD: a CRD's structural schema can't declare `metadata` fields
(`name`, `generation`, …), so typed dot-access to metadata isn't available, but
`dig("metadata", "name")` works regardless. For the typed dot-field style on
fields a schema *does* declare (`wa.Spec.Host`), and when to prefer it, see
[Typed Resource Access](../../docs/site/docs/templating.md#typed-resource-access).

## Try it on a cluster

```bash
# 1. Install the CRD and a sample app
kubectl apply -f examples/byo-crd/crd.yaml
kubectl create namespace demo
kubectl apply -f examples/byo-crd/webapp.yaml

# 2. Point a running HAPTIC controller at this config (or fold these
#    watchedResources / haproxyConfig / maps into your Helm values under
#    controller.config). Then:
kubectl get webapp shop -n demo
# NAME   HOST               ACCEPTED
# shop   shop.example.com   True      <-- written back by statusPatch()
```

## Adapting it to your CRD

Change `watchedResources.webapps` to your `apiVersion`/`resources`, and swap the
field paths in the template (`spec.host`, `spec.serviceName`, …) for yours.
That's the entire change — no controller rebuild, no Go.
