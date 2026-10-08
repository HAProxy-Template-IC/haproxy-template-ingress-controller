# ADR-0033: Retain acknowledged fleet configuration across leader lifetimes

## Status

Proposed.

## Context

The deployer keeps its last validated render occurrence in memory. Followers warm
the render pipeline but do not run the leader's deployment scheduler. Neither a
fresh process nor a warm follower necessarily has that occurrence when elected.
If rendering then fails, existing HAProxy workers keep serving, but replacement
pods keep the bootstrap configuration whose readiness endpoint returns 503.

ADR-0032 can isolate invalid watched objects. It cannot manufacture a valid global
template or accepted content for an unreachable critical HTTP source. ADR-0028
preserves a serving iteration within a process; it cannot supply deployment bytes
after that process exits.

The ordinary published `HAProxyCfg` is not a durable deployment receipt. Publication
can precede the first worker acknowledgement, and publishing the next output can
prune the previous output's auxiliary files. Agent state can prove what is applied,
but its plan blob intentionally omits exact configuration and auxiliary bytes.

## Decision

Use HAPTIC's Kubernetes output resources to store complete checkpoints, with
separate ownership from ordinary render publication. Agents retain their existing
API and permissions. All recovery reads and integrity checks run in the controller.

### Checkpoint publication

A successful deployment carries a pod receipt identifying the pod UID, runtime,
content checksum, applied plan, running worker plan, worker-operation plan, and
apply mode. A scheduled reload is insufficient. For runtime updates, the worker's
operation plan can confirm the new output while its last reload plan remains old.

After acknowledgement, the publisher writes a content-addressed `HAProxyCfg`
owned by the same `HAProxyTemplateConfig` UID. Its auxiliary files are separate
content-addressed objects owned by that checkpoint. Certificates and CA bundles
remain Secrets. `spec.retainedPlan` preserves compressed deployment declarations
and artifact metadata that cannot safely be recovered by parsing HAProxy text.
Its worker receipt is historical evidence, not current fleet membership. Pod
termination and discovery cleanup leave checkpoint statuses and their auxiliary
children intact, including after the acknowledging pod disappears.

The publisher writes the complete set, verifies it by reading it back, and only
then atomically promotes its name, UID, and checksum in the ordinary `HAProxyCfg`'s
`status.retainedConfigs`. Interrupted publication leaves the previous pointer
usable. Repeated acknowledgements of identical output reuse its content address.
Keep the newest checkpoint, its predecessor, and older checkpoints whose checksums
still appear in pod deployment status. Garbage collection follows pointer promotion
and removes only unreferenced checkpoints belonging to this template UID; owner
references clean up their children. Stale pod status can retain extra storage.

### Deployment freshness across leadership terms

Every term clears previous in-memory render and deployment candidates, even when
context cancellation prevented processing the leadership-loss event. A returning
leader must re-establish freshness after another leader has managed the fleet.

The first render in a leadership term waits for the normal HAProxy render gate
before dispatch. This keeps an invalid cold render from superseding the durable
intent before any worker can load it. After the first accepted render, the existing
asynchronous render gate behavior remains unchanged (ADR-0022).

Before the first agent apply, atomically record the output plan ID and content
checksum on the leader Lease. The update checks the Lease holder, fencing epoch,
and resource version together. A new leader claims its writer identity while
preserving the last deployment intent. A delayed previous leader cannot overwrite
that intent after takeover. Lease renewals preserve the annotation. Without leader
election, an owned retained-state Lease preserves the same record across restarts.

Recording intent before apply closes the interval between a pod loading new bytes
and the publisher saving its acknowledgement. If intent names a newer output but
its complete acknowledged checkpoint is missing, recovery refuses the older
checkpoint, even if every old pod has disappeared. A failed or interrupted newer
apply therefore sacrifices replacement capacity until rendering recovers; it never
makes an older checkpoint eligible again. A normal successful render can establish
a fresh deployment intent through the usual validation and deployment path.

