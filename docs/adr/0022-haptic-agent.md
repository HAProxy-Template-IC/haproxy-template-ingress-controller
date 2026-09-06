# ADR-0022: The HAPTIC agent replaces the Data Plane API; the controller decides, the agent applies

## Status

Accepted 2026-08-18. Obsoletes ADR-0009. Supersedes ADR-0020 §Decision steps 1
and 2 (the syntax parse and the schema check) everywhere, and step 3 (`haproxy
-c` before the deploy) on the reconcile path; the webhook and the config-load
gate keep step 3. ADR-0013's Option 1 reasoning is restated in HAProxy's terms
rather than the API's. ADR-0013's `spec.json` reference is historical: the
generated validators survive only for the WASM playground.

## Context

Route propagation is HAPTIC's remaining measurable gap: create an HTTPRoute →
first 200 takes ≈1 s tuned (seconds by default) against 10–30 ms for an xDS
control plane. The structural cause is that every route adds a `backend`
section, so every route change reloads HAProxy, and the reload path pushes the
whole config through the Data Plane API (DPAPI).

Measured 2026-08-17 (`hack/spikes/`, HAProxy 3.4.3, real chart renders):

| routes | DPAPI raw push, skip reload | DPAPI push + reload | write files + master reload |
|---|---|---|---|
| 300 | 199 ms | 396 ms | 203 ms |
| 1000 | 422 ms | 677 ms | 261 ms |
| 3000 | 1145 ms | 1602 ms | 487 ms |

The DPAPI push is CPU-bound in `dataplaneapi`: client-native parses the whole
config into its model, runs `validate_cmd`, writes, and parses again — it does
not store the bytes it is handed (`handlers/configuration/raw/raw.go`,
`client-native/v6/configuration/raw.go`, `transaction.go`). Its runtime
surface has no `add backend`/`publish backend`/`del backend` and no raw CLI
(v3.4.2, 2026-08-14), so HAProxy 3.4's dynamic backends — the mechanism that
makes a route add reload-free — are unreachable through it. HAPTIC uses a small
subset of the API and carries ~7.7 k hand-written lines plus ~30 k generated
per-version clients for it.

Two constraints shaped the replacement. RULE #2: validation is never traded
away; `haproxy -c` stays where operator input enters. And "no custom config
parsing": the config text is parsed only by HAProxy in production.

## Decision

1. **The HAPTIC agent** (`haptic agent`, same image, the `agent` container of
   every HAProxy pod) replaces the DPAPI. It owns the pod's file tree and the
   HAProxy sockets and exposes two calls: `GET /v1/state` and `POST /v1/apply`
   (`pkg/dataplane/agent/api`).
2. **The controller decides, the agent executes.** The render declares its
   structure as a `renderplan.Plan` (sections by token substitution, backend
   records, map entries, file kinds); `deployplan.Diff(next, Baseline)`
   classifies the change per pod into `runtime | file_only | reload` and
   composes typed ops. The agent writes the files transactionally, runs the ops
   verbatim on the worker `stats socket`, reloads through the master socket when
   told or as fallback, paces reloads, and reports. No HAProxy config parser
   exists in any production binary; client-native's parser survives only in the
   differential CI test and behind the playground build tag (`depguard`
   enforces it).
3. **Validation moves, it does not shrink.** The webhook and the config-load
   gate keep the full `haproxy -c`. The reconcile pipeline is render-only; the
   same `haproxy -c -dr` runs asynchronously in `rendergate` (leader-only, own
   semaphore slot, duty-cycle capped so admission never waits on it). It always
   checks the newest render, plus any superseded plan a pod still reports
   applied, so the fleet's exposure is bounded by what the pods hold, not by
   the render rate. A verdict is about the plan's content, not the render
   that produced it: it names every occurrence of that plan, including the
   re-renders a reconcile loop produces while `haproxy -c` runs, and the gate
   remembers it so later occurrences settle without another check. A refusal
   reverts every pod that carries the failed plan
   without its own HAProxy having loaded it (`mode: revert_lkg`, the agent's
   durable last-known-good set) — a pod whose own binary reloaded it is
   stronger evidence than the controller image's community-edition binary and
   is left alone — and flips the gate to validate-before-dispatch until a
   render passes. HAProxy is the synchronous gate at apply: a rejected command
   or reload is a NACK, the old worker keeps serving, the agent restores the
   LKG files. The DPAPI schema check is dropped; it validated conformance to
   the DPAPI's model, which is no longer a requirement. Enterprise pods lose
   the per-pod DPAPI `validate_cmd`; the pod's own binary rejects a bad config
   at reload time, the journal rolls it back, the NACK carries HAProxy's
   message. One synchronous check survives on the reconcile path: a render
   that accepts HTTP-store content no earlier render used takes the full check
   before that content becomes the store's accepted version, because the gate's
   later verdict reverts the fleet's files, not the store. Every render that
   accepts nothing new skips it, which is all of them in a steady state.

   **The delta, stated.** Every render is still checked by `haproxy -c`, with
   the same flags and the same binary. What changes is when: while the gate is
   open, a render reaches the pods before its verdict, so the exposure window
   is one check plus one apply. It is closed on both ends — the scoped revert
   undoes the applies that HAProxy never loaded, and the latch means a second
   render cannot follow a refused one until a check passes. What that window
   costs is bounded by what an unloadable file set can do: nothing to a running
   worker, which keeps serving what it already loaded, and one failed reload to
   a pod that would otherwise have reloaded into it — which the agent's journal
   rolls back regardless of this gate. Structural changes are reload-proven by
   the pod synchronously, so the window only exists for the runtime-only class.

   **What else the gate holds.** Everything that describes what the fleet runs
   moves with the deployment, not with the render: the `spec.k8sResources` a
   template emits, the `HAProxyCfg` the controller publishes, and the leader
   term's auxiliary baseline (`currentFiles`). While the gate holds renders,
   each of those keeps the last render HAProxy accepted, and the pass that
   releases a held render releases them with it. A Service advertising routing
   the data plane refused, or a `currentFiles` baseline the pods were reverted
   away from, would each be a different form of the same lie.
