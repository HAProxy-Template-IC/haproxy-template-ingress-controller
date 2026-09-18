# Validate a configuration before you deploy it

Run `haptic preflight` to check your chart values before deployment. Helm runs it
by default during installation and upgrades. Adding it to your delivery pipeline
finds the same errors before you apply the release.

## Why the chart's own tests aren't enough

Your values can enable different features, templates, and sidecars from the chart's
defaults. Validate that combination with the version you plan to deploy.

The controller refuses to start with configuration that fails its load checks.
The admission webhook validates routing resources, not the complete
`HAProxyTemplateConfig` and library set. Preflight checks that set together and
can also validate Vector and Varnish configuration, whose failures may leave
traffic serving while logging or caching is unavailable.

## Run the check

`haptic preflight` renders the chart with your values and runs the
same checks the controller runs on startup:

```bash
haptic preflight \
  --values ./haptic-values.yaml \
  --namespace haptic \
  --release haptic
```

It exits non-zero if the configuration wouldn't load. Wire it into your
pipeline before the step that applies the chart — any runner works, these are
plain commands with no runner-specific syntax:

```bash
haptic preflight --values ./haptic-values.yaml --namespace haptic
helm upgrade --install haptic <chart> -n haptic -f ./haptic-values.yaml
```

The chart is embedded in the controller image, so the check renders the chart
that image was built with. Pass `--chart` to render a different one.

!!! note "The chart already runs this for you"
    `preRolloutValidation.enabled` defaults to `true`, so `helm install` and
    `helm upgrade` run this same `preflight` as a `pre-install`/`pre-upgrade`
    hook Job against the release's own values. Running it yourself in a pipeline
    moves the same failure earlier — before anything reaches the cluster.

### What it checks

| Check | Catches |
|---|---|
| Structural validation and the bundled `validationTests`, including `haproxy -c` | A configuration the controller would refuse to load |
| `vector validate` on the rendered sidecar config | A malformed document **or** a broken transform that would keep the supervised Vector child unavailable |
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
| `--expect-chart-version` | `$HAPTIC_EXPECT_CHART_VERSION` | Fail unless the chart being rendered carries exactly this version. Set it to the version you're about to install, so a drifted controller image tag fails loudly instead of validating the wrong chart |

Set `HAPTIC_CONTAINER_RUNTIME` to choose a runtime (default: `docker`, then
`podman`). Preflight selects Vector's image from the rendered Helm workload
and Varnish's image from the workload mounting each VCL ConfigMap. It checks
every distinct config and image pair, including variants with the same file name.

Set image overrides in your Helm values so validation and deployment use the
same images. If you set `HAPTIC_VECTOR_IMAGE` or `HAPTIC_VARNISH_IMAGE`, it must
equal the corresponding rendered image. A mismatch or an unresolved image
fails the check.

### Schemas

Preflight requires schemas to resolve typed fields and determine which optional
resources and features are available. It fails if it can't load a schema source.

By default, it reads schemas from the target cluster using your Kubernetes
credentials. This includes the custom resource definitions (CRDs) and schema
versions installed there.

For offline validation, pass `--schema-dir` with CRD manifests or OpenAPI v3
schemas. Keep this directory aligned with the target cluster; preflight can't
verify that alignment without cluster access.

```bash
# Offline: schemas from a directory, no cluster contacted.
haptic preflight --values ./haptic-values.yaml --schema-dir ./schemas
```

## Getting the binary

The `haptic` binary ships in the controller image at
`/usr/local/bin/haptic`, together with the chart. The simplest
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
docker cp "$id:/usr/local/bin/haptic" ./haptic
docker cp "$id:/usr/share/haptic/chart" ./chart
docker rm "$id"

./haptic preflight --values ./haptic-values.yaml --chart ./chart
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

It also doesn't say what deploying the configuration does to a running pod.
`haptic diff` does: it compares your candidate with what a pod runs and prints
`runtime`, `file_only` or `reload`, with a reason for every change that can't
run at runtime.

```bash
haptic diff -f candidate.yaml
```

See [Debugging — Common recipes](./debugging.md#common-recipes) for the
pod-to-file and file-to-file forms.
