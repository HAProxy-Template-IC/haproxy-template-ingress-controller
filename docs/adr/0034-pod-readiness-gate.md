# ADR-0034: An opt-in Pod readiness gate orders single-replica rollouts

## Status

Accepted. Replaces ADR-0013's `TestIngressRollingRestartZeroDowntime` contract
for gated pods.

## Context

A Deployment's RollingUpdate deletes the old pod as soon as the new one is Ready.
Ready comes from the pod's own probes; HAProxy learns about the new endpoint only
after HAPTIC renders, validates, and deploys it. Those are independent watchers,
so Kubernetes imposes no order between them. With one replica the old pod can stop
before any HAProxy pod routes to the new one.

GitHub CI run 38483303507 captured it: the old backend served its last request
9 ms after HAPTIC observed the new endpoint, and the deploy completed 482 ms after
it. Two probes failed with `503`. Faster rendering shrinks that window but can't
remove it, and HAProxy retries and queueing don't help when no server is usable
(checked on 3.0.29: every request is an immediate `503` until the server is
enabled).

## Decision

Add the condition `haproxy-haptic.org/load-balancer-ready`, enabled by
`controller.templateLibraries.kubernetesBackends.podReadinessGate.enabled`.
Workloads opt in by listing it under `readinessGates`. It is the same mechanism
as the AWS Load Balancer Controller's target-health gate.

The feature is chart-only:

- `kubernetes-backends` watches pods (`backend_pods`, trimmed with
  `ignoreFields`). A not-ready endpoint whose pod holds the gate and reports
  `ContainersReady=True` renders enabled. Without this, the gate would wait for
  routing and routing would wait for Ready.
- `status-patches-150-pod-readiness-gate` emits the condition only in the
  `deployed` variant, which the status applier writes only when every HAProxy pod
  runs that render (or a skipped deploy confirms the fleet already does).
- SSA under the `haptic-deployed` field manager owns only that entry, because
  Pod `status.conditions` is `listType=map` keyed by `type` (Kubernetes 1.33+).

`TestIngressRollingRestartZeroDowntime` now gates its backend and allows no failed
request. `TestIngressRollingRestartUngatedRecovers` keeps the ungated case: only
fast `503`s, within one window no longer than the scale suite's 3 s single-change
convergence budget.

## Consequences

- ADR-0013's residual shrinks but doesn't vanish: a worker already leaving when
  the gate deploy lands keeps the new server disabled. It now also needs that
  worker's last keep-alive request to coincide with the old pod's exit, which
  follows the deploy by the status write, kubelet, and Deployment latency.
- A gated pod becomes Ready only while a HAPTIC leader deploys. A controller outage
  stalls rollouts of gated workloads, never their existing Ready pods.
- Enabling the gate grants cluster-wide `pods` read and `pods/status` patch.
- The status applier's field manager is fixed, so two installations gating the
  same pod overwrite each other's condition. The docs say to use one.
- The gate doesn't delay the old pod's shutdown. Draining in-progress requests
  on `SIGTERM` stays the application's job.

## Alternatives considered

- **Assert propagation speed in the test.** The old "at most one `503`" contract
  for an ungated pod is a timing bound on the runner, not a guarantee HAPTIC
  can give.
- **`minReadySeconds` or a `preStop` sleep in workloads.** Both guess a duration.
  The gate waits for the actual deploy.
