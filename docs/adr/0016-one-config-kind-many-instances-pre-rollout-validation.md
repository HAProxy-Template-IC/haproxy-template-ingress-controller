# ADR-0016: One config kind, many instances, validated before rollout

## Status

Proposed. No code.

Revised after two adversarial reviews (62 + 57 findings raised, 30 + 24
surviving independent verification). The first draft argued from the wrong
rationale — a latent webhook flaw rather than the recurring size ceiling — and
its successor understated the stripper's cost, prescribed a migration that does
not compile, and missed that the release Secret is a second and tighter ceiling.
All are corrected below. The subchart migration in 6a was measured by performing
it, not projecting it.

## Context

Four shapes have been tried for the same problem — the merged config outgrew
etcd's per-object limit:

1. **One object, everything in it.** At defaults + `nginxIngress`: 1,564,098
   against a ~1,572,864 limit — **99.4%**, 8,766 of headroom, failing as
   `etcdserver: request is too large` on `helm install` with no workaround
   (ADR-0014).
2. **One object per template library** (ADR-0014, !1440). Reverted: a fragment is
   not a config, so its CRD had to make every field optional, and validators
   already running in operators' clusters judged a fragment as a complete config.
3. **One config object + a companion `HAProxyValidationTests` kind** (!1450,
   current):

   | object | gate units | % of 1,572,864 |
   |---|---|---|
   | `HAProxyTemplateConfig/haptic-config` | 1,049,280 | 66.7% |
   | `HAProxyValidationTests/haptic-config-tests` | 761,399 | 48.4% |

   Units are what `scripts/check-cr-size.py` counts, and the gate excludes
   `metadata.managedFields` (≈21 KB at 485 snippets) and Helm's release
   annotations — real objects are larger than the table.

Shape 3 works but costs two kinds and a test-resolution seam that must agree
across the load gate, the live gate, `validate`, and admission. The companion
kind has **no** admission rule and no watch, so a bad tests object is caught only
at the next config change or at restart.

### The ceiling was mitigated three times, never removed

Each shape bought headroom; none changed the mechanism. The budget is still
**global** — one object holds every enabled library — so every chart addition
walks back toward the same wall, and the fix is always another round of
subtraction.

**There are two ceilings, and the tighter one is not the object.** The etcd
object is gated at 1,101,004 (70% of 1,572,864); the Helm release Secret is
gated at 950,000 by `scripts/check-chart-release-size.py`.

| ceiling | today | % of its gate | growth before CI is red |
|---|---|---|---|
| etcd object (worst) | 1,049,280 | 95.3% | 4.9% |
| release Secret, as `chart-size-check` renders it (2 libs) | 913,200 | 96.1% | 3.9% |
| **release Secret, all-libraries profile** | **941,648** | **99.1%** | **0.9%** |

Quoting 66.7% — the fraction of etcd's *limit* — makes the object look
comfortable. It is not. And the binding constraint on `main` is the Secret at
0.9%, which **sharding does not move at all**: the Secret is a sum over every
enabled library, not a per-object maximum.

**A gate hole, independent of this ADR and worth fixing before it.** The two size
checks render *different* library profiles — `cr-size-check` enables four
libraries, `chart-size-check` two — so CI has never evaluated the 99.1% figure.
Align them first; it changes which problem this ADR is solving.

### The workarounds have become defects

Staying under a global ceiling forced byte-shaving passes that are now
liabilities in their own right. The Scriggo comment stripper is the clearest:

- It strips `{#- … -#}` from `templateSnippets`, where that form exists to fuse
  the surrounding lines. The strip deletes the line and leaves the newline, so
  fusion silently does not happen. Census of the 843 comments: **470** are
  mid-template own-line sites where this genuinely suppresses fusion, 258 are
  leading doc headers and output-neutral, and **44 sit inside Scriggo statement
  blocks** — see below.
- The same file holds the `files`/`maps`/`k8sResources` stripper to the opposite
  standard, refusing to touch that form precisely because doing so is unsafe.
