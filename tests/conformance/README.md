# Conformance suites

Gateway API and Ingress conformance run as sibling containers on the test
cluster's Docker network. Use `make test-gateway-conformance` and
`make test-ingress-conformance` after cluster setup.

## Gateway API jobs

| Job | Execution | Purpose |
| --- | --- | --- |
| `test-gateway-conformance-smoke` | One deterministic shard on merge requests | Regression feedback |
| `test-gateway-conformance` | Four shards on main and nightly pipelines | Full regression gate |
| `gateway-conformance-report` | Manual, complete suite against a pipeline snapshot | Candidate report with provenance |
| `release-gateway-conformance` | Complete suite against the published release image | Release report with provenance |

Reports require all three Gateway profiles and reject run, skip, and short
filters. The existing `BackendTLSPolicySANValidation` exception remains visible
as partial extended coverage. Reports from separate shards cannot be combined.

## Generate local candidate evidence

From this checkout, use the project build and cluster setup targets:

```bash
make docker-build-test
HAPTIC_E2E_PROFILE=conformance TEST_RUN_PATTERN='^$' make test-e2e
export CONFORMANCE_CONTROLLER_IMAGE=haptic:test
export CONFORMANCE_IMPL_VERSION="$(docker run --rm --entrypoint /usr/local/bin/haptic haptic:test version | awk '/^[[:space:]]*Version:/ {print $2}')"
export CONFORMANCE_ARTIFACT_DIR="$(mktemp -d /tmp/haptic-conformance-evidence.XXXXXX)"
bash scripts/gateway-conformance-report.sh
```

For an isolated cluster, preserve the same `HAPTIC_E2E_CLUSTER_NAME`,
`HAPTIC_E2E_KUBECONFIG_PATH`, and Docker network across setup and testing. Set
`CONFORMANCE_KIND_CLUSTER` and `CONFORMANCE_KIND_NETWORK` to that cluster and
network. The script refuses to overwrite earlier evidence.

The script retains the exact test exit code, validates the report, and records
the source, image, binary, running controller, cluster, and suite identities.
`report.yaml`, `provenance.json`, and `SHA256SUMS` are the only CI artifacts;
credential-bearing test images and kubeconfigs aren't included.

## Submit a release report upstream

1. Download a passing `release-gateway-conformance` job's artifacts and verify
   them using the [release evidence instructions](../../docs/site/docs/operations/gateway-conformance.md#find-evidence-for-your-release).
2. Copy the unmodified report into the upstream Gateway API repository under
   `conformance/reports/v1.6/haproxy-haptic-haptic/` for the pinned 1.6 suite.
3. Name the file `<channel>-<implementation-version>-<mode>-report.yaml`, using
   the values in the report.
4. Add a `README.md` table linking the report and HAPTIC release. Describe the
   tested chart values and the release job used to reproduce it; link its
   provenance artifact and disclose partial extended results.
5. Open a pull request against `kubernetes-sigs/gateway-api` for upstream review.

Follow the upstream
[report format and submission rules](https://github.com/kubernetes-sigs/gateway-api/tree/main/conformance/reports).
The CI job generates evidence; it doesn't submit reports or claim upstream
acceptance.
