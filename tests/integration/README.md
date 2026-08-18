# tests/integration

Integration tests that run against a real Kind cluster with real HAProxy pods, each with the HAPTIC agent as its second container — the topology the chart deploys. Every test file is tagged `//go:build integration`, so a plain `go test` skips them — use `make test-integration` or pass `-tags=integration` explicitly.

## What's tested

Grouped by the `*_test.go` files in this directory:

| File group | Scope |
|------------|-------|
| `env_test.go` | Fixture-level smoke tests (namespace names stay RFC 1123 compliant) |
| `sync_backends_test.go`, `sync_frontends_test.go`, `sync_servers_test.go`, `sync_global_defaults_test.go`, `sync_sections_test.go`, `sync_observability_test.go`, `sync_auxiliary_test.go` | One transition per case: apply an initial file set, apply a second one, and assert the pod's tree and HAProxy's runtime state converged |
| `sync_idempotency_test.go` | A re-applied render is a noop: no write, no reload |
| `sync_ca_file_test.go` | An mTLS trust bundle rotates on the running worker |
| `auxiliaryfiles_test.go` | The life cycle of every file kind: map, certificate, crt-list, CA file, general file |

Per-section fixtures live under `testdata/` — one subdirectory per HAProxy concept (acls, backends, binds, http-checks, map-files, ssl-certs, …).

## What the suite asserts

The agent exposes no file or configuration endpoint, so every assertion reads the pod itself: `kubectl exec … cat` for the tree, `socat` over HAProxy's worker stats socket for `show map` / `show info`, and `GET /v1/state` for the runtime inventory the controller diffs against.

A case declares two file sets. The suite renders each as a `renderplan.Plan`, asks `deployplan.Diff` what the pod has to do, and sends the resulting apply. Because nothing here parses HAProxy syntax, a plan declares the whole configuration as one section: any configuration change is a reload, while a change confined to auxiliary files runs on the live worker. Cases that pin that distinction set `expectedVerdict`.

## Fixture API

`env.go` exposes `fixenv.CacheResult`-backed constructors:

```go
cluster := SharedCluster(env)   // Kind cluster, shared across the package
image   := AgentImage(env)      // HAProxy image + haptic binary, built and loaded once
ns      := TestNamespace(env)   // per-test namespace (auto-cleaned)
haproxy := TestHAProxy(env)     // HAProxy pod + agent, both ready
client  := TestAgentClient(env) // the controller's end of the wire contract
```

Each fixture resolves its dependencies lazily and caches per scope. A test that calls `TestHAProxy(env)` transparently gets a cluster, an image, a namespace, and a running pod.

`NewSession(t, env)` wraps those into the controller's side of one pod: a desired file set, the plan describing it, and the baseline the pod's last ACK reported.

Version gates use `skipBelowHAProxy(t, "3.1")` or the `minHAProxy` field of a table case. The bound is an HAProxy release, and it comes from the same `HAPROXY_VERSION` that selects the image, so the gate and the pod can never disagree.

## Running

```bash
make test-integration                                       # all
go test -tags=integration ./tests/integration -run TestSyncBackends -v
KEEP_CLUSTER=true go test -tags=integration ./tests/integration -run TestXxx -v
```

`KEEP_CLUSTER=true` (the default) reuses the Kind cluster between runs; set it to `false` to always tear down. The Kind context is `kind-haproxy-test` — switch to it with `kubectl config use-context kind-haproxy-test` when you want to poke at state from a failing run.

The suite builds the `haptic` binary itself and lays it into the pod image, in CI as well as locally. `HAPTIC_BINARY=/path/to/haptic` skips that build and uses the given one instead.

## Adding a case

1. Drop an HAProxy-config fixture under `testdata/<section>/`. Reference auxiliary files by their base-relative path (`maps/x.map`, `general/x.http`) and certificates by their bare filename — the harness adds `default-path origin`, `crt-base` and the worker stats socket to every `global` section it loads.
2. Add a `syncTestCase` to the matching `sync_<section>_test.go` with the two configuration fixtures.
3. Declare any auxiliary file the configuration references, keyed by its manifest path.
4. Set `expectedVerdict` only when the case pins the runtime-versus-reload distinction, and `minHAProxy` when the directive needs a later release.

## Flaky-test policy

Flakes are bugs. `tests/CLAUDE.md` has the investigation checklist — never "retry and merge".

## See also

- [`tests/README.md`](../README.md) — top-level test layout and Makefile targets
- `tests/integration/CLAUDE.md` — fixture-design conventions and parallel-test safety
- [`tests/agent`](../agent/) — the same contract without a cluster: HAProxy and the agent as docker containers
- [`docs/site/docs/development/agent.md`](../../docs/site/docs/development/agent.md) — the wire contract
- [fixenv](https://github.com/rekby/fixenv) — fixture library
- [Kind](https://kind.sigs.k8s.io/) — local Kubernetes
