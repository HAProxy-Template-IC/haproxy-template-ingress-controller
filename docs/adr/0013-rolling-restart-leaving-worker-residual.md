# ADR-0013: Accept the bounded rolling-restart residual — a leaving worker's stranded keep-alive connection

## Status

Accepted. No code behaviour change. Documents why the two candidate fixes for
issue #70 are **not** pursued and records `TestIngressRollingRestartZeroDowntime`
as the standing sentinel. Builds on ADR-0011 (config-driven server addresses, no
server-state-file).

Option 1's reasoning was rewritten by [ADR-0022](0022-haptic-agent.md): the
controller's reach into a pod is no longer the Data Plane API's endpoint set but
an agent that holds both HAProxy sockets, so worker routing is a question about
HAProxy's master CLI alone. The measurement that decided it is in ADR-0022's
alternatives — `@1 c1; c2` relays only the first command, and `@@` does not
exist below 3.2. The conclusion is unchanged; the reason is now HAProxy's, not
an API's.

## Context

Issue #67 closed the bulk of the single-replica rolling-restart 503 by keeping the
latest render's runtime-eligible server subset flowing to the live workers
throughout the deploy interval (`applyRuntimeSubset` at both deploy-loop wait
points). Issue #70 is the first artifact-backed capture of the **residual** that
survives that fix (CI job 15186800866, `e2e[3.4]`: one 503 / 403 ms). It is not a
regression of #67 — that fix is verified intact in the same capture.

**Mechanism.** Concurrent tenants' TLS-bearing structural changes coalesce behind
the 5 s min-deploy interval. The structural deploy renders **pre-flip** (before the
rollout's new pod is Ready) and issues a `force_reload` push of that body to both
pods. While that POST is in flight (~139 ms), the rollout's EndpointSlice flip
lands (new pod Ready, old pod terminating). The flip drives a fresh render whose
runtime fast-path (`set server …`) applies in ~50 ms — but the Dataplane API routes
runtime commands to the **current** worker only, so the correction lands in the
**new** worker the reload just spawned. The **leaving** worker keeps its pre-flip
state (`SRV_1` → the now-dead pod, `SRV_2` → maint) and still owns an established
keep-alive connection. A request pinned to that connection has no redispatch
target (3 retries, `SC--`) and dies at ~403 ms. The new worker serves 200s
immediately.

The victim is therefore a connection **on the leaving worker**, which is
unreachable through the Dataplane API's runtime lane.

## Decision

Accept this as a bounded residual. Do not pursue either candidate fix. Keep
`TestIngressRollingRestartZeroDowntime` (`tests/e2e/ingress_rolling_restart_test.go`,
run across the full HAProxy 3.0–3.4 e2e matrix on every MR and post-merge, with no
skip gate) as the standing sentinel.

The residual requires **all** of: a structural reload in flight ∧ an EndpointSlice
flip landing inside the ~150 ms render→swap window (largely inside the ~139 ms
`force_reload` POST itself) ∧ an in-flight keep-alive request pinned to the flipped
backend on the old worker. `option redispatch` + the RFC 5737 `192.0.2.1`
placeholder sentinel (ADR-0011) keep even this window non-catastrophic — a fast
`timeout connect` failover, not an unbounded self-loop.

## Options considered and rejected

### Option 1 — Seed the leaving worker via HAProxy master-CLI worker routing (`@!<pid>` / `@<relative>` on `haproxy-master.sock`)

This is the only mechanism that could reach a stranded connection on the leaving
worker. **Out of scope for a controller-only fix.**

- The controller reaches HAProxy **only** through the Dataplane API over HTTP
  (`pkg/dataplane/client/client.go`). `haproxy-master.sock` is a Unix domain socket
  **inside the HAProxy pod**; the controller has no network path to it.
- The deployed Dataplane API exposes **no** worker/process routing and **no** raw
  runtime-command passthrough. Verified against the newest bundled spec (v3.3,
  `pkg/generated/dataplaneapi/v33/spec.json`): the runtime endpoints are ACLs,
  maps, `ssl_*`, `stick_tables`, `servers`, and `info` only. The runtime server
  mutation `PUT /services/haproxy/runtime/backends/{parent_name}/servers/{name}`
  takes only the `name` + `parent_name` **path** params — no `process`/`pid`
  selector (the `runtime_server` schema is `address, admin_state, id, name,
  operational_state, port`). The only `raw` endpoint is
  `/services/haproxy/configuration/raw` (a full config-body push), not a CLI
  passthrough. This holds for every version HAPTIC deploys (3.0–3.4).
- HAProxy's master CLI does support `@!<pid>`/`@<relative>` worker routing, but the
  Dataplane API neither surfaces it nor is the master socket reachable by the
  controller. Option 1 needs a genuine **upstream Dataplane API feature that does
  not exist** plus a socket path the controller does not have. Not a controller-only
  change.

### Option 2 — Re-collect runtime actions immediately before the `force_reload` POST

Re-diffing the desired config against the pod's live state right before the seed
push, to widen the set of `set server` actions the seed carries. **Does not close
the capture and can regress correctness — rejected.**

- Inside `applyWithReload` (`pkg/dataplane/orchestrator.go`) the only "desired"
  reference is the fixed, pre-flip `desiredConfig` (the scheduled render captured at
  dispatch). The capture's flip is **not** in that body (it landed after dispatch),
  so no re-diff against it can produce the flip's actions.
- The seed push (skip_reload + `set server`) reaches only the **current** worker —
  the same limitation as Option 1 — so even a re-collected seed cannot reach the
  stranded connection on the leaving worker.
- **Correctness-regression risk.** The diff is currently computed at sync start,
  when the capture shows `runtime_ops=0` (on-disk still matched the pre-flip
  render). Moving the re-diff to *right before the POST* widens the window in which
  a concurrent partial runtime bypass (`runtime_bypass.go`) has already written a
  **post-flip** body to disk. `desiredConfig` is pre-flip, so the re-collected delta
  would then emit **revert** actions (put the new pod's slot back to maint,
  `SRV_1` back to the dead pod) and seed them onto the draining old worker —
  actively undoing the fresher bypass state. That is the exact "re-disabled the new
  pod's slot" harm the issue names, transposed onto the leaving worker; the current
  sync-start timing avoids it.
- The freshest render lives in the scheduler (`pending` / `lastDispatched*`), not in
  `pkg/dataplane`; threading it into the orchestrator to do "toward-fresher" seeding
  would break the "`force_reload` pushes the scheduled+validated render" invariant
  and the per-pod checksum/status accounting — and still would not reach the leaving
  worker.

## Consequences

- The residual stays an accepted, artifact-characterised known limitation, guarded
  by `TestIngressRollingRestartZeroDowntime` on every e2e run (3.0–3.4). A
  regression that *widens* it fails that test.
- A code comment at the seam (`applyWithReload`'s seed-push block in
  `pkg/dataplane/orchestrator.go`) points here so a future "re-collect before the
  POST" or "post-reload resync" attempt is caught before it reintroduces the
  revert-stomp.
- If a hard zero-window guarantee across a co-batched structural reload ever becomes
  a requirement, the only real closers are an **upstream Dataplane API worker-routing
  feature** (Option 1) or the DNS/`server-template` model in ADR-0011 alternative 2 —
  both adopted deliberately, with their trade-offs understood.
