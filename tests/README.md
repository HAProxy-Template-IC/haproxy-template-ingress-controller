# tests/

This directory holds architecture checks, integration and acceptance suites, and shared test infrastructure. Infrastructure unit tests live beside their helpers; controller unit tests live under `pkg/`.

## Layout

```
tests/
├── architecture_test.go   # arch-go validation (runs on every `go test ./tests`)
├── defaults_consistency_test.go  # cross-package constant-parity checks
├── kindutil/              # Kind cluster helpers shared by integration & acceptance
├── testutil/              # Generic helpers (fixtures, assertions) shared across suites
├── schemas/               # Kubernetes OpenAPI schema files used by e2e tests
├── integration/           # Integration tests (fixenv + Kind, //go:build integration)
│   └── CLAUDE.md / README.md
├── acceptance/            # Controller-only acceptance tests (e2e-framework + Kind)
│   └── CLAUDE.md / README.md
├── e2e/                   # Full-stack e2e tests (helm + HAProxy, //go:build e2e)
│   └── CLAUDE.md
└── conformance/           # Gateway API upstream conformance suite (//go:build gateway_conformance)
```

## Shared infrastructure

Write new orchestration and reusable helpers in Go. Use `tests/process` for
external commands, `tests/kindutil` for owned Kind clusters, `tests/testutil` for
bounded polling, and `tests/tunnel` and `tests/httpclient` for traffic transport.
Pass contexts, cluster identities, namespaces, and artifact paths explicitly.
Keep scenario assertions independent of production functions.

`tests/scenarios` implements chart upgrades, default installs, and real Argo CD
and Flux lifecycle tests. The existing Make targets call `tests/runner` through
thin Bash wrappers. Shared YAML assets and certificate factories live in
`tests/fixtures`; each caller receives fresh data.

Run `make test-process` for subprocess lifecycle tests. These require Unix and
use the `testinfra` build tag; ordinary unit tests inject a runner. `make test`
and the CI unit job also run this target. Cluster scenarios require Docker,
Kind, kubectl, and Helm.

Cluster cleanup checks ownership before deleting nodes or networks. Kept
clusters can be resumed from their ownership record. Existing clusters without
that record may be borrowed by the integration/e2e suites but are never deleted
by them. Scenarios require a fresh cluster name. Private kubeconfigs and command
logs remain in the printed artifact directory after cleanup. Local API and
forwarding listeners use loopback; remote Docker requires reachable API bindings.

See [Test infrastructure contracts](INFRASTRUCTURE.md) for entry points,
regression coverage, diagnostics, and the retained native-language exceptions.

## Running

### Gateway conformance fixture isolation

The four `GatewaySecret*ReferenceGrant*` tests and `ListenerSetReferenceGrant`
run exclusively because they share certificate Secrets and grant namespaces.
The ListenerSet fixture grants every Gateway in `gateway-conformance-infra`
access to every Secret in `gateway-conformance-web-backend`. Running it alongside
the missing-grant tests makes their expected denial impossible. Its namespace
readiness wait can then keep that grant alive until those tests time out.

The runner changes only these tests' scheduling. Their manifests, assertions,
timeouts, feature requirements, and inclusion in conformance reports stay intact.
All other upstream parallel tests retain their parallel execution.

### Commands

From the root of the repo:

| Command | What it runs |
| --- | --- |
| `make test` | Unit tests, shared helpers, process lifecycle tests, and tooling regressions |
| `make test-process` | Real subprocess cleanup and cancellation checks, without a cluster |
| `make test-integration` | Integration suite against Kind and HAProxy |
| `make test-acceptance` | Controller acceptance tests on Kind |
| `make test-acceptance-parallel` | Acceptance tests sharing one cluster with isolated namespaces |
| `make test-e2e` | Chart installation and full-stack routing tests |
| `make test-chart-upgrade` | Released-chart upgrades, rejection, and repair |
| `make test-helm-defaults` | Default chart admission, certificates, HAProxy syntax, and traffic |
| `make test-gitops-lifecycle` | Real Argo CD or Flux installation, sync, upgrade, rejection, and recovery |
| `make test-coverage` / `test-integration-coverage` / `test-coverage-combined` | Unit/integration coverage |
| `make check-all` | Lint, security checks, and `make test` |

Run the cluster suites separately after `make check-all`; it does not create clusters.

Environment knobs used by the integration suite:

- `KEEP_CLUSTER=true` (default) — reuse the Kind cluster across runs; set to `false` to always clean up.
- `KIND_NODE_IMAGE=kindest/node:v1.32.0` (default) — override the Kind node image.

Integration tests additionally require the `integration` build tag; `make test-integration` adds it automatically. Running `go test ./tests/integration/...` with no tag silently finds no tests.

## Architecture Test

`architecture_test.go` drives [`arch-go`](https://github.com/arch-go/arch-go) against `arch-go.yml` in the repo root. The rules enforce the "controller is the only coordination layer, libraries are independent" shape of the tree. A failure looks like:

```text
Architecture validation failed!
  Rule: pkg/core should not depend on pkg/controller
    Package: pkg/core/config
      - imports pkg/controller/events (forbidden)
```

The fix is almost always moving the offending import, not changing the rule. Update `arch-go.yml` only when the boundary itself has legitimately moved.

## Integration Tests

Live under `tests/integration/`. Use [`fixenv`](https://github.com/rekby/fixenv) for fixture composition and a shared Kind cluster (via `tests/kindutil`) so each test runs in its own namespace without paying the cluster-creation cost every time. All test files are tagged `//go:build integration`.

See `tests/integration/README.md` for per-test organisation and `CLAUDE.md` for fixture design.

## Acceptance Tests

Live under `tests/acceptance/`. Use [`kubernetes-sigs/e2e-framework`](https://github.com/kubernetes-sigs/e2e-framework) and a locally built controller image. Each test exercises user-facing behaviour end-to-end (config reloads, metrics endpoint shape, debug endpoint content, etc.) and reaches controller debug endpoints through the shared `pkg/k8s/podclient` port-forward client.

See `tests/acceptance/README.md` for the test inventory and `CLAUDE.md` for the env helpers.

## Adding Tests

- **Unit tests** — beside the code they cover (`pkg/foo/foo_test.go`). No build tag.
- **Integration tests** — under `tests/integration/`, tagged `//go:build integration`, reuse the fixtures in `env.go`.
- **Acceptance tests** — under `tests/acceptance/`, follow the feature-based style in `leader_election_test.go` or `metrics_test.go`.
- **New test type** — put the directory under `tests/`, add a Makefile target, and document it both here and in its own `CLAUDE.md`.

Flaky-test policy: flakes are bugs, not noise. `tests/CLAUDE.md` has the investigation checklist — retrying a CI job without finding root cause is not an option.

## Prerequisites

- Go `1.27.x` (pinned via `.tool-versions` / `go.mod`; use `env -u GOROOT go ...` if your shell points at an older toolchain).
- Docker (for Kind, and for the `test-acceptance` image build).
- Kind is installed automatically by the Makefile targets the first time you run them.

## See Also

- `tests/CLAUDE.md` — developer notes, flaky-test policy, fixture conventions
- `tests/integration/` and `tests/acceptance/` sub-READMEs and CLAUDE.md files
- `arch-go.yml` — the architecture rules this directory enforces
- `Makefile` — authoritative list of `test-*` targets