- Consequently a config rendered **through Helm** and the *same* config
  **hand-written as a CR** produce different HAProxy output at those 470 sites —
  and the CR is a first-class API. At the 44 statement-block sites the divergence
  is sharper still: the hand-written CR does not render differently, it fails to
  compile.

Measured cost of removing it: **+394,272 on the object** (1,049,280 → 1,443,552)
and **+178,276 compressed on the release Secret** (913,660 → 1,091,936 = 104.1%
of the hard 1,048,576 cap, i.e. `helm install` fails). Both figures are larger
than this ADR's first draft claimed, which is why Decision 6 is ordered behind
*two* prerequisites rather than one.

### Sharding changes the budget from global to per-object

This is the actual argument for Decision 2, and it is what makes Decision 6
possible. Measured on the sharded tree that already exists in git (`b0c7ea1c`,
the !1440 shape — 12 config objects, tests inline):

| | worst object | % of gate | headroom |
|---|---|---|---|
| today: one object, stripping ON | 1,049,280 | 95.3% | 5% growth |
| sharded, stripping ON | 556,881 | 50.6% | 98% growth |
| **sharded, stripper deleted** | **700,671** | **63.6%** ✓ | **57% growth** |

Keeping every comment costs the worst shard +143,790 bytes and it still passes
with 400 KB to spare. The **object** ceiling stops being a recurring crisis and
starts scaling with library count, and the byte-shaving that produced the defects
above is no longer needed on that axis.

It does **not** fix the Secret. That ceiling is a sum, so it needs the separate
lever in 6a, and after Decision 6 spends most of that lever back the ADR owes an
answer to "what removes the Secret ceiling the *next* time".

**The machinery for many instances already exists.** `CRD_NAME` is parsed as a
list (`splitConfigNames`), and `conversion.MergeSpecs` merges N specs "in
argument order, later wins", identity from the last source. The chart renders
one object; the controller was never the constraint.

## The admission webhook cannot validate a change set

**This is not a problem today, and that is the point.** The shipped chart passes a
single `CRD_NAME` and renders one config object, so `mergeWithSiblingConfigs`
fetches no siblings and no intermediate state exists. The flaw is **latent, and
Decision 2 activates it** — which is why Decision 4 is part of this ADR rather
than a separate cleanup. Sharding without removing the webhook would ship the
failure below.

`mergeWithSiblingConfigs` (`pkg/controller/webhook.go`) substitutes the incoming
object and fetches the other configured names **from the cluster as they are
now**. Admitting `A` during a multi-object change judges `A(new) + B(old) +
C(old)` — a state in nobody's intent. Kubernetes admits objects one at a time;
there is no batch, so a per-object webhook structurally cannot see the change set.

The rescue is a heuristic, and fires only on an appVersion difference:

```go
// deferTemplateFailureOnSkew
if v.runningConfigVersion == "" || crVersion == "" || crVersion == v.runningConfigVersion {
    return nil, false   // no deferral — deny
}
```

- **Chart version bump** → skew → template failures admitted with a warning.
- **Same-version change** — values edit, GitOps re-sync, `kubectl apply` — → no
  skew → the intermediate merged state is judged strictly → **denied mid-batch**.

The gap therefore bites where it is least expected: not on upgrades, but on
ordinary same-version edits. And the mechanism that rescues upgrades does so by
switching validation off, citing *"the target controller's fail-closed load gate
re-validates authoritatively."*

That is one of **five** fail-open paths in admission (`configvalidator.go:392,
423, 439, 478, 507/511`, plus `foldCompanionTests:566-570`), on top of
`failurePolicy: Ignore`. All defer to the same backstop.

**Not on that list, and important:** an object *not* named in `CRD_NAME` is
validated **standalone and denied** on failure (`configvalidator.go:319-322`,
pinned by `tests/e2e/haproxytemplateconfig_admission_test.go`). That is a gate
Decision 4 removes, not a fail-open path it fixes.

## Decision