### Recovery and agreement

After a render failure, a leader without an in-memory validated occurrence reads
the fleet through authenticated agent `GET /v1/state` with file verification. A
bootstrap pod has no applied, running, worker-operation, or last-known-good plan,
no stored applied plan, no applied token, and no invariant violation.
Worker verification and reload adoption are serialized: a verified read must not
mistake an owned reload's new worker for an unexpected replacement. Detection of
foreign workers outside reload adoption and invalidation of an in-flight apply's
baseline remain enforced.

Read checkpoint pointers and the exact auxiliary references from Kubernetes.
Verify template and parent owner references, object UIDs, namespaces, kinds,
deletion state, content digests, content-addressed names, the complete auxiliary
set identity from ADR-0031, artifact paths, plan identity, and its worker receipt.
Read certificate bytes rather than trusting their checksum annotations. Re-read
the pointer to reject a snapshot that changed during collection. A modified,
foreign, missing, or incomplete object fails recovery; it is never repaired on
the recovery path.

Select only the checkpoint matching the durable deployment intent. Publication
order alone is insufficient: an older acknowledgement can arrive after a newer
one. Every configured pod must report that selected plan, current worker evidence,
and its complete file digest/size set. Mixed plans, a pending reload, unverifiable
worker state, or any unreachable pod reject recovery. If a newer checkpoint is
complete and agrees with the entire fleet, use it. Otherwise discard the retained
candidate and leave replacement pods NotReady. Existing pods stay untouched.

If every discovered pod is a bootstrap pod and none is unreachable, the durable
worker receipt proves that a pod previously loaded the selected checkpoint. The
latest deployment intent must still match. Recheck intent and live fleet agreement
immediately before each bootstrap apply and after state conflicts. These checks
apply equally to graceful hand-over and sudden leader loss.

Before dispatch, run the complete recovered output through the same validation
service and current HAProxy binary as the render gate, including `-c` and `-dr`.
A failed check leaves bootstrap pods NotReady. Recovery uses the normal deployment
scheduler and reload-proven agent apply. It verifies each target's bootstrap state
again immediately before applying and after a state conflict. Configured pods
receive no apply, reload, revert, or plan update.

Recovery work is fenced to the leadership term and discovered pod identities.
New pod discovery triggers another attempt; bounded background retries handle
transient API or agent failures. A new authenticated render cancels pending
recovery. The first successful render uses the normal per-pod diff and runtime
operations where supported, including for pods started from the checkpoint.

### Publication while blocked

A failed reconciliation cancels active ordinary publication and prevents queued
publication from starting. Keep unsaved worker receipts in the bounded deployed
queue and resume them when rendering recovers, even if the output is unchanged.
If the process exits before the acknowledgement is saved, the latest deployment
intent still rejects an older checkpoint; replacements remain NotReady until a
usable checkpoint exists. Saving receipts does not bypass the publication latch.
Recovery does not publish a synthetic successful render
or configuration receipt, update the canonical configuration, or run auxiliary
cleanup. Requests already accepted by the API server before cancellation cannot
be undone; the separately owned acknowledged checkpoint remains intact. A new
authenticated render releases the publication latch through the normal path.

### Validation delta: RULE #2

No admission rule, startup load gate, validation test, or schema requirement is
removed. Normal rendering retains its existing gates; the first render of each
leadership term now waits for its HAProxy verdict before dispatch. Recovery adds
checks over a complete previously acknowledged output: ownership and content
identity, actual worker agreement, a durable pre-apply freshness barrier, and
synchronous validation by the **current** HAProxy binary
before the first dispatch. An earlier binary's acceptance never substitutes for
this check. The target agent still validates, reloads, and proves worker uptake;
a NACK never turns bootstrap readiness into success.

