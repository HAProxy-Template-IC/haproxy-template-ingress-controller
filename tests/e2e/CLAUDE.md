# tests/e2e - Full-Stack End-to-End Tests

Development context for full-stack end-to-end tests.

## Scope

This suite tests the **deployed system as a whole**: the controller, the helm
chart, the template libraries, the SPOA hub and modules, external services
(auth-server, blocklist-server), and actual HTTP/TLS routing through HAProxy.

## Self-contained

The suite owns its dependencies. `make test-e2e` will:

1. Build the controller image as `haptic:test` via `docker-build-test`.
2. Tag it as `haptic:test-haproxyX.Y` and verify its source hash and binary digest.
3. Create kind cluster `haptic-e2e` if it doesn't exist (or reuse it).
4. Load `haptic:test-haproxyX.Y` into kind.
5. Apply CRDs.
6. Helm-install the chart from `charts/haptic` via the Helm CLI with
   values pointing at the local image.
7. Apply embedded backend fixtures (auth-server, blocklist-server,
   echo-server, haproxy-demo-backend, haproxy-test-backend).
8. Verify that every controller pod belongs to the expected rollout.
9. Wait for the controller pipeline to reach `deployment.status=succeeded`.
10. Verify every controller pod's binary checksum, then run the tests. The scale
    test defers this checksum until after its memory and CPU samples.

Nothing outside the suite is required. `scripts/start-dev-env.sh` is the
**developer's interactive dev loop** and is not invoked by the test suite.

## Distinction from sibling suites

| Suite | Scope | Cluster | Talks to controller via |
| --- | --- | --- | --- |
| `tests/integration/` | Component config-generation logic | Per-test kind | Direct fixtures |
| `tests/acceptance/` | Controller-only behavior (debug endpoints, leader election, metrics) | `kind-haproxy-test` | API server proxy |
| **`tests/e2e/`** | **Full deployed stack — real HTTP through HAProxy** | **`kind-haptic-e2e`** | **API server proxy + NodePort** |

## Conventions

- Build tag: `e2e`. Run with `make test-e2e`; the target stamps and verifies the exact controller image and binary.
- **Per-test namespace.** Each test creates its own namespace and applies its own
  routing fixtures (Ingress, HTTPRoute, Secret, etc.); cleaned up via `t.Cleanup`.
  Stateless backends (echo-server, auth-server, blocklist-server) stay shared
  and are deployed once per cluster.
- **Condition-based waits, never sleeps.** All waits poll a real condition
  (HTTP probe, debug-endpoint state, endpoint count) under
  `testutil.WaitConfig` exponential backoff. No `time.Sleep(N)` in test code.
- **Per-test logs on failure.** A standard `t.Cleanup` gated on `t.Failed()`
  dumps controller / HAProxy / SPOA / namespace-event logs to
  `debug-logs/<test-name>/` for CI artifact upload.
- **All full-stack tests live here as Go tests** under the `e2e` build tag.

## Reading the code

- `main_test.go` — `TestMain`, fully self-contained: kind cluster, helm install,
  fixture apply, controller-ready wait.
- `constants.go` — cluster, namespace, port constants.
- `env.go` — `WaitForE2EEnvironmentReady`, debug-client construction,
  pod/log helpers.
- `fixtures.go` — namespace and routing-resource (Ingress / HTTPRoute / Secret)
  builders for per-test manifests.
- `cleanup.go` — `DumpLogsOnFailure`.
- `httpclient/` — fluent HTTP/HTTPS/mTLS client with retry/backoff and
  DinD-aware host resolution.

The embedded backend fixtures are sourced from `scripts/dev-env-assets/`
via the `devassets` Go package added there. This keeps a single source of
truth for both the test suite and `start-dev-env.sh`.

## Running

```bash
# All e2e tests
make test-e2e

# A specific test
TEST_RUN_PATTERN=TestIngressBasic make test-e2e

# Keep cluster after run for debugging (default: keep)
KEEP_CLUSTER=true make test-e2e

# Keep namespaces after failure for debugging
KEEP_NAMESPACE=true make test-e2e

```

The defaults remain `haptic-e2e`, `/tmp/haproxy-e2e-kubeconfig`, the `kind`
Docker network, and host ports `31080`, `31443`, and `31404`. The custom-cluster
variables are an internal seam for a caller that owns and verifies the Docker
network. Use `make bench-gateway-api` for an isolated benchmark environment.
`HAPTIC_E2E_GWAPI_CHANNEL=experimental` installs the experimental CRDs and
enables the chart's matching experimental-field validation tests.

## CI

The CI job runs `make test-e2e` directly. No `start-dev-env.sh` invocation.
On failure, the `debug-logs/` directory is uploaded as a CI artifact.
