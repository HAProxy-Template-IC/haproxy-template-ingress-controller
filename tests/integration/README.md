# tests/integration

Integration tests that run against a real Kind cluster with real HAProxy + Dataplane API pods. Every test file is tagged `//go:build integration`, so a plain `go test` skips them — use `make test-integration` or pass `-tags=integration` explicitly.

## What's Tested

Grouped by the `*_test.go` files in this directory:

| File group | Scope |
|------------|-------|
| `env_test.go` | Fixture-level smoke tests (cluster comes up, HAProxy responds) |
| `sync_backends_test.go`, `sync_frontends_test.go`, `sync_global_defaults_test.go`, `sync_sections_test.go`, `sync_observability_test.go`, `sync_auxiliary_test.go`, `sync_idempotency_test.go`, `sync_common_test.go` | Dataplane-sync coverage of every HAProxy section the comparator knows about, plus idempotency (re-sync is a no-op) |
| `auxiliaryfiles_test.go` | Map/SSL/general/crt-list file handling through the three-phase sync |
| `enterprise_botmgmt_test.go`, `enterprise_keepalived_test.go`, `enterprise_misc_test.go`, `enterprise_udplb_test.go`, `enterprise_waf_test.go` | Enterprise-only sections; skipped via `skipIfNotEnterprise` + capability-specific skips when the test cluster runs community HAProxy |

Per-section YAML fixtures live under `testdata/` — one subdirectory per HAProxy concept (acls, backends, binds, http-checks, waf, etc.).

## Fixture API

`env.go` exposes a handful of `fixenv.CacheResult`-backed constructors:

```go
cluster  := SharedCluster(env)                 // Kind cluster, shared across tests
ns       := TestNamespace(env)                 // per-test namespace (auto-cleaned)
haproxy  := TestHAProxy(env)                   // HAProxy pod + Dataplane API ready
raw      := TestDataplaneClient(env)           // low-level *client.DataplaneClient
hi       := TestDataplaneHighLevelClient(env)  // public *dataplane.Client
parser   := TestParser(env)
cmp      := TestComparator(env)
```

Each fixture resolves its dependencies lazily and caches per-scope. A test that calls `TestHAProxy(env)` transparently gets a cluster, a namespace, and a running HAProxy — without paying for them in tests that don't need them.

Capability gates (Enterprise-only features, version-specific API surface) are handled by the `skipIf*` helpers in the same file. The common ones:

| Helper | Skip condition |
|--------|----------------|
| `skipIfNotEnterprise` | Cluster runs community HAProxy |
| `skipIfWAFNotSupported` / `skipIfWAFGlobalNotSupported` / `skipIfWAFProfilesNotSupported` | WAF feature not on this Enterprise version |
| `skipIfUDPLBNotSupported` / `skipIfUDPLBACLsNotSupported` | UDP load-balancer sections not supported |
| `skipIfKeepalivedNotSupported` / `skipIfBotManagementNotSupported` / `skipIfALOHANotSupported` | Other Enterprise feature gates |
| `skipIfPingNotSupported` / `skipIfGitIntegrationNotSupported` / `skipIfDynamicUpdateNotSupported` / `skipIfAdvancedLoggingNotSupported` | v3.2+ or misc Enterprise features |

Exhaustive list: grep `func skipIf` in `env.go`.

## Running

```bash
make test-integration                                       # all
go test -tags=integration ./tests/integration -run TestSyncBackendAdd -v
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestXxx -v
```

`KEEP_CLUSTER=true` (the default) reuses the Kind cluster between runs; set it to `false` to always tear down. The Kind context is `kind-haproxy-test` — switch to it with `kubectl config use-context kind-haproxy-test` when you want to poke at state from a failing run.

## Adding a Section

Most integration tests follow the same shape — render a minimal HAProxy config for the feature, sync it via the high-level client, then assert the Dataplane API reports the expected shape:

1. Drop a YAML or HAProxy-config fixture under `testdata/<section>/`.
2. Add a `sync_<section>_test.go` (or extend an existing grouping) that pulls the fixture, calls `hi.Sync(...)`, and verifies the resulting state.
3. If the section is Enterprise-only, guard the test with the appropriate `skipIf*` helper — or add a new one to `env.go` if the capability isn't already represented.
4. If the section is new to the comparator, `pkg/dataplane/comparator/CLAUDE.md` has the execute-factory walkthrough.

See `tests/integration/CLAUDE.md` for fixture-design conventions (when to hit `SharedCluster` vs. when to isolate, parallel-test safety, how the HAProxy-version matrix in CI selects fixtures).

## Flaky-Test Policy

Flakes are bugs. `tests/CLAUDE.md` has the investigation checklist — never "retry and merge".

## See Also

- [`tests/README.md`](../README.md) — top-level test layout and Makefile targets
- `tests/integration/CLAUDE.md` — fixture-design conventions, enterprise-test patterns
- [`pkg/dataplane/comparator`](../../pkg/dataplane/comparator/) — what these tests exercise
- [fixenv](https://github.com/rekby/fixenv) — fixture library
- [Kind](https://kind.sigs.k8s.io/) — local Kubernetes