1. **Fold `validationTests` back into `HAProxyTemplateConfig`;** retire
   `HAProxyValidationTests`. The companion kind, `validationTestsSelector` and
   `requireValidationTests` are all still in `[Unreleased]`, so this is a rewrite
   of unreleased notes and **must not be tagged BREAKING**.
2. **Ship the chart as N instances**, merged by `CRD_NAME` order, latest wins.
3. **Add a `pre-install`/`pre-upgrade` Helm hook** running the full suite over
   the complete rendered set before any object is mutated.
4. **Remove the `ValidatingWebhookConfiguration` rule for
   `HAProxyTemplateConfig`** and its Go handler.
5. **Keep the startup load gate and the live gate.**
6. **Delete the Scriggo comment stripper**, in both its `templateSnippets` and
   its `files`/`maps`/`k8sResources`/`sslCertificates` forms — preceded by
   converting the 44 comments inside `{% … %}` / `{%% … %%}` statement blocks to
   Go `//` comments, without which the chart does not compile.

### 2a. Sharding requires new merge semantics — this is not free

`MergeSpecs` deep-merges maps and replaces lists. Reproduced against the real
function: two shards colliding on a test name give `err = nil`, no override
reported, and a **Frankenstein test** — winner's `assertions`/`description`,
loser's surviving `fixtures`, a test neither author wrote. `_global` fixture
lists set by two sources are replaced, not accumulated.

Both existing duplicate-name guards die: the chart-time `fail()`
(`_libraries.tpl:357-367`) works only because every library folds into one
accumulator, and the Go union's hard error (`conversion/union.go:76-81`) becomes
unreachable once the companion kind retires, since `unionDiscoveredValidationTests`
runs *after* `MergeSpecs` and would see one already-flattened source.

This is not cosmetic: the pre-rollout hook merges through the same `MergeSpecs`,
so the gate this ADR calls "stronger" would itself run the reduced suite, and the
load gate would pass it. Validation traded away with the delta unstated.

**Required, therefore:**

- `spec.validationTests` lifted out of the mergo accumulator and unioned per
  source (`UnionValidationTests` semantics: error on a non-`_global` duplicate,
  accumulate `_global` fixtures/httpResources/requires). The precedent is
  `migrationCoverage`, special-cased in the same file for exactly this reason.
- A cross-instance duplicate-name **error** for `templateSnippets`, `maps`,
  `files`, `sslCertificates`, `k8sResources`, with `_global` and the documented
  operator-override escape hatch exempted.
- A regression test asserting a duplicate test name across two instances is an
  error, not a log line.

Scope note: uniquely-named tests survive, and today's three `_global`
contributors are key-disjoint, so the bundled set does not break on day one. The
delta is losing the guards that would make Decision 2 safe.

### 3a. What the hook validates, and how it gets it

Unspecified in the first draft; both obvious implementations are blocked.

- **Carrying the rendered set into the Job does not fit.** Helm stores hook
  manifests in the release record. The release Secret is at **913,652 /
  1,048,576 (87.1%)** today; appending the config documents takes gzip -9 from
  274,528 → 524,459, and ×4/3 base64 → **1,246,893 = 118.9%** of the hard limit.
  `helm install` fails. Sharding splits those bytes, it does not reduce them.
- **`preflight` validates the wrong chart.** `resolveChartDir` falls back to the
  image-baked `/usr/share/haptic/chart`. `.gitlab-ci.yml` already says so:
  "`preflight`, which renders the embedded chart, validates a config that differs
  from the deployed one." Lockstep is only a default.

**Chosen mechanism:** the Job reads the **pending release Secret**
(`Release.Manifest`, written before hooks run), selects the config documents, and
hands them to `validate -f`. It must **hard-fail if the release cannot be read** —
never skip.

