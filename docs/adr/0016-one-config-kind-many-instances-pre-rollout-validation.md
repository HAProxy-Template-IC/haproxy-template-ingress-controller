# ADR-0016: One config kind, many instances, validated before rollout

## Status

Accepted 2026-08-05. Prerequisites shipped earlier (!1541, !1542, !1543);
Decisions 1–6 are implemented in a single MR — see Sequencing for why one
release is safe.

**Partly superseded by [ADR-0017](0017-template-library-kind.md)**: the input
mechanism here — N `HAProxyTemplateConfig` objects marked `spec.partial`, merged
in `CRD_NAME` order — is replaced by one config referencing
`HAProxyTemplateLibrary` objects through `spec.libraryRefs`. Everything else
stands, including the removal of the `haproxytemplateconfigs` admission webhook
and its replacement by leader validation plus the startup load gate. The
strict-first/fast-later implementation was superseded by
[ADR-0020](0020-authoritative-render-validation-pipeline.md): every changed
render now passes the complete pipeline.
`spec.partial` never reached a release, so only snapshot consumers of `main`
were affected.

Revised four times. The first draft argued from the wrong rationale; the second
understated the stripper's cost; the third re-measured every figure after four
MRs landed and refuted five of them. This fourth revision corrects three
judgement errors the third made — all the same error, judging a component
against today's N=1 world instead of the world Decision 2 creates — and
replaces Decision 3's input mechanism after the original was shown not to fit
and its first replacement validated too late. Every claim is marked with how it
was established; see [Verification ledger](#verification-ledger).

## Context

Four shapes have been tried for the same problem — the merged config outgrew
etcd's per-object limit:

1. **One object, everything in it.** At defaults + `nginxIngress`: 1,564,098
   against a ~1,572,864 limit — 99.4%, failing as `etcdserver: request is too
   large` on `helm install` with no workaround (ADR-0014).
2. **One object per template library** (ADR-0014, !1440). Reverted: a fragment
   is not a config, so its CRD had to make every field optional, and validators
   already running in operators' clusters judged a fragment as a complete
   config.
3. **One config object + a companion `HAProxyValidationTests` kind** (!1450,
   current).

**What has changed since the revert, and why shape 2 is viable now:** the CRD
relaxation the revert objected to was performed anyway and never restored —
verified at HEAD, `spec.required` is absent and there are zero
`x-kubernetes-validations`. The price the revert refused to pay has been paid;
what remains is to collect what it bought. The other objection — validators
judging fragments as complete configs — is Decision 4's subject.

### Where the ceilings stand (measured 2026-08-05, all-vendor profile)

| | bytes | % of hard limit | % of its gate |
|---|---|---|---|
| `HAProxyTemplateConfig/haptic-config` | 1,064,413 | 67.7% of 1,572,864 | **96.7%** of 1,101,004 |
| `HAProxyValidationTests/haptic-config-tests` | 792,377 | 50.4% | — |
| Helm release Secret | 666,764 | 63.6% of 1,048,576 | **70.2%** of 950,000 |

Two corrections to earlier revisions, both material: **the release Secret is no
longer the binding constraint** (was 99.1% of gate; !1541 took it to 70.2%),
and **the gate-profile hole is closed** (!1542 gave both size checks a shared
`WORST_CASE_LIBS`). What remains is one ceiling: the object, at 96.7% of its
gate, with a *global* budget — one object holds every enabled library, so every
chart addition walks toward the same wall.

### The stripper: correctness is fixed, only bytes remain

The Scriggo comment stripper was a correctness defect *and* a byte lever. The
correctness half shipped (!1543): 1,182 comment sites corrected, an
anti-fusion lint in `make lint`, and **the chart renders identically with the
stripper deleted — 696/696 validationTests**, up from ~199/658 (before !1543 it
did not compile at all: 44 comments sat inside `{% … %}` blocks where `#` is a
lexer error).

The stripper is now **inert**. Deleting it is purely a size question:

| | release Secret (gate 950,000) | etcd object (gate 1,101,004) |
|---|---|---|
| today | 666,764 ✓ (70.2%) | 1,064,413 ✓ (96.7%) |
| **stripper deleted** | **860,340 ✓ (90.6%)** | **1,464,153 ✗ (133.0%)** |

One prerequisite, not the two earlier revisions claimed: only the object.
Trimming comments cannot substitute — 382,103 bytes across 486 snippets, and
deleting *every comment in the chart* lands at 97.7% of the gate with no
documentation left. It is sharding or nothing.

### Per-library sharding, tests inline — measured on today's tree

The budget goes from global to per-object. Measured by attributing every
rendered snippet, map, file, k8sResource and validationTest to the subchart
that declares it (all-vendor profile):

| shard | stripper ON | stripper DELETED | % of gate |
|---|---|---|---|
| gateway | 546,712 | **690,423** | **62.7%** |
| haptic-annotations | 481,287 | 577,354 | 52.4% |
| nginx-ingress | 176,056 | 205,829 | 18.7% |
| haproxy-ingress | 148,284 | 184,089 | 16.7% |
| base | 107,468 | 164,193 | 14.9% |
| (six more, all smaller) | | | |

The worst shard — **with its validationTests folded back in and every comment
kept** — sits at 62.7% of the gate with 410,581 bytes (59% growth) of headroom.
Two consequences:

- **Decision 1 costs nothing.** Earlier reasoning compared the *combined*
  792,377-byte tests object against a sharded config, as if tests stayed a
  monolith. They do not: per-library tests distribute across the same shards,
  and the table above already includes them.
- **The shard boundary is the library, not an arbitrary split.** A minimal
  two-object split was considered and rejected: it needs a rule for what goes
  where, that rule has no principle behind it, and it gets re-litigated at
  three shards — with each re-argument moving snippets between objects, which
  changes `CRD_NAME` order, which changes merge precedence. The per-library
  boundary already exists (it is the subchart split), needs no rule, and adding
  a library adds an object with nobody deciding anything.

**The controller machinery already exists.** `CRD_NAME` is parsed as a list
(`cmd/controller/run.go:118`), and `conversion.MergeSpecs`
(`conversion/merge.go:73`) merges N specs in argument order, later wins. The
chart renders one object; the controller was never the constraint.

## The admission webhook cannot validate a change set

**This is not a problem today, and that is the point.** The shipped chart
passes a single `CRD_NAME`, so `mergeWithSiblingConfigs` (`webhook.go:448`)
fetches no siblings and no intermediate state exists. The flaw is **latent, and
Decision 2 activates it**.

That function substitutes the incoming object and fetches the other configured
names **from the cluster as they are now**. Admitting `A` during a multi-object
change judges `A(new) + B(old) + C(old)` — a state in nobody's intent.
Kubernetes admits objects one at a time; a per-object webhook structurally
cannot see a change set. A coupled change — a snippet moves from one shard to
another, a rename spans two objects — is *individually* invalid in either
order, so the webhook denies a change whose end state is correct.

The rescue heuristic fires only on an appVersion difference
(`configvalidator.go:348`): chart upgrades are admitted with validation
switched off, while ordinary same-version edits — values change, GitOps
re-sync, `kubectl apply` — are judged strictly against the intermediate state
and **denied mid-batch**. The gap bites exactly where operators live. That is
one of five fail-open paths in admission (`configvalidator.go:283, 315, 505,
509, 566`), on top of `failurePolicy: Ignore` for this rule.

**And the merged view is all the webhook has.** Its completeness check,
`ValidateMergedCompleteness`, is by its own doc comment a check on the *merged*
config, "because a single HAProxyTemplateConfig of a merged set is legitimately
incomplete." Under Decision 2 it can only run on the sibling-merged
intermediate state — the same inconsistent read. Keeping the webhook does not
preserve its guarantees; it preserves code whose every check inherits the
mid-batch view.

## Decision

1. **Fold `validationTests` back into `HAProxyTemplateConfig`;** retire
   `HAProxyValidationTests`, `validationTestsSelector`,
   `requireValidationTests` and the test-resolution seam. All are still
   `[Unreleased]`, so this is a rewrite of unreleased notes and **must not be
   tagged BREAKING**. Depends on Decision 2 (combined, the two objects exceed
   etcd's hard limit); measured above, it costs the sharded shape nothing.
   The kind's CRD postdates the last release (added in !1446, absent from
   `v0.2.0-alpha.1`), so no released operator has it: delete it outright — no
   migration, no changelog cleanup note, per the repo's own
   nothing-shipped-nothing-BREAKING rule. Only the `[Unreleased]` notes are
   rewritten.
2. **Ship the chart as one instance per library**, merged by `CRD_NAME` order,
   latest wins — the !1440 shape, now with the union semantics (2a) that make
   it safe and without the CRD relaxation it originally forced (already paid).
3. **Add a `pre-install`/`pre-upgrade` Job** that validates the complete future
   set **before any object is applied** — mechanism in 3a, redesigned.
4. **Remove the `ValidatingWebhookConfiguration` rule for
   `HAProxyTemplateConfig`** and its Go handler, with the two replacements
   named in 4a. Entailed by Decision 2: every check the rule performs needs the
   merged view, and admission's merged view of a multi-object change is
   structurally wrong.
5. **Keep the startup load gate and the live gate** — fail-closed, and the only
   gates on paths that bypass Helm entirely (`kubectl`, drift, rollback,
   `--no-hooks`).
6. **Delete the Scriggo comment stripper.** Correctness shipped in !1543; only
   Decision 2 remains in front of it.

### 2a. Sharding requires new merge semantics — this is not free

**Reproduced against the real `MergeSpecs`**, not reasoned about. Two shards
declaring the same test name:

```
err       = <nil>
overrides = []conversion.SnippetOverride(nil)
merged    = {"description":"B's description",
             "assertions":[{"pattern":"B","type":"contains"}],
             "fixtures":{"ingresses":["A-fixture"]}}
```

A **Frankenstein test** — B's description and assertions, A's surviving
fixtures, a test neither author wrote, with no error and no override reported.
And `_global` fixture lists set by two sources are **replaced**, not
accumulated (only `from-B` survives).

Both existing duplicate-name guards die under sharding: the chart-time `fail()`
works only because every library folds into one accumulator, and the Go union's
hard error (`conversion/union.go`) becomes unreachable once the companion kind
retires — it runs *after* `MergeSpecs` and would see one already-flattened
source. The pre-rollout gate and the load gate both merge through `MergeSpecs`,
so without this work they would silently run the reduced suite. Validation
traded away with the delta unstated — RULE #2.

**Required, therefore, at any shard count:**

- `spec.validationTests` lifted out of the mergo accumulator and unioned per
  source: error on a non-`_global` duplicate, accumulate `_global`
  fixtures/httpResources/requires. Precedent: `migrationCoverage`,
  special-cased in the same file for the same reason. Decision 1 makes this
  *cleaner* — the union moves into `MergeSpecs` instead of living in a separate
  post-merge pass over a separate kind.
- A cross-instance duplicate-name **error** for `templateSnippets`, `maps`,
  `files`, `sslCertificates`, `k8sResources`, with `_global` exempted. The
  operator-override exemption is **positional, not marker-based**: collisions
  among sources 1..N−1 error; the *last* source in `CRD_NAME` — the operator's,
  by chart construction — may override anything, logged via the existing
  `SnippetOverride` path. It cannot key on `spec.partial`, because the main
  chart object is itself partial (it carries `podSelector` but not
  `haproxyConfig`, which base owns) — so **every** chart-rendered object gets
  `spec.partial: true`, and the CEL completeness rule in 4a guards exactly the
  hand-written standalone CR.
- A regression test asserting a duplicate test name across two instances is an
  error, not a log line.

### 3a. The pre-rollout gate: `preflight` in a hook Job

**The requirement is validation of the complete future set before any object
is applied.** Post-apply variants were considered and rejected: the CR is inert
until the controller loads it, but a post-apply failure degrades the operator's
signal from "test X failed, nothing changed" to a wedged rollout the load gate
is holding back.

**Two input mechanisms are dead, both by measurement:**

- *Reading the pending release Secret.* No release Secret exists under Argo CD
  — it renders with `helm template` and applies manifests — so a Job whose
  input is the release record hard-fails every Argo sync forever.
- *Carrying the rendered set in the Job manifest.* Hook manifests are stored in
  the release record. Measured: the config documents are 2,287,068 bytes raw;
  carrying them costs +410,064 compressed, putting the release payload at
  **102.7% of the hard 1,048,576 cap with the stripper on (121.2% off)**.
  `helm install` fails.

**Chosen mechanism: the Job runs `haptic-controller preflight` against the
image-embedded chart, fed the release's values.** All parts exist or are small:

- `preflight -f values.yaml` already renders the image-embedded chart
  in-process and runs the full load gate over the result — structural
  validation, the merged `validationTests`, `haproxy -c`
  (`cmd/controller/preflight.go`). The hook Job is the same pattern as the
  shipped `crd-upgrade-hook.yaml` (a `pre-install,pre-upgrade` Job on the
  controller image, weight -5/0, `before-hook-creation,hook-succeeded`), at a
  later hook weight so CRDs are already upgraded.
- **Values delivery:** the chart renders `.Values | toYaml` into a hook Secret
  at a lower hook weight; the Job mounts it and passes it to `-f`. Values are
  kilobytes — no size problem — and no more sensitive than the release Secret
  that already stores them (though under Argo this Secret is a *new* place
  values land; same namespace, same RBAC posture as the credentials Secret).
- **The version guard is mandatory, not optional.** `preflight` today falls
  back to the image-embedded chart with no check that it matches the chart
  being installed ("lockstep is only a default"). The Job must receive
  `{{ .Chart.Version }}` and hard-fail unless it equals the embedded chart's
  version — a drifted pair must fail loudly, never validate the wrong chart.
  An operator running a deliberately different image disables the hook by
  value.

**Failure semantics, verified against platform documentation:**

- **Helm:** a hook Job is blocking; if a pre-upgrade hook fails, the release is
  marked failed and **the main manifests are not applied**. The previous
  release keeps serving. (Caveat found while verifying: helm/helm#31690 — v4
  fails after timeout rather than immediately; annoying, not unsafe.)
- **Argo CD:** `pre-install`/`pre-upgrade` map to `PreSync`; "if any of them
  fails the whole sync process will stop and will be marked as failed" — main
  manifests not applied. `hook-weight` maps to sync-waves, the delete policies
  map. Two caveats to document: defining any *Argo* hook in the app makes Argo
  ignore all Helm hooks, and Argo cannot distinguish install from upgrade
  (every operation is a sync — harmless here, the Job behaves identically).
- **Flux:** helm-controller drives the Helm SDK, identical semantics;
  `disableHooks` is the opt-out.

**Known caveats, stated rather than hidden:**

- **`--no-hooks` / `disableHooks` skip it.** That is an explicit operator
  opt-out, the same class as `--force` — it does not make the gate worthless,
  and the load gate remains the fail-closed backstop on every such path. (An
  earlier revision argued the opposite; that reasoning would disqualify every
  hook ever written.)
- **Values round-trip is not perfectly faithful:** `.Values` in a template is
  the *coalesced* map, so an operator null-override that deletes a chart
  default does not survive re-overlay — the default resurrects in the hook's
  render. Adjacent to the known apiserver null-pruning issue. Document; if it
  bites, the fix is comparing the hook's rendered config names against the
  incoming set, which the Job can do cheaply.
- **No container runtime in the Job:** the vector/varnish compile checks are
  skipped, exactly as `preflight` already documents.
- **Schemas from the live cluster** (in-cluster kubeconfig), stated as RBAC —
  `--schema-dir` silently falls back to untyped access, which would make the
  hook strictly weaker than the load gate it fronts.
- **Capabilities must mirror the cluster, and today they do not.**
  `preflight.go:219-221` builds `DefaultCapabilities` and **unconditionally
  appends the Gateway API version** plus whatever `--api-versions` passes —
  no discovery. On a cluster without Gateway CRDs the hook therefore
  validates a *superset* of what deploys (gateway shard included where the
  real render prunes it). Superset-passing does not prove subset validity.
  The Job must derive capabilities from live discovery, the sibling
  requirement to schemas-from-live-cluster.
- **Merge order must be pinned.** `preflight` orders documents by template
  filename; the controller merges in `CRD_NAME` order, and `MergeSpecs` is
  order-dependent. Irrelevant at N=1, load-bearing under Decision 2: both
  must derive from one chart-emitted list, pinned by a test.
- **Job spec:** ≥256Mi (measured 215 MiB) and an emptyDir `/tmp`.
- **Recovery:** the Job validates only the incoming rendered set, never live
  state — a broken fleet must stay recoverable, the invariant `applycrds.go`
  already documents for this hook slot.

### 4a. What replaces the webhook — two things, named

Decision 4 removes one of **three** strict entry points (the normative SHALL in
`openspec/specs/reconciliation-pipeline/spec.md` lists them); watched-resource
admission keeps rendering the full config and running `haproxy -c` on every
Ingress create/update. What is genuinely lost is narrower, and each piece gets
a replacement that does not depend on a per-object view of a multi-object
change:

1. **Semantic validation of a config/template change at apply time.** No
   watched resource changes, so the surviving webhook rule never fires; the
   change reaches the leader's fast pipeline (syntax + schema only). Replace
   with either: the **strict** pipeline on the leader for config-triggered
   renders (~94 ms, config changes are rare, and the leader reads the whole
   `CRD_NAME` set — a consistent view, no sibling problem), or
   `validateConfig: true` as a paired chart change. Pick one in the
   implementing MR; the first is preferred because it also produces the error
   on the object's status rather than in a dataplane log.
2. **Completeness at apply time for operator-authored complete configs.** An
   object *not* named in `CRD_NAME` is validated standalone and denied today
   (`configvalidator.go:315-322`) — that is the one webhook behaviour whose
   view is *not* wrong under sharding, because there is nothing to merge with.
   Replace with a CRD-level CEL rule gated on a chart-set marker
   (`spec.partial: true` on shards; complete configs must satisfy the old
   `required` set). A spec field, not a label — CEL's access to metadata is
   restricted. This restores apply-time rejection **in the apiserver itself**,
   stronger than a webhook that can be unreachable (`failurePolicy: Ignore`).

**Fix regardless of this ADR** (defect in `main`, found while verifying): the
reconciliation-pipeline SHALL justifies the fast path partly on "the Dataplane
API runs its own `haproxy -c` server-side", but `validateConfig` defaults
`false` and renders `validate_cmd: /bin/true`
(`haproxy-deployment.yaml:409`). That limb is false today.

### 2b. Sharding trades apply atomicity for size — name the cost

The single object had one virtue no revision of this ADR has stated: **an
apply is atomic.** The controller never sees half a change. With N objects, a
coupled change applies as N sequential writes, and the live gate re-merges on
every event — so for the seconds between the first and last write, the
controller holds `A(new) + B(old)`: a state in nobody's intent, this time at
*reconcile* time rather than admission time.

Two backstops bound it, neither eliminates it:

- A merge or render **failure** on the intermediate state fails open — the
  previously published config keeps serving (`configloader/loader.go:119`).
- The structural-change debounce (default **2 s**, `values.yaml:1141`)
  coalesces a burst of CR writes into one reconcile; Helm and Argo apply a
  release's objects well inside that window in the common case.

What remains is the intermediate that **renders valid but means neither
intent** — e.g. a map entry moving between shards is briefly in neither — and
deploys transiently if the burst outruns the debounce. Transient, convergent,
and HAProxy reloads are hitless, but it is a real regression vs. one object.

**Considered and rejected:** stamping every shard with a set-checksum and
merging only when all N agree. It closes the window completely, and it also
means a hand-edited shard (checksum now disagreeing forever) is **silently
ignored until the next release** — a convergence stall with no error, worse
than the transient it prevents. Accept the transient; document it; keep the
debounce non-zero (already a standing rule).

### 5a. Gate defects that must be bounded alongside Decision 2

Verified, both live at N=1 today; sharding multiplies the operations that reach
them:

- An in-process reinit into a failing load path yields a permanently Ready,
  do-nothing controller (`beginIteration` re-arms the 90 s grace on every 5 s
  retry); a startup load-gate failure is a `Running` pod with `/healthz` 503,
  not CrashLoopBackOff — feedback is asynchronous via
  `ValidationStatus=Invalid`.
- **Blast radius via readiness:** 503 → NotReady → the webhook Service loses
  endpoints → the watched-resource rule (`failurePolicy: Fail`, no
  `namespaceSelector`) denies **every Ingress create/update in the cluster**.
  `replicaCount: 2` does not help; both replicas read the same merged set.

### 6a. Deleting the stripper — what is left

Remove the `regexReplaceAll` calls and pattern variables from
`templates/_libraries.tpl`. That is the whole change: comment-form correctness,
the statement-block conversions and the anti-regression lint shipped in !1543;
the RULE #3 rewrite shipped in !1545. The chart already passes 696/696 with the
stripper removed.

## Why the load gate stays

| path | hook (3a) | load gate | webhook (today) |
|---|---|---|---|
| `helm upgrade`, Flux, Argo sync | ✅ before apply | ✅ | ⚠️ per-object view |
| `helm rollback` | ❌ no Argo `pre-rollback` | ✅ | ⚠️ |
| `kubectl edit` / `apply` / drift | ❌ | ✅ | ⚠️ |
| controller restart | ❌ | ✅ | n/a |
| `--no-hooks` / `disableHooks` | ❌ opt-out | ✅ | ⚠️ |
| complete config, structural reject at apply | ❌ | not at apply | ✅ → CEL (4a) |
| coupled multi-object change | ✅ whole set | ✅ whole set | ❌ denies it |
| duplicate names across instances | ✅ via 2a | ✅ via 2a | ❌ |

**Withdrawn and still withdrawn:** operator-defined instances outside the chart
are not covered by the hook (it renders the chart) and not admissible-checked
once the webhook is gone; the CEL rule in 4a covers their structural
completeness, the load gate the rest. If `extraConfigNames` is ever offered,
this table gains a row that is ❌/✅/❌ and the offer must say so.

## Sequencing

**One implementation MR, one release** (operator decision, to conserve CI
minutes — every gate runs locally first). This supersedes the two-release plan,
which existed for exactly one reason: the running *old* webhook denies new
per-library objects standalone during a pre-shard → post-shard upgrade (the
2026-07-28 provenance failure). In a single release that ordering is
guaranteed by mechanism instead:

**The `apply-crds` pre-upgrade hook strips the legacy `HAProxyTemplateConfig`
rule from the live `ValidatingWebhookConfiguration` before any manifest is
applied.** Hooks precede all manifests on Helm and map to PreSync under Argo
(both verified against platform docs above), so the old rule is gone before
the first shard reaches the apiserver, regardless of kind-sort order. The
migration stays exercised forever: `scripts/test-chart-upgrade.sh` replays
every published release oldest-first, and every pre-shard release keeps this
path hot (the no-can-kicking rule in `charts/CLAUDE.md` is satisfied by
construction). Raw `kubectl apply` flows without hooks may see one failed
apply of the shard objects until the VWC lands; a second apply converges.

Within the MR, the work decomposes as: 2a union semantics → retire the
companion kind → status stamping → webhook removal + CEL + strict-on-leader →
apply-crds VWC strip → preflight upgrades (version guard, discovery
capabilities, merge order) → chart sharding + hook Job + stripper deletion →
tests and docs. Local gauntlet before push: `make check-all`, all
template-test profiles, both size gates, helm-unittest,
`test-chart-upgrade.sh`, and the e2e suites on a fresh kind cluster.

**Explicitly deferred, not forgotten:** the 5a readiness blast-radius bound.
It predates this ADR, exists at N=1, needs its own RULE #2 delta discussion
(live-reinit and startup failures need different mechanisms), and burying that
design in a mega-MR is how it would get rubber-stamped. It is the first
follow-up after this MR merges.

## Alternatives considered

**Keep the webhook, make it batch-aware.** Not possible: admission has no
transaction and no view of the apply set.

**Drop the load gate as well.** Rejected — it is the only gate on five rows of
the table above.

**Keep the companion tests kind, shard the config only.** Now measurably
pointless: per-library tests ride their own shards at no cost (worst shard
62.7% *with* tests), and keeping the kind keeps the seam, the selector, the
discovery watch and the four-site agreement problem.

**Two shards instead of per-library.** Rejected: the boundary is arbitrary,
gets re-litigated at three, and each re-argument reshuffles `CRD_NAME` order —
merge precedence — for no saving that matters (worst per-library shard already
has 59% headroom).

**Post-apply validation Job.** Rejected: validates after objects land; a
failure wedges the rollout instead of preventing it. The CRD set must be
validated before the upgrade touches anything.

## Verification ledger

Established on 2026-08-05 against `main`:

| Claim | Method | Verdict |
|---|---|---|
| Frankenstein test across shards; `_global` replaced not accumulated | ran `MergeSpecs` | **confirmed** |
| worst per-library shard 690,423 = 62.7%, tests inline, stripper off | measured, per-subchart attribution | **confirmed** |
| carrying the rendered set into the hook: 102.7% / 121.2% of hard cap | measured (gzip -9 + base64) | **confirmed — mechanism dead** |
| Helm: pre-upgrade hook failure aborts before manifests; Jobs block | Helm docs + issue tracker | confirmed (v4 timeout caveat: helm/helm#31690) |
| Argo: pre-upgrade → PreSync; PreSync failure stops the sync | Argo docs | confirmed (Argo-hooks-defined caveat) |
| chart precedent: pre-upgrade Job on controller image | `crd-upgrade-hook.yaml` | confirmed |
| `preflight -f` renders embedded chart + runs load gate; **no version guard today** | code | confirmed |
| `ValidateMergedCompleteness` is a merged-view check by design | its doc comment | confirmed — webhook keeps no consistent guarantee under sharding |
| `spec.required` absent, 0 CEL rules at HEAD | parsed CRD | confirmed — revert's price already paid |
| Secret blocks stripper removal | re-measured | **REFUTED** (90.6% ✓) |
| removing the webhook leaves no semantic validation | spec + code | **REFUTED** (1 of 3 entry points) |
| earlier sharded-tree figures (b0c7ea1c) | superseded | replaced by fresh per-library measurement above |
| preflight capabilities: hardcoded, Gateway API force-appended | code (`preflight.go:219-221`) | **confirmed gap** — hook must use live discovery |
| CEL validation rules available at the k8s floor | CI matrix ≥ v1.32, CEL GA 1.29 | confirmed |
| structural-change debounce bounds the multi-object apply window | `values.yaml:1141` (2 s default) | confirmed — probabilistic, not a guarantee (see 2b) |
| old webhook denies new shard objects during pre→post-shard upgrade | provenance (2026-07-28) + `configvalidator.go:315-322` | **confirmed — forces the release-ordering constraint in Sequencing** |