4. **Hard cutover.** Chart and controller upgrade together; removed values
   `fail` with their replacement; no `dpapi|agent` mode flag. Version skew
   during the roll degrades to full-state + reload, never to a refusal.
5. **Servers are named after pods** and backends carry `guid`s, so endpoint
   churn is `add server`/`set server`/deferred `del server` on every supported
   version and route add/remove is `add backend … from <profile>` /
   `del backend` on 3.4. The SRV_n slot pool goes.

## Alternatives considered

- **Keep the DPAPI, add what is missing upstream.** Rejected: the raw push is
  O(config) by construction and sits on the reload-free path; the missing
  runtime endpoints are not on the upstream roadmap.
- **A smart agent** that parses the config and decides runtime-vs-reload
  itself. Rejected by a judge panel: a classification bug would ship in the
  data plane, the parser would have to exist in production, and the controller
  already has the structure the render produced.
- **Master-socket relay (`@1`, `@@1`) instead of a worker socket.** Rejected
  by measurement: `@1 c1; c2` relays only `c1`, `@@` is absent on 3.0/3.1,
  session state and `wait` do not hold. The chart's `global` gains a worker
  `stats socket`; the master socket serves `reload`/`show proc` only.
- **gRPC / a raw CLI endpoint / TLS in the first version.** Cut for
  simplicity; TLS is additive later.

## Consequences

- Reconcile latency drops from render + validate + DPAPI push + reload to
  render + apply (map/cert/server changes in single-digit ms; a route
  add/remove reload-free on 3.4 once the chart's profiles land).
- ~86 k lines of DPAPI client, generated clients, comparator, orchestrator and
  parser code are deleted; ~3 k lines of agent, `deployplan` and `renderplan`
  replace them.
- The template-author API (`Backend()`, `BackendServers()`, `RegisterMap()`)
  carries the burden of declaring structure; it is strict-mode and documented
  with the chart's macros.
- HAProxy pods pull the HAPTIC image; `haproxy.podSpec.imagePullSecrets`
  defaults to the controller's.
- Metrics migrate (`haptic_dataplane_api_operations_total` →
  `haptic_deploy_apply_total{pod,mode}`, …); the full table is the
  "Where the old metrics went" section of the Monitoring page.
- No `haptic_config_validation_skipped_total` is added. The optimistic render
  gate defers `haproxy -c` off the reconcile wall clock — it runs concurrently
  with the apply — but never skips it, so a "skipped" counter would misreport.
  Refusals are already counted by `haptic_config_rejected_total{validator="haproxy"}`.

### The Enterprise coverage gap

`tests/integration/enterprise_botmgmt_test.go` is dropped with this cutover. It
pushed Enterprise-only sections — bot-management profiles, captchas, WAF
profiles and WAF global — through the DPAPI's model against an HAProxy
Enterprise pod, and it needed a `HAPEE_KEY` to pull that image and its modules.

What it proved has no successor and needs none: the DPAPI accepted or rejected
those sections against its own schema, and that schema is gone. What replaces it
is weaker in one specific way and stronger in another.

- **Weaker**: no CI job deploys an Enterprise binary any more, so no test
  observes an Enterprise-only directive being parsed. HAPTIC has no Enterprise
  image in CI and no licence to obtain one.
- **Stronger**: the pod's own binary is now the judge. An Enterprise directive
  reaches an Enterprise pod verbatim, where it either parses or does not — where
  the DPAPI's model could accept a section HAProxy Enterprise would reject, or
  reject one it would accept. The reload-time rejection path, the journal
  rollback and the NACK carrying HAProxy's own message are covered on Community
  HAProxy by `tests/agent` and `tests/integration`, and they are the same code
  on either edition.

The residual risk is an Enterprise-only *deployment* defect — the agent
mishandling something specific to that binary. Decision 12 of the cutover bounds
the known one: the agent container runs as the Enterprise image's `haproxyUid`
from a Community-based controller image, so the agent must not depend on a
passwd entry or `$HOME`. It does not, and the docker suite runs it with a
read-only root filesystem to keep it that way. Anything past that is untested
until an Enterprise image is available to CI.