**This mechanism does not work under Argo CD, and the hard-fail rule makes that
permanent.** Argo renders with `helm template` and applies the manifests — no
`helm install` runs, so **no release Secret exists**. The PreSync Job would
hard-fail on every sync, forever, on a row the table below marks ✅. Either the
Job must accept a chart-rendered ConfigMap as an alternative input (and then the
release-Secret ceiling in 6a applies to it), or Decision 3 must state plainly
that it covers Helm and Flux but not Argo — in which case Argo installs are
carried by the load gate alone. Resolve this before building; it is the
difference between Decision 3 being a gate and being a Helm-only convenience. If `preflight` is chosen instead, a chart-version/appVersion equality
check is mandatory so a drifted pair fails loudly.

Also required:

- **Merge order.** `preflight` sorts by template filename; the controller merges
  in `CRD_NAME` order, and `MergeSpecs` is order-dependent. Both must derive from
  one chart-emitted list, pinned by a test.
- **Schemas.** `validate` takes only `--schema-dir`; unset, typed access silently
  falls through to untyped. `preflight` treats that as forbidden — "a run with no
  schemas would report a pass the controller's own load would not" — and defaults
  to the live cluster. Without a schema source the hook is **strictly weaker than
  the load gate**, and `--schema-dir` also strips features against bundled rather
  than installed CRD versions. The hook must take schemas from the live cluster,
  and that must be stated as RBAC.
- **RBAC.** Reading the pending release Secret means `get` on Secrets in the
  namespace that also holds the Dataplane credentials Secret; live schemas need
  cluster read. Compare the existing hook's deliberately narrow grant.
- **Job spec.** Deployment-equivalent memory (≥256Mi; measured 215 MiB) and an
  emptyDir at `/tmp`. The container runtime is absent in a Job, so
  `checkRenderedSidecarConfigs` skips the vector/varnish checks.
- **Recovery.** `applycrds.go` documents the opposite invariant for this hook
  slot: best-effort on purpose, because "aborting would turn a webhook we merely
  could not reach into an upgrade the operator cannot run at all… recovering a
  fleet that is already broken." The hook must validate only the incoming
  rendered set, never live state, so a broken fleet stays recoverable.
- **`--no-hooks` disables it wholesale**, as do Flux's `disableHooks`. The hook
  therefore contributes **zero** guarantee on any path an operator can switch off
  with a flag. Only the load gate is load-bearing; the hook is the early,
  complete, high-signal gate on top.

### 4a. What must change with the webhook

- **The fast reconcile pipeline may skip `haproxy -c` *because* this webhook
  exists** — four code sites plus a normative SHALL
  (`openspec/specs/reconciliation-pipeline/spec.md:105`). What is lost is the
  semantic phase over a render against the cluster's real resources: a dangling
  `use_backend` is rejected by `haproxy -c` and **accepted** by the syntax+schema
  check the fast path runs. **There is nothing downstream to defer to:**
  `charts/haptic/values.yaml:1318` sets `validateConfig: false`, which renders
  `validate_cmd: /bin/true`, so the dataplane's server-side `haproxy -c` always
  exits 0. An invalid body is written to disk fleet-wide first and fails only at
  `verifyReload`. Under RULE #2 that is validation traded away, not moved — so
  either keep semantic validation on the leader path (~94 ms per render) or flip
  `validateConfig` to `true` as a paired chart change with its stated cost.
- **Completeness.** ADR-0014 dropped four `Required` markers and named the
  webhook as the compensation; !1450 never restored them (`spec.required` is
  absent at HEAD, no `x-kubernetes-validations`). `ValidateMergedCompleteness`
  has three callers — admission, `ValidateStructure` (which the load and live
  gates both reach), and `validate` — and Decision 4 deletes the only
  **apply-time** one. The survivors are post-apply and fail open. Decision 2
  makes `required:` unrestorable, since a shard legitimately has no
  `podSelector`. Name the replacement (a CEL rule scoped to non-shard objects, or
  a retained narrow webhook running only this gate) or drop Decision 4.
- Spec deltas for `reconciliation-pipeline` and `validating-webhook`, dead values
  keys, two Helm unittests, and six doc pages.

### 5a. What the gates actually do

The first draft's "only startup is fatal" is **false**. Corrections:

