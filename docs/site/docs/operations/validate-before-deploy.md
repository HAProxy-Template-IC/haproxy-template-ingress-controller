# Validate a configuration before you deploy it

HAPTIC checks a configuration before it serves traffic with it, and refuses to
start on one that fails. That's deliberate — a controller that quietly served a
broken configuration would be worse — but a bad configuration surfaces as a
**crash-looping controller** rather than as a failed deploy.

Run the same checks in your delivery pipeline and the failure lands where you
want it: on the pipeline, before anything reaches the cluster.

## Why the chart's own tests aren't enough

The chart ships `validationTests` and they run in HAPTIC's CI — against the
chart's **default** values. Your `values.yaml` enables features the defaults
don't, and some checks only fail once a feature is on. Nothing outside your
pipeline has ever seen your values.

Three things make this worth an explicit step:

- The controller's load gate is **fail-closed**. A configuration that fails it stops the controller from starting at all.
- The admission webhook does validate on apply, but its failure policy is deliberately permissive so a broken webhook can't block your recovery, and it defers some checks during a rolling upgrade — exactly when you deploy.
- Sidecar configuration fails **quietly**. A rejected Vector config leaves the sidecar on its bootstrap configuration with no metrics endpoint, so the pod never becomes ready and the rollout stalls with no error to read.

## Run the check

`haptic-controller preflight` renders the chart with your values and runs the
same checks the controller runs on startup:

```bash
haptic-controller preflight \
  --values ./haptic-values.yaml \
  --namespace haptic \
  --release haptic
```

It exits non-zero if the configuration wouldn't load. Wire it into your
pipeline before the step that applies the chart — any runner works, these are
plain commands with no runner-specific syntax:

```bash
haptic-controller preflight --values ./haptic-values.yaml --namespace haptic
helm upgrade --install haptic <chart> -n haptic -f ./haptic-values.yaml
```

The chart is embedded in the controller image, so the check renders the chart
that image was built with. Pass `--chart` to render a different one.

### What it checks

| Check | Catches |
|---|---|
| Structural validation and the bundled `validationTests`, including `haproxy -c` | A configuration the controller would refuse to load |
| `vector validate` on the rendered sidecar config | A malformed document **or** a broken transform — the sidecar would keep its bootstrap config and never become ready |
| `varnishd -C` on the rendered Varnish Configuration Language (VCL) | A VCL that doesn't compile, which leaves the cache pod in `CrashLoopBackOff` |

The last two run the real Vector and Varnish binaries in containers, so they
need a container runtime. Without one they're skipped with a warning; the load
gate always runs.

### Command-line flags

| Flag | Default | Meaning |
|---|---|---|
| `--values`, `-f` | *required* | Your values file. The whole point is your values, not the defaults. Repeatable — later files win, as with `helm -f` |
| `--namespace`, `-n` | `haptic` | Release namespace. Use the one you deploy to — chart output depends on it |
| `--release` | `haptic` | Release name, which resource names are derived from |
| `--chart` | image-embedded chart | Chart directory, then `$HAPTIC_CHART_DIR`, then the copy inside the controller image |
| `--kubeconfig` | `$KUBECONFIG`, then in-cluster | Which cluster to read API schemas from — see [Schemas](#schemas) |
| `--schema-dir` | `$HAPTIC_SCHEMA_DIR` | Read schemas from a directory instead of the cluster, for running fully offline |
| `--api-versions` | — | Extra API versions your cluster serves. The Gateway API `GatewayClass` version is always declared, so the Gateway library renders the same way it does in the cluster |

Environment overrides: `HAPTIC_CONTAINER_RUNTIME` (default: `docker`, then
`podman`), `HAPTIC_VECTOR_IMAGE`, `HAPTIC_VARNISH_IMAGE`.

### Schemas

Templates that use typed resource access need the Kubernetes API schemas.
Without them the render falls back to untyped access and validates a
*different* configuration than the controller loads, so `preflight` always
reads schemas from somewhere and refuses rather than passing on a weaker
check.

By default it reads them from the cluster you're deploying to — the same
credentials the deploy step uses. That's the most faithful source: it reflects
that cluster's actual API surface, including which optional custom resource
definitions (CRDs) are installed, so a Gateway API feature is validated when
Gateway API is there and stripped when it isn't.

To run without cluster access, point `--schema-dir` at a directory of CRD
manifests or OpenAPI v3 schemas. A directory only describes what somebody put
in it, so it can't tell you what your cluster actually serves — use it when
the pipeline has no credentials, not as the default.

```bash
# Offline: schemas from a directory, no cluster contacted.
haptic-controller preflight --values ./haptic-values.yaml --schema-dir ./schemas
```

## Getting the binary

`haptic-controller` ships in the controller image at
`/usr/local/bin/haptic-controller`, together with the chart. The simplest
pipeline step runs the image directly:

```bash
docker run --rm --user "$(id -u):$(id -g)" \
  -v "$PWD:/w" -w /w \
  -v ~/.kube/config:/kube/config -e KUBECONFIG=/kube/config \
  <haptic-image> preflight --values haptic-values.yaml --namespace haptic
```

`--user` matters: the image runs as its own non-root user, which can't read a
kubeconfig owned by someone else. Running as the user that owns the file avoids
a permission error that looks like a missing cluster.

That covers the load gate. The Vector and Varnish checks start containers of
their own, so they're skipped inside a container without access to a runtime —
run the binary on the pipeline host to get all three:

```bash
id=$(docker create <haptic-image>)
docker cp "$id:/usr/local/bin/haptic-controller" ./haptic-controller
docker cp "$id:/usr/share/haptic/chart" ./chart
docker rm "$id"

./haptic-controller preflight --values ./haptic-values.yaml --chart ./chart
```

Match the binary to the version you're about to deploy. Validating with one
version and deploying another checks the wrong thing.

## What it doesn't cover

The check renders against your values, not against your cluster's live
Ingresses, Gateways, and Services. It proves the configuration is loadable and
that the generated sidecar and cache configurations are valid; it doesn't
predict what a future routing resource renders to.

For that, keep the admission webhook enabled — it validates each watched
resource as it's applied, and rejects one that would break the rendered
configuration.
