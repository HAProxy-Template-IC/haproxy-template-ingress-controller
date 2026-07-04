# Design: Gateway API Runtime Version Detection

## Context

Everything version-coupled funnels through a handful of choke points, established by code investigation (2026-07-03):

- The watch GVR is a **literal string-split** of config's `apiVersion` (`pkg/controller/resourcewatcher/watcher.go` `toGVR`/`parseAPIVersion`) — no discovery, no validation. A discovery-backed RESTMapper already runs in-process in three places (`typebootstrap_wiring.go` `resolveKind`, webhook rules, status/resource appliers) but is never consulted for watching.
- An **unserved GVR is not detected**: the dynamic informer's initial LIST fails forever, `WaitForCacheSync` never returns, startup Stage 3 blocks with no timeout, `/healthz` stays 503. A served version **removed mid-run** is fully silent (bulk watchers set no watch-error handler; the informer serves its stale cache).
- **Config hot-reload is a full iteration restart**: every informer, the typed-template bootstrap, schemas, webhook registrations, and components are torn down and rebuilt (`pkg/controller/iteration.go` `handleConfigurationChange`). There is no per-informer hot-restart primitive.
- **Typegen is already version-capable**: schemas are fetched per exact GVK (CRD-first, per-GroupVersion OpenAPI fallback); `--schema-dir` mirrors this offline, deriving served versions from CRD YAMLs.
- **Status patches are already apply-time resolved** (template-supplied apiVersion → RESTMapper at SSA time), but the chart hardcodes `gateway.networking.k8s.io/v1` in all eight status macros.
- The chart's availability gating (`_helm_load` `enable`/`inject`/`unset` on `.Capabilities`) is **frozen at helm render time**; the TCPRoute entry is the only version-adaptive one.
- Upstream ground truth (parsed from every gateway-api release's shipped CRDs, v0.5.0–v1.6.0): no Gateway API CRD has ever used a conversion webhook — co-served versions are schema-identical. But a version *name* is not a fixed schema: the effective schema is (release, channel, version); e.g. HTTPRoute `v1` gained `timeouts` and `rules[].name` in place across releases. BackendTLSPolicy `v1alpha2` (v1.0) is the one true breaking rename (`targetRef`→`targetRefs`, `tls`→`validation`) and was never co-served with its successors.

Serving matrix (standard channel) that the chart's preference lists must encode:

| Kind | Served versions by release |
|---|---|
| GatewayClass / Gateway / HTTPRoute | `v1beta1` since v0.5 (still served in v1.6); `v1` since v1.0; `v1alpha2` v0.5–0.7 only |
| ReferenceGrant | `v1beta1` since v0.6; `v1` since v1.5 |
| GRPCRoute | `v1` since v1.1; experimental `v1alpha2` before |
| BackendTLSPolicy | `v1` since v1.4; experimental `v1alpha3` v1.1–1.3; `v1alpha2` excluded (breaking rename) |
| TLSRoute | `v1` since v1.5; experimental `v1alpha2` v0.5–1.4 (`v1alpha3` added alongside in v1.4, experimental) — schema deltas are validation tightening only (hostnames optional→required, rules maxItems 16→1), field names identical |
| ListenerSet | `v1` since v1.5 (was `XListenerSet` in `x-k8s.io` before — different group and plural) |
| TCPRoute / UDPRoute | `v1` since v1.6; experimental `v1alpha2` before |

The matrix documents **standard-channel** serving; the "experimental … before" notes cover the experimental channel. The chart's preference lists (tasks 6.1) deliberately include the schema-compatible experimental-channel versions — GRPCRoute/TCPRoute `v1alpha2`, TLSRoute `v1alpha3`+`v1alpha2`, BackendTLSPolicy `v1alpha3` — so experimental-channel installs of older releases resolve and activate those features too. Only shapes that are structurally incompatible (BackendTLSPolicy `v1alpha2`) are excluded.

## Goals / Non-Goals

**Goals:**

- No Gateway API release can brick the controller; features degrade per-kind exactly as far as the installed CRDs allow.
- In-place CRD changes (install, upgrade, serving removal) converge at runtime with no helm operation and no pod restart.
- All new Go machinery is resource-agnostic (RULE #1): version lists, optionality, `requires`, CRD self-watch, and render-context metadata work identically for an operator's custom CRD.
- Existing configs keep today's semantics unchanged (backward compatible).

**Non-Goals:**

- Cross-group version fallback (`XListenerSet` in `gateway.networking.x-k8s.io` with a different plural): a two-release experimental relic; not worth candidate machinery that spans group+plural.
- BackendTLSPolicy `v1alpha2` support: incompatible shape, never co-served; deliberately excluded from the preference list.
- UDPRoute support (unchanged: not watched, conformance-skipped).
- Per-informer hot-swap without iteration restart (see Decisions).
- Multi-version *simultaneous* watching of one kind — exactly one served version is selected per resource.

## Decisions

### D1: Resolve versions at iteration start via discovery; preference lists in config

`watchedResources.<name>` accepts `apiVersions: [ordered candidates]`; the first candidate the apiserver serves wins. Resolution uses the discovery/RESTMapper already constructed for the typed-template bootstrap, promoted to run before watcher setup and shared. The resolved version feeds all six literal-consumers (informer GVR, cached-store GVR, typegen fetch, webhook GVK, dry-run mapping, fixture defaulting).

*Alternative rejected:* resolving to "whatever the cluster's preferred version is" without a list — the chart must be able to exclude incompatible shapes (BackendTLSPolicy `v1alpha2`) and order preferences; an explicit list keeps that control in the resource-specific layer.

### D2: Availability gating moves from helm render time to config load time

`optional: true` on a watched resource + `requires: [<resource-name>, ...]` on snippets/tests. At config load (every iteration), unavailable optional resources are dropped and every element requiring them is stripped. Go implements one generic rule and knows nothing about what the elements mean. The chart's `_helm_load` `enable` Capabilities gate and the TCPRoute `inject`/`unset` block are deleted (the values flag remains as user intent).

*Alternative rejected:* extending the helm `.Capabilities` pattern to all kinds — structurally cannot react to in-place CRD changes (render-time evaluation), and multiplies the tpl hairball across nine kinds × their version histories.

*Constraint this imposes on the chart:* snippets that must survive a strip may reach stripped resources only through compile-safe seams — `render "..." default ""`, the `render_glob` extension points, or `shared` read-backs — never a direct typed reference or import. This discipline already exists (TCPRoute status seams, fixed 2026-07-04) and generalizes per-kind.

### D3: CRD watch triggers the existing reload path — no new lifecycle primitive

A `SingleWatcher`-style informer on `apiextensions.k8s.io/CustomResourceDefinitions`, filtered to groups appearing in `watchedResources`, debounces served-version changes into the existing `ConfigChangeCh` iteration restart. The restart re-runs discovery, re-resolves, re-strips, regenerates typed structs, rebuilds informers — all already-tested machinery.

*Alternative rejected:* surgical per-informer hot-swap — a new lifecycle primitive (dynamic watcher add/remove, typed-struct regeneration, snippet recompile mid-flight) with a large race surface, for an event that occurs a few times a year per cluster, while the full reinit is in-process, takes seconds, and HAProxy keeps serving traffic throughout. RULE #1 note: the CRD watch is the controller keeping its own watch set valid (operational-identity exception, like the `haproxy-pods` self-watch) and is generic anyway (driven entirely by config content).

*Scope note:* discovery-based resolution covers aggregated APIs too, but the change trigger only reacts to CRDs. Template inputs are in practice CRDs or core resources; periodic re-resolution can be added later if an aggregated-API input ever matters (YAGNI).

### D4: Fail fast on required-unserved; observability for mid-run removal

A required resource with no served candidate fails the iteration with a named error (surfaced in `/healthz` and logs) instead of the silent unbounded `WaitForCacheSync`. The existing 5s iteration retry plus the CRD watch converge automatically once the CRD appears. Bulk watchers get `SetWatchErrorHandler` (log + timestamp, as `SingleWatcher` already does).

### D5: Resolved version exposed to templates as generic watch-set metadata

`resources.<name>.APIVersion()` returns the resolved apiVersion (same surface for a custom CRD as for HTTPRoute). The eight gateway status macros replace hardcoded literals with it; the status applier already RESTMaps template-supplied versions at apply time, so no Go change is needed there. The helm-created GatewayClass object moves into the chart's runtime-rendered `k8sResources` (same machinery as the per-Gateway Services), making it version-adaptive and install-order-proof.

### D6: Field-generation compatibility via the existing dig-guard discipline

Typed field access compiles against the *live* schema, so fields newer than a kind's oldest resolvable schema generation must be `dig()`-guarded — the pattern already used for experimental-channel fields (`retry`, `sessionPersistence`, CORS). Implementation includes an audit of every typed access against the oldest schema each preference list can resolve to (known candidates: HTTPRoute `timeouts`, `rules[].name`). Old-release CRD bundles under `tests/schemas-ga-*` make this auditable and regression-testable offline: `DirFetcher` derives served versions from the bundle, so the same resolution/stripping code path runs in unit tests with no cluster.

## Risks / Trade-offs

- [Chart strip discipline is manual: a new snippet referencing an optional resource directly compiles fine in the default full-featured render and crash-loops only on degraded clusters] → per-kind helm-unittest strip-invariant cases (the TCPRoute one exists as the model) plus template tests against degraded `--schema-dir` bundles in CI; the loader comment documents the seam rules.
- [Webhook rules drift: the helm-owned `ValidatingWebhookConfiguration` pins apiVersions while Go resolves at runtime] → widen chart webhook rule apiVersions to the full candidate list per resource (rules are arrays; unserved versions in a rule are inert).
- [validationTests fixtures inherit the resolved version, so tests run against whichever schema the cluster serves] → fixtures already omit apiVersion and default from config; test assertions are schema-shape-agnostic (rendered-output patterns). Degraded-profile template tests pin the stripped behavior explicitly.
- [Discovery lag: a just-installed CRD may not be immediately visible to discovery when the reload fires] → the CRD watch debounces, and a failed resolution of an optional resource simply strips the feature for that iteration; the next CRD event (or required-resource retry loop) converges. No worse than today's behavior; strictly better because convergence needs no human.
- [The reinit-on-CRD-change causes a config reload (HAProxy keeps running, but deploys pause for seconds)] → acceptable: CRD changes are rare administrative events; this is the same cost as any config edit today.
- [Old-release support claims can rot as new fields land in the chart] → the CI matrix over representative releases (v1.0, v1.4, v1.5, v1.6) plus offline degraded-bundle template tests fail on regressions; the supported-version statement in docs is generated from the tested set, not hand-maintained optimism.

## Migration Plan

1. Go: config schema additions (`apiVersions`, `optional`, `requires`) with defaults preserving current semantics — deployable independently, no chart change required.
2. Go: resolution + strip + fail-fast + watch-error handler + render-context metadata + CRD watch. Still inert for existing configs (single-version, required, no requires).
3. Chart: gateway library preference lists, `requires` annotations, resolved-version status macros, GatewayClass to `k8sResources`, gate/inject/unset removal, webhook rule widening. Ships as one chart release; rendered output changes but behavior on a v1.6 cluster is identical.
4. Tests/CI: schema bundles, degraded-profile template tests, e2e matrix, upgrade-in-place e2e.
5. Docs: supported-version matrix, upgrade behavior, custom-CRD guidance for the new fields.

Rollback: each phase is independently revertible; phase 2 machinery without phase 3 chart adoption changes nothing observable.

## Open Questions

- Should the resolved-version and availability set be exposed on the introspection endpoint and as metrics (`haptic_watched_resource_available{resource,version}`)? (Cheap, likely yes — decide during implementation.)
- Whether the e2e matrix uses full conformance on old releases (suite version must match CRD version) or targeted smoke tests only (recommended: smoke; full conformance stays latest-only).