- The live gate fails open with the previously published config still serving
  (`configloader/loader.go:122-123`) — but an in-process **reinit** into a
  failing load path yields a permanently Ready, do-nothing controller, because
  `beginIteration` re-arms the 90 s grace on every 5 s retry.
- A load-gate failure is **not** CrashLoopBackOff: the pod stays `Running` with
  restarts at 0 and `/healthz` 503. Feedback is asynchronous, via
  `ValidationStatus=Invalid` on the object.
- **Blast radius via readiness:** 503 → NotReady → the webhook Service loses
  endpoints → the watched-resource rule (`failurePolicy: Fail`, no
  `namespaceSelector`) denies **every Ingress create/update in the cluster**.
  `replicaCount: 2` does not help; both replicas read the same merged set.

Both defects exist at N=1 today. Decision 2 multiplies the operations that reach
them, so a bounded posture must land alongside.

### 6a. Deleting the stripper is a goal, not a side effect

The stripper is a correctness defect that exists only to buy CR bytes (see
Context). Decision 2 removes the reason for it, so it goes:

- Remove the five `regexReplaceAll` calls and the four pattern variables from
  `templates/_libraries.tpl`.
- Authors write `{# … #}` or `{#- … -#}` and get exactly the whitespace
  semantics they asked for, on every path — Helm-rendered or hand-written CR.
- The **777** own-line `{#- … -#}` sites stop having their fusion silently
  changed, and the two sections stop being held to different standards.

**Deleting it does not change output — it stops the chart compiling.** 44
comments sit inside Scriggo statement blocks, where `#` is a lexer error in
*either* marker form:

```
Error: compiling templates: compiling template 'haproxy.cfg':
util-access-log-targets:29:4: syntax error: invalid character U+0023 '#'
```

Converting all 44 to `{# … #}` reproduces that failure byte-identically. They
must become Go `//` comments, which Scriggo accepts in statement context. 43 are
in `templateSnippets` across 9 snippets, 1 in `files/vector.yaml`. A chart-lint
check should fail on `{#` inside a statement block so the class cannot return.

**The whitespace controls get fixed, not worked around.** Removing the stripper
means Scriggo processes those 777 sites, so the fusion their `-` markers ask for
finally happens — and at most of them that is not what the author wanted. They
wrote `{#- … -#}`, saw un-fused output because the merge-time strip had deleted
the line, and adapted to it. The source has been lying about its own intent; the
stripper was hiding it. So Decision 6 includes correcting each marker to what the
site actually needs — overwhelmingly the non-stripping `{# … #}` form, which
reproduces today's shipped and tested output, **except at the 44 statement-block
sites, where `{# … #}` is the same lexer error and the answer is `//`**.

Two constraints on that migration:

- **It is not byte-identical.** An own-line `{# … #}` that Scriggo processes
  leaves a blank line, where the stripper deleted the line outright. Harmless in
  HAProxy config, map files and YAML — but the proof of correctness is a render
  diff **ignoring blank lines**, plus a green validationTest suite, not a plain
  diff.
- **It needs a parser, not a regex.** Two bulk-regex attempts corrupted content:
  mixed `{#- … #}` delimiters exist, and a non-greedy multi-line match runs on to
  the next `-#}` and swallows the config between. Convert by locating each
  comment's real delimiters, and gate the result on the render diff above.
- **Budget the per-site work from the measured scale.** Control (stripper on):
  658 tests pass. With the 44 fixed but markers unmigrated: **199 pass, 459 fail**
  (458 `haproxy_valid`) — real fusion producing invalid HAProxy, e.g.
  `set-var(txn.listener_port) var(txn.dst_port)http-request set-var(…)` →
  `missing comma after fetch keyword`. With a blunt marker pass: **654 pass, 4
  fail**. Those last four are why this is per-site, not mechanical.
- Update the root `CLAUDE.md` RULE #3 note, which currently tells authors that
  comment length is free because the stripper removes it. After this it is not
  free — it is paid for out of a per-object budget with 57% headroom, which is
  the honest framing.

