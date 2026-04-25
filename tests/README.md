# tests/

Everything that isn't a unit test. Unit tests live beside their code under `pkg/**`; this directory holds architecture validation, integration tests against a real Kubernetes + HAProxy, and end-to-end acceptance tests.

## Layout

```
tests/
├── architecture_test.go   # arch-go validation (runs on every `go test ./tests`)
├── kindutil/              # Kind cluster helpers shared by integration & acceptance
├── testutil/              # Generic helpers (fixtures, assertions) shared across suites
├── integration/           # Integration tests (fixenv + Kind, //go:build integration)
│   └── CLAUDE.md / README.md
└── acceptance/            # End-to-end tests (e2e-framework + Kind)
    └── CLAUDE.md / README.md
```

## Running

From the root of the repo:

| Command | What it runs | Typical duration |
|---------|--------------|------------------|
| `make test` | Unit tests + `TestArchitecture` | seconds |
| `make test-integration` | Integration suite (creates/uses a persistent Kind cluster) | ~2min cold, ~30s warm |
| `make test-acceptance` | Acceptance suite (builds controller image, creates Kind cluster, runs tests sequentially) | 3–5min |
| `make test-acceptance-parallel` | Same as above but shares one cluster across test cases | ~half the above |
| `make test-coverage` / `test-integration-coverage` / `test-coverage-combined` | Same suites with coverage output | as above + small overhead |
| `make check-all` | `make lint` + `make audit` + `make test` (the CI baseline) | under a minute |

There is no `test-all` target — run the three top-level ones in sequence if you need them all.

Environment knobs used by the integration suite:

- `KEEP_CLUSTER=true` (default) — reuse the Kind cluster across runs; set to `false` to always clean up.
- `KIND_NODE_VERSION=v1.29.0` — override the node image.

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

Live under `tests/acceptance/`. Use [`kubernetes-sigs/e2e-framework`](https://github.com/kubernetes-sigs/e2e-framework) and a locally built controller image. Each test exercises user-facing behaviour end-to-end (config reloads, metrics endpoint shape, debug endpoint content, etc.) and talks to the controller via the Kubernetes API-server proxy (see `tests/acceptance/README.md` for rationale).

See `tests/acceptance/README.md` for the test inventory and `CLAUDE.md` for the env helpers.

## Adding Tests

- **Unit tests** — beside the code they cover (`pkg/foo/foo_test.go`). No build tag.
- **Integration tests** — under `tests/integration/`, tagged `//go:build integration`, reuse the fixtures in `env.go`.
- **Acceptance tests** — under `tests/acceptance/`, follow the feature-based style in `leader_election_test.go` or `metrics_test.go`.
- **New test type** — put the directory under `tests/`, add a Makefile target, and document it both here and in its own `CLAUDE.md`.

Flaky-test policy: flakes are bugs, not noise. `tests/CLAUDE.md` has the investigation checklist — retrying a CI job without finding root cause is not an option.

## Prerequisites

- Go `1.26.x` (pinned via `.tool-versions` / `go.mod`; use `env -u GOROOT go ...` if your shell points at an older toolchain).
- Docker (for Kind, and for the `test-acceptance` image build).
- Kind is installed automatically by the Makefile targets the first time you run them.

## See Also

- `tests/CLAUDE.md` — developer notes, flaky-test policy, fixture conventions
- `tests/integration/` and `tests/acceptance/` sub-READMEs and CLAUDE.md files
- `arch-go.yml` — the architecture rules this directory enforces
- `Makefile` — authoritative list of `test-*` targets