Recovered output is not a successful render of current inputs. It has no synthetic
template status patches or events. `/healthz` includes the last render error in
`components.reconciliation-coordinator.error` until rendering succeeds. Its HTTP
status still reports component availability: an idle controller can return 200
while rendering is blocked. Recovery never clears that error. The startup,
readiness, liveness, and serving-iteration decisions from ADR-0028 stay unchanged.

### Observability

Emit `RetainedConfigActive` Warning events on the `HAProxyTemplateConfig` UID,
naming the checksum and render error. Emit `RetainedConfigUnavailable` with the
reason when no checkpoint qualifies. Deduplicate unchanged notices within a term.

Set `haptic_retained_config_active` to 1 when validated recovery is scheduled.
This describes use of the fallback, not proof that every replacement is Ready.
Clear it after a current render successfully converges across the fleet, and on
leadership loss. The bundled `HAProxyRetainedConfigurationActive` alert warns after
two minutes. Agent rejection and readiness remain independently observable.

## Limits and trust boundary

- Retained endpoint lists, routes, certificates, and fetched files stay unchanged.
  A Ready replacement can point at endpoints that no longer exist. Restoring
  capacity does not guarantee backend availability or apply new routing inputs.
- A controller supporting checkpoints must first observe and publish an
  acknowledged deployment. A first install, or an upgrade already blocked before
  that deployment, has no retained snapshot and keeps today's NotReady behavior.
- At least one controller must pass its startup load gate. If every process fails
  before leader components start, existing proxies keep serving but replacements
  cannot recover. Fix the startup error; this ADR does not bypass that gate.
- Agents reading Kubernetes themselves would need access to certificates in
  Secrets and duplicate publisher trust, selection, and version-validation logic
  in the data plane. That expansion is out of scope and unnecessary here.
- Owner references and hashes detect foreign or modified records under the
  existing Kubernetes RBAC trust boundary. They are not digital signatures against
  an administrator who can replace resources, ownership, and checkpoint pointers.
- Separate checkpoint objects duplicate storage and add publication writes. Retain
  complete units rather than sharing mutable children with the normal publisher.

## Verification

`TestRetainedConfigurationRecovery`, also registered in
`TestAllAcceptanceParallel`, uses the chart's real bootstrap and pod specification,
two controllers, and two HAProxy pods. It blocks a critical HTTP source, restarts
the standby to remove its accepted HTTP cache, deletes the leader, and scales the
fleet to three. Each new pod must become Ready and serve host X within two minutes.
Old pods keep identical apply counters, worker start metrics, and applied plan IDs.
The same test stops all controllers, starts two fresh ones, replaces the proxy
named in the checkpoint's historical receipt, and checks repair, per-pod
convergence, events, gauge transitions, and publication stability. The receipt
must survive its pod's removal. The bound excludes image download and cluster
provisioning.

`TestRetainedConfigurationSuddenFailover` repeats the scale-up and repair checks
with zero grace time when deleting the leader. `TestRetainedConfigurationFreshness`
removes the newer checkpoint pointer after a newer fleet deployment, restarts the
controllers, and proves the older checkpoint cannot start a replacement. Restoring
the newer pointer allows recovery even when the older pointer appears first.

`TestRetainedConfigurationTampering` changes the selected checkpoint's bytes without
changing its checksum. A cold leader must report the corruption and leave a new
pod without an applied plan or Ready status. `TestRetainedConfigurationChecksCurrentHAProxy`
checks restored declarations with the real current binary and rejects the removed
`nbproc` directive. Unit tests cover complete artifact reconstruction, incomplete
publication, forged ownership, mismatched content, pod disagreement, and the guard
against applying to configured pods. They also cover takeover during an intent
update, delayed writes from the previous leader, acknowledgement reordering, a
newer apply without a saved checkpoint, and fleet changes before dispatch.
`TestLeaderChangeReloadsNothing` remains the normal failover regression. Full
merge-train validation is required before merge.