**Two prerequisites, not one.** Decision 6 needs *both* ceilings cleared:

| | release Secret (gate 950,000) | etcd object (gate 1,101,004) |
|---|---|---|
| today | 913,660 (96.2%) | 1,049,280 (95.3%) |
| subcharted | 627,124 (66.0%) | 1,049,280 (unchanged) |
| **subcharted + stripper deleted** | **806,244 ✓ (84.9%)** | **1,443,552 ✗ (131%)** |

The subchart move **does** pay for the stripper on the Secret, with 143,756 to
spare — and does nothing for the object, which only Decision 2 clears.

The lever on the Secret is structural: **`charts/**` is excluded from the stored
file set, while `libraries/**` is stored raw *and* again in the rendered
manifest.** Moving each library into a subchart removes the raw copy. This is
**independent of Decision 2** — `b0c7ea1c` shards 12 objects with `libraries/`
still raw, and `f9b894b4` subcharted vector at N=1 — so it ships alone, first.
Still stored raw today:

| | bytes |
|---|---|
| `libraries/base.yaml` | 226,977 |
| `libraries/spoa-hub/` | 161,483 |
| `libraries/ingress.yaml` | 88,539 |
| `libraries/ingress-annotations-compat.yaml` | 84,556 |
| `libraries/ssl.yaml` | 66,838 |
| **total** | **628,393** |

**Measured, not projected** — all five moves performed and verified:

| library | `condition:` | saving |
|---|---|---|
| `ssl.yaml` | `…templateLibraries.ssl.enabled` | −34,540 |
| `ingress.yaml` | `…ingress.enabled` | −31,576 |
| `ingress-annotations-compat.yaml` | `…ingressAnnotationsCompat.enabled` | −31,172 |
| `spoa-hub/` | **none — see below** | −70,676 |
| `base.yaml` | `…base.enabled` | −118,580 |
| **total** | | **−286,544** (913,668 → 627,124) |

Render-identical byte-for-byte on the default and all-libraries profiles;
`helm unittest` 429/429. The CR object is unchanged: subcharting moves *source*
bytes out of the Secret and does nothing to the rendered object.

**`spoa-hub/` cannot take a `condition:`.** Its enable predicate is an `or` over
a template helper, and Helm's `condition:` accepts only values paths. Adding the
obvious one emptied `backend spoa-hub` (6 → 0) on the default profile. Ship it
with no `condition:` and a comment saying why.

**The `changes:` paths move in the same commit as each library.**
`grep -n "charts/haptic/libraries" .gitlab-ci.yml` → 11 lines in 5 blocks, and
`.rules-spoa-chart` is `!reference`d by 11 jobs (every e2e shard, both
conformance suites, `test-helm-defaults`). A stale path never matches and never
errors; the file's own comment records this regression shipping before (!849).
Add a lint asserting every `charts/haptic/charts/*/` appears in that block, and
update `scripts/check-migration-coverage.sh:36` and
`scripts/test-chart-upgrade.sh:376`, which hardcode moved paths.

## Why the load gate stays

| path | hook | load gate | webhook (today) |
|---|---|---|---|
| `helm upgrade`, Flux, Argo full sync | ✅ | ✅ | ✅ |
| `helm rollback` | ❌ no Argo `pre-rollback` | ✅ | ✅ |
| `kubectl edit` / `apply` / drift | ❌ | ✅ | ✅ |
| controller restart after any of the above | ❌ | ✅ | n/a |
| `--no-hooks` / `disableHooks` | ❌ | ✅ | ✅ |
| rejects a structurally incomplete object at apply | ❌ | not at apply | ✅ |
| renders against live resources with `haproxy -c` | ❌ | ❌ | ✅ |
| duplicate names across instances | ❌ | ❌ | chart `fail()` today |

