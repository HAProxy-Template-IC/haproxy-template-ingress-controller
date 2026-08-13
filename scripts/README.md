# scripts/

Shell helpers used by developers and by `Makefile` targets. Everything here is meant to be runnable from the repo root. Authoritative behaviour lives in the scripts themselves — run with `--help` where supported.

| Script | Purpose | Typical caller |
|--------|---------|----------------|
| [`start-dev-env.sh`](#start-dev-envsh) | Local Kind-based development environment | developers |
| [`test-templates.sh`](#test-templatessh) | Run the chart's `validationTests` against a merged Helm render | developers, CI |
| [`test-helm-defaults.sh`](#test-helm-defaultssh) | Validate the chart with default values | developers, CI |
| [`test-benchmark.sh`](#test-benchmarksh) | Go benchmark runner with consistent flags | developers |
| [`generate-dev-ssl-cert.sh`](#generate-dev-ssl-certsh) | Self-signed SSL cert used by the dev environment | `start-dev-env.sh` |
| [`source-hash.sh`](#source-hashsh) | Hash of controller build inputs (dev-env sync check) | `start-dev-env.sh status` |
| `verify-source-hash.sh` | Reject a stale or missing controller source identity | Controller build paths |
| [`extract-dataplane-spec.sh`](#extract-dataplane-specsh) | Download Dataplane API OpenAPI spec for a given HAProxy version | code generation |
| [`fetch-k8s-openapi-schemas.sh`](#fetch-k8s-openapi-schemassh) | Fetch K8s built-in resource schemas (Namespace, Service, Secret, EndpointSlice, Ingress) from a running cluster's OpenAPI v3 endpoint and emit CRD-wrapped YAML under `tests/schemas/` | developers, schema refresh |
| [`release.sh`](#releasesh) | Release automation (controller + chart, one version) | `make release` |
| `dev-env-assets/` | Static files (CRD, kind config, manifests) used by `start-dev-env.sh` | `start-dev-env.sh` |

---

## start-dev-env.sh

Manages a Kind-based local development environment. Subcommands:

| Subcommand | Effect |
|------------|--------|
| *(none)* / `up` | Create or attach to the `kind-haptic-dev` cluster, build + deploy the controller |
| `restart` | Rebuild the controller image, redeploy |
| `logs` | `kubectl logs -f` the controller |
| `exec` | Open an interactive shell inside the controller pod |
| `status` | Show controller/HAProxy pod status + running-vs-local source-hash |
| `metrics` | Curl the controller's `/metrics` endpoint via port-forward |
| `port-forward` | Port-forward an HAProxy pod's listener for local curling |
| `test` | Run a basic ingress smoke check via curl |
| `down` / `clean` | Delete the Kind cluster |

`up --skip-build` and `restart --skip-build` reuse an existing controller image instead of rebuilding (handy when iterating on chart-only changes). `status` uses `source-hash.sh` to show whether the cluster is running your latest code — always run it before debugging.

## test-templates.sh

Runs the validation tests embedded in the chart's libraries. Equivalent to:

1. `helm template charts/haptic …` (with `--api-versions` for Gateway API)
2. Extract the single `HAProxyTemplateConfig` with `yq`
3. `haptic-controller validate -f <merged>.yaml [--test <name>] [--dump-rendered] [--verbose]`

```bash
./scripts/test-templates.sh                                   # all tests
./scripts/test-templates.sh --test test-httproute-basic       # one test
./scripts/test-templates.sh --dump-rendered --verbose         # debug failing test
./scripts/test-templates.sh --output yaml | yq '.tests[].name'  # list available tests
```

Always prefer this over running `haptic-controller validate` against a library file directly — library files are incomplete (no `haproxyConfig`, no cross-library snippets) and will fail validation on their own.

## test-helm-defaults.sh

Renders the chart with default values and asserts key invariants — useful as a quick "does the chart still produce valid output after my change?" check. Runs in CI on every MR.

## test-benchmark.sh

Wrapper around `go test -bench=...` that picks the right `-count`, `-benchtime`, and `-run` defaults for this project's benchmarks, and teeing output to a file for comparison.

## generate-dev-ssl-cert.sh

Generates a self-signed SSL certificate plus matching key for the dev environment. Called automatically by `start-dev-env.sh`; rarely run directly.

## source-hash.sh

Emits a short hash of the controller's Go source, module files, and profile-guided optimization profile. `start-dev-env.sh status` uses it to decide whether the controller running in the dev cluster matches the local source inputs. Deterministic — the same inputs produce the same hash across runs.

## extract-dataplane-spec.sh

Downloads the OpenAPI v3 specification for a given HAProxy Dataplane API version by spinning up a throwaway Docker container, querying `/v3/specification_openapiv3`, and saving the response as formatted JSON.

```bash
./scripts/extract-dataplane-spec.sh 3.2
./scripts/extract-dataplane-spec.sh 3.1 /tmp/dataplane-v31.json
./scripts/extract-dataplane-spec.sh 3.0 pkg/generated/dataplaneapi/v30/spec.json
```

Requirements: Docker, `curl`, `jq`, `nc`. The script handles container lifecycle itself — if you see cleanup errors, remove the container manually with `docker rm -f dataplaneapi-extract-<version>`.

Used for code generation — after saving a new spec, run `make generate-dataplaneapi-v<version>` to regenerate the typed Go client under `pkg/generated/dataplaneapi/`.

## fetch-k8s-openapi-schemas.sh

Refreshes the K8s built-in schemas under `tests/schemas/` from a running cluster. These power offline typed-watched-resources access (`controller validate --schema-dir tests/schemas`) for the K8s built-ins the chart watches alongside its CRDs — Namespace, Service, Secret, ConfigMap, EndpointSlice, Ingress. Run when the kube-apiserver version bumps or when adding a new K8s built-in to the bundle.

```bash
# Uses KUBE_CONTEXT (defaults to kind-haptic-e2e); needs `kubectl` reachable.
./scripts/fetch-k8s-openapi-schemas.sh
```

The script queries each resource's `/openapi/v3/...` endpoint, inlines `$ref`s into the schema, and emits a CRD-wrapped YAML file per resource (`tests/schemas/<group>_<version>_<resourcePlural>.yaml`). The CRD wrapper is what lets `pkg/k8s/schemafetcher/dir_fetcher.go::PluralsFor` resolve `(apiVersion, plural) → GVK` offline — bare OpenAPI v3 files with an `x-kubernetes-group-version-kind` extension work for typed lookup but don't register a plural, so chart configs declaring `watchedResources.<X>: { apiVersion: v1, resources: <X> }` couldn't resolve their GVK without it.

Requirements: a kube context with the resources installed (kind clusters are fine — the OpenAPI surface is identical across vanilla apiserver builds), `kubectl`, `python3`, and `yq`.

## release.sh

Invoked by `make release RELEASE_VERSION=...` (which runs `release.sh <version>`). One invocation prepares the whole release (controller + chart share one version): it promotes the `CHANGELOG.md` `[Unreleased]` section, bumps `VERSION` and `Chart.yaml` (`version`, `appVersion`, image annotations), updates the `helm install --version` examples, and creates the release commit (the docs-site changelog page is generated from `CHANGELOG.md` at mkdocs build time, so it needs no release-time sync). Do **not** call it directly in normal workflows — go through the Makefile target so you get the arg validation for free. See [`docs/site/docs/development/releasing.md`](../docs/site/docs/development/releasing.md) for the end-to-end release process.

## See Also

- `Makefile` — the authoritative list of developer commands; most of these scripts are accessible as `make <target>`
- `docs/site/docs/development/releasing.md` — release process
- `charts/CLAUDE.md` — chart development workflow (`test-templates.sh` is the recommended entry point)
