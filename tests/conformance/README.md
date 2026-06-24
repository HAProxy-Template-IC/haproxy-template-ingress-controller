# Conformance suites

Two upstream conformance suites run here against the chart-deployed cluster:

- **Gateway API** — `gateway_conformance_test.go` (build tag `gateway_conformance`).
- **Ingress** — `ingress_conformance_test.go` (kubernetes-sigs/ingress-controller-conformance wrapper).

Both run as a sibling container on the kind docker network; see
`make test-gateway-conformance` / `make test-ingress-conformance` and the
file-level doc comments.

## Gateway API: regression gate vs. submittable report

The Gateway API suite serves two purposes, with two CI jobs:

| Job | Shape | Purpose |
|-----|-------|---------|
| `test-gateway-conformance` | sharded 4× (`parallel: 4`), runs per MR | fast regression gate — fails on any conformance regression |
| `gateway-conformance-report` | **unsharded**, single full run, **manual** | produces a submittable `ConformanceReport` artifact |

The report can't be stitched from the shards: `cSuite.Report()` only knows about
the tests the *running instance* executed, so a coherent report needs one process
that runs the whole suite. That's why the report job is a separate, unsharded run.

Test selection is driven by `SupportedFeatures` in both jobs; `ConformanceProfiles`
and `ReportOutputPath` are **report-only** and are set only when
`CONFORMANCE_REPORT_OUTPUT` is present, so the gate's behaviour is unchanged.

## Generating the report

In CI: trigger the **manual** `gateway-conformance-report` job. It runs the full
suite with `CONFORMANCE_IMPL_VERSION` set from `VERSION`, writes the report inside
the test container, `docker cp`s it out, and uploads `conformance-report.yaml` as
a job artifact (`when: always`, so a partial report is captured even if some tests
fail).

Locally (after `make test-e2e` has left the `haptic-e2e` cluster up):

```bash
CONFORMANCE_KEEP_CONTAINER=1 \
  CONFORMANCE_REPORT_OUTPUT=/conformance-report.yaml \
  CONFORMANCE_IMPL_VERSION="$(cat VERSION)" \
  TEST_RUN_PATTERN="" \
  make test-gateway-conformance
docker cp haptic-conformance-run:/conformance-report.yaml ./conformance-report.yaml
docker rm -f haptic-conformance-run
```

## Submitting upstream (separate, deliberate step)

To list HAPTIC on the [Gateway API implementations page](https://gateway-api.sigs.k8s.io/implementations/):

1. Download `conformance-report.yaml` from the manual job.
2. Commit it into a PR against [kubernetes-sigs/gateway-api](https://github.com/kubernetes-sigs/gateway-api)
   under `conformance/reports/v1.5.x/haproxy-haptic-haptic/` (the org-project
   convention), alongside a short `README.md`.

That upstream PR is **not** created automatically — open it intentionally once the
report is reviewed.