**Withdrawn from the first draft:** the row claiming operator-defined instances
outside the chart are covered by the load gate. They are not — an object not in
`CRD_NAME` is neither merged nor loaded, and `CRD_NAME` is a scalar rendered from
`controller.configName` with no append mechanism. Either the chart grows an
`extraConfigNames` key — and then **nothing** validates those objects at apply
once the webhook is gone — or the invitation is withdrawn. Decide before building.

## Consequences

**Gained.** One kind instead of two. No sibling-merge, skew-deferral or
fail-open reasoning in admission — code that cannot work correctly is deleted
rather than patched. Validation runs over the complete intended set. Coupled
multi-object changes stop being denied at the same chart version.

**Lost.**

1. No in-cluster apply-time completeness check on any path, and the CRD
   `required:` cannot be restored under sharding.
2. No `haproxy -c` over a live-resource render at config acceptance; a
   live-state-only break surfaces at the Dataplane push and, once promoted, as
   cluster-wide Ingress denial.
3. The standalone-validation **deny** for hand-written non-chart objects.
4. The duplicate-name guards (see 2a).

**Gained, and the reason to do this at all: the ceiling stops recurring.** The
worst object goes from 95.3% of the gate to 63.6% *with every comment kept*
(table in Context). Headroom scales with library count instead of being consumed
by it, and the byte-shaving passes that produced the correctness defects are no
longer needed.

**Size is measured.** Reproduce with `git worktree add --detach <dir> b0c7ea1c`
then `make cr-size-check`; delete the `regexReplaceAll` calls in
`templates/_libraries.tpl` for the stripper-off figure. Caveats: these are
2026-07-28 sizes (+45 tests since; gateway still ≈55% of the gate if all growth
landed there), and the open question is not *whether* it fits but the **split
rule** if one library alone approaches the gate.

**Argo runs the hook, with caveats.** `pre-install`/`pre-upgrade` → `PreSync`,
and a failed `PreSync` blocks the sync. But `pre-rollback`/`post-rollback` are
unsupported, and any `argocd.argoproj.io/hook` annotation makes Argo ignore
**all** Helm hooks.

**Documentation must not carry the guarantee.** "Run the tests in CI" and "don't
bypass hooks" are recommendations. Enforcement is the load gate; the hook is the
early gate that an operator can disable.

## Migration

**The first draft credited Decision 4 with saving this. That is wrong.** During
the upgrade that shards, the judge is the **old controller pod** with its old
single-name `CRD_NAME`. A new shard name is not in that list → `managed=false` →
validated standalone → hard-denied on `pod_selector: match_labels cannot be
empty`. The skew rescue cannot help: `parseAndGate` returns before
`deferTemplateFailureOnSkew` is reachable, and `failurePolicy: Ignore` does not
cover an explicit deny.

What actually saves it is `retargetConfigWebhook` (`applycrds.go:118,244-274`),
which moves the live hook to a path the old controller 404s on. Three hazards:

1. **Release sequencing.** The retarget is a no-op the instant the baseline
   already serves the versioned path. Land this before any release carrying
   `/validate/config/v1`, or bump the path.
2. **`crds.upgradeJob.enabled: false`** skips the retarget entirely.
3. The retarget is best-effort and swallows its failure.

Also required:

- **Retiring a kind is not free.** The apply-crds ClusterRole has no `delete`
  ("the hook never removes CRDs — data-loss safety") and Helm never deletes from
  `crds/`. Removing the file leaves the CRD registered forever with an orphaned
  761 KB object in etcd. Document a `kubectl delete crd` step, or say plainly
  that it is left behind.
- A `scripts/test-chart-upgrade.sh` phase replaying the old-webhook-live case
  (note `:71-73` currently excludes alphas).

## Sequencing

The smallest shippable increment is **the subchart migration alone** — it depends
on no decision in this ADR, is worth −286,544 on the tightest gate in the repo,
and is reversible by revert.

| MR | content | must land together |
|---|---|---|
| 0 | Align the two size gates' library profiles (the hole in Context) | — |
| 1 | Subchart `ssl`, `ingress`, `ingress-annotations-compat`, `spoa-hub` (no `condition:`), `base` | the 11 `.gitlab-ci.yml` paths + 2 script paths, in the same commit as each move |
| 2 | Source-comment migration: 44 → `//`, then per-site marker correction | the `{#`-in-statement-block lint; gate on 658/658 |
| 3 | Decisions 1 + 2 + 2a (fold the tests kind, shard, union semantics) | the `config_shape_test.go` overturn, status and deletion semantics |
| 4 | Decisions 3 + 5 (hook, keep both gates) | RBAC, schema source, merge-order pin |
| 5 | Decision 6 (delete the stripper) | strictly after 1 + 2 + 3 — needs Secret **and** object |
| 6 | Decision 4 (remove the webhook) | only with the semantic-validation replacement named above |

MRs 0 and 1 are worth doing whether or not the rest of this ADR proceeds.

## Alternatives considered

**Keep the webhook, make it batch-aware.** Not possible: admission has no
transaction and no view of the apply set.

**Drop the load gate as well** (the proposal that prompted this ADR). Rejected —
the delta is negative on six rows of the table above, and the code's own
fail-open comments name the load gate as the enforcer.

**Keep the companion tests kind.** Size stays comfortable and no sharding is
needed, but the two-kind seam and the sibling problem both remain. Reasonable if
the sharding work is judged too large.

## Before building

- **Rebuild the chart splitter and `CRD_NAME` list generator** deleted by !1450;
  keep them agreeing via a chart unit test; restore `metadata.name` document
  selectors in the eight suites asserting on `haproxytemplateconfig.yaml`.
- **Overturn `tests/e2e/config_shape_test.go` explicitly.** It asserts exactly
  one config object — *"a configuration spread across objects cannot be validated
  as a whole"* — that tests are not inlined, and that a companion exists.
  Decisions 1+2 invert all four. Its header calls the shape "the point, not an
  implementation detail" and cites the !1440 revert. It is the codified form of
  the lesson this ADR proposes to un-learn: state the rationale and name its
  successor.
- **Decide status under N instances.** Merged identity comes from the last
  source, so N-1 objects get no status, and a library-shard-only change stamps an
  unchanged `observedGeneration` — every kstatus/Flux/`kubectl wait` gate passes
  instantly against a pre-change status. Write per-member status, or aggregate
  generations as `CompositeVersion` already does for resourceVersions.
- **Add deletion semantics for a set member.** `configloader.record` never
  removes, so a deleted member is re-merged from the held copy while
  `waitForInitialConfig` blocks forever on the missing name — livenessProbe kills
  the running pod ~2 min later, then the startupProbe → CrashLoopBackOff.
- **Successor designs for `scripts/test-chart-upgrade.sh` phases 3 and 4**
  (positive control; staging a broken fleet), and a statement of why a
  hard-failing `pre-upgrade` hook does not regress the broken-fleet recovery
  guarantee.
- **Fix `scripts/test-helm-defaults.sh`** — four breakages, one of which fails
  *vacuously*: the `kubectl replace --dry-run=server` admission assertion becomes
  a no-op that always passes once the rule is deleted.
- **The playground renders only the first shard.** `parseConfigSpec` returns
  `list.Items[0].Spec`, and the docs tell operators to paste
  `kubectl get haproxytemplateconfig -o yaml` verbatim. `getting-started.md` also
  tells them to `kubectl edit … haptic-config` — under sharding "the" config no
  longer exists.
- **Budget the CI and dev-loop cost.** `test-chart-upgrade.sh` alone performs 13
  install/upgrade invocations, each of which would run the full suite (~17–20 s
  plus Job scheduling), on top of every e2e shard and every
  `start-dev-env.sh restart`.
- **Measure the release Secret with every library subcharted**, before deleting
  the stripper (see 6a). Order: subchart, confirm, then delete.
- **Fix two stale claims** that this ADR would accidentally make true again:
  `CHANGELOG.md:34` and `.gitlab-ci.yml:1979-1982` both still say the chart
  renders one config per template library. They are false on `main` today.
