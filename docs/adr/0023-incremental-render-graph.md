# ADR-0023: Incremental rendering uses exact immutable dependencies

## Status

Proposed 2026-08-24. Implementation in progress for issue #187.

## Context

Rendering grows with the complete watched-resource set even when one object
changes. The HTTPRoute benchmark takes about 72 ms at 300 routes, 216 ms at
1,000, and 628 ms at 3,000. Sequential admission therefore repeats an O(N)
render for each CREATE and becomes O(N²) over a batch.

A text cache around a template call is unsafe. Templates can read watched
objects and HTTP content, choose dependencies dynamically, contribute shared
text, transform the resource view, and record Events. Reusing text without the
complete dependency and effect set can miss a required update or retain an
effect after its producer disappears.

Mutable `SharedContext` isn't a cache boundary. Its values can retain aliases,
and execution order can choose the producer. Observing every possible Go heap
mutation isn't a reliable invalidation protocol.

## Decision

Incremental rendering is opt-in for a template snippet. The snippet becomes a
keyed component that runs once for each object in one or more configured
watched-resource sources.

### Component identity and inputs

Each component instance is identified by component name, source alias,
namespace, and name. A static `source` selects one watched-resource alias. A
`bindingsTemplate` can instead produce a JSON object whose keys are source
aliases and whose values become immutable `props`. The binding planner may
select from detached stable render inputs such as `extraContext`, capabilities,
the current plan and files, paths, and runtime settings. Inputs it doesn't emit
aren't component dependencies.

The component receives these explicit values:

- `source`, `item`, immutable `props`, and `renderSubject`;
- a resource accessor that records each exact `List`, indexed `Get`, or
  identity read;
- an equivalent accessor for controller-owned operational resources;
- an HTTP accessor that records the URL, complete options and authentication
  descriptor, accepted content, and exact observation revision; and
- `shared.Unique`, which records an immutable keyed text contribution;
- `shared.Publish`, which records an immutable keyed structured value; and
- `shared.Select`, `shared.SelectValues`, and `shared.Count`, which read
  authenticated publications from declared producer groups.

`requires` retains its existing meaning: it strips a snippet when an optional
watched resource is unavailable. It's neither a dependency declaration nor an
access allowlist.

Bindings and component inputs are detached before execution. The incremental
Scriggo entry point exposes only the deterministic component environment and
approved pure helpers. Stable ambient values reach a component only through its
canonical props. It rejects arbitrary native functions, goroutines, and
mutations of published inputs or tracked store results. A local value can still
be built and mutated while the component runs.

### Exact resource projection

A component can use `mode: resourceProjection` when its only job is to publish
one exactly indexed watched resource. Its binding planner selects a watched
source alias and emits a canonical descriptor containing the store key vector,
publication cell and key, and an optional winner rank. No Scriggo component
program runs.

The renderer authenticates the binding and executes one exact store `Get` in a
normal graph query. Zero matches records the negative observation and publishes
nothing. One match publishes the complete detached raw object. More than one
match fails the render. The query and result retain independent authenticated
roots, so changing the binding, replacing the store, deleting or recreating the
object, or an away-and-back transition invalidates the projection without a
resource-kind special case.

Projection groups and their transitive consumer closure are demand-driven. A
root request evaluates and seals that chain; an unused chain contributes no
cold execution or index. The mode permits only `publishValue`, requires a
binding template, and forbids source enumeration, activation, Scriggo root
fusion, and `consumes` declarations on the projection component. Malformed or
non-canonical descriptors and incomplete provenance fail closed.

### Dependency graph

The persistent graph stores exact inputs, component queries, and derived-view
projection queries. Every query execution builds a fresh dependency frame from
the reads that actually occurred. A successful execution replaces the previous
frame; dependencies from a branch that's no longer taken are removed.

An input change dirties only queries that observed that input. A query result
advances its changed revision only when its canonical bytes change, so a
consumer doesn't rerun when an upstream query recomputes the same value.

Object deletion retires its query and effects. Dynamic binding removal uses the
same retirement path. Query and input retirement preserves nodes still needed
by another dependency and removes them when the last dependency disappears.

### Watched-resource, controller-resource, and HTTP stores

Watched resources, controller-owned resources, and HTTP content use the same
correctness contract:

- a stable store identity;
- an immutable value-and-revision snapshot;
- exact present and negative observations;
- a bounded semantic change journal; and
- final verification that every observation the render recorded is exact
  against the snapshot it pinned.

A watched-resource journal entry invalidates only previously observed list,
index-prefix, or identity inputs that the changed object can affect. Store
replacement, an incomplete journal, or an unidentified change discards reuse
and executes the whole graph against a newly pinned exact snapshot. A store
without the immutable snapshot, exact journal, and atomic commit-fence protocol
makes a live render fail before any component executes.

An on-demand watched store publishes each ref, revision, and warm body through
one persistent immutable root. Pinning loads that root without copying the warm
cache, while a later body read remains lazy and verifies the pinned informer
generation, resource version, identity, and index keys.

Admission rebases its fixed overlay on the commit-fenced current root. An exact
journal limits the final membership proof to identities that changed while the
render ran; a source replacement or journal gap rejects the transaction.

An HTTP input is keyed by URL and the complete source descriptor, including
options, headers, and credentials. Accepted bytes and source-authority changes
are observable. A 304 response, identical bytes, access time, refresh metadata,
and rejected pending content aren't. The renderer uses exact descriptor indexes
to avoid scanning unrelated HTTP inputs. Replaying an already-enrolled URL uses
the transaction's keyed observation rather than rebuilding and scanning the
complete HTTP snapshot set.

Initial HTTP content remains a render-local candidate until the existing
validation transaction accepts it. A component that observes a candidate or a
non-cacheable fetch makes the render non-cacheable. Accepted and negative reads
carry verification tokens, including protection against store replacement,
eviction, journal loss, and away-and-back transitions.

### Derived resource stage

The watched store remains immutable. A component that declares the
`deriveResource` effect can own the transformed view for its source alias. Each
source has at most one active owner.

Before any root template runs, the renderer evaluates every new or dirty owner
instance in canonical order. Each owner reads the raw resource snapshot plus
only its own private transformation chain. The completed transformations are
then exposed through an exact-identity resolver, independent of root-template
or group call order.

The shared derived view is frozen before root rendering. A later legacy
`deriveResource` call fails the render, so it can't introduce order-dependent
state or affect cached consumers. Admission uses the same stage over its
hypothetical resource overlay, but never publishes graph or derived state.

### Activation signatures

`whenAnyPathExists` is component semantics, not an optimization hint. Its paths
are presence JSONPaths validated with the configuration. `[*]` selects any
array element; filters remain unsupported. When none exists on the
post-derivation `item`, the component has no text, publications, or effects.
A component that owns `deriveResource` can't declare activation because its
derived item doesn't exist until that owner runs.

One activation-signature query per source identity evaluates every currently
bound gated component after derivation and returns the canonical active
component names. It reads each relevant binding and the exact source item. For
a source with an active derivation owner, it also reads that owner and its
projection; without an owner, it uses the raw item directly. Governance can
therefore add a trigger to activate a component or remove it to deactivate the
component without mutating the watched store.

An unrelated item or props change may execute the signature query, but an
unchanged signature doesn't dirty any component query. Inactive pairs have no
component query, result, or effects. A false-to-true transition creates the
component query; a true-to-false transition retires its query and complete
result. Binding additions, changes, and removals use the same transitions.
Failed renders and admission overlays publish none of these changes.

### Cold source transactions

The dependency graph still selects canonical waves and enforces every
governance and shared-publication barrier. Within one worker and one dependency
wave, children with the same exact source identity and projection reuse one
authenticated raw item, render subject, and immutable projection preparation,
including when their props rows differ. A Scriggo virtual machine can span
successive waves for that worker, but no prepared value crosses a barrier
without a new authenticated child context.

Each child fiber switches to its own generation, dependency reader, effect
recorder, HTTP lease, and result-arena slot. Child exit revokes that generation,
so a retained call or context can't read or publish after its boundary. The
renderer installs completed child results in canonical batch order rather than
worker completion order.

Preparation failure, child failure, panic, or cancellation aborts the complete
wave arena and publishes no result. Once source-transaction preparation or
execution starts, the renderer doesn't retry the wave through a different
execution path.

### Output and supported effects

A component returns ordinary text, `shared.Unique` contributions, or structured
publications. Ordinary text and `shared.Unique` can't be mixed. A publication
can accompany `backendPlan`, but not ordinary text or `shared.Unique`.
`shared.Unique(cell, key, value)` and `shared.Publish(cell, key, value)` select
the first owner in canonical component, source, namespace, name, and call
order. Losing owners remain indexed, so deleting or changing the winner exposes
the next owner without executing it again.

Published values are detached through canonical JSON before they enter a
component result. Roots read one cell with `incremental_values(group, cell)`.
An early read may evaluate and refresh a group, but it neither marks the group
rendered nor replays its HTTP references or Events; the later canonical group
call performs that replay once. Each read decodes fresh values and registers
them with the immutable-input guard, so a root can't mutate persistent state.
Publication indexes are scoped by group. Winner calculation includes every
component in that group, including components without `backendPlan`.

A component declares each producer group in `consumes` or `optionalConsumes`
and may read one winner with `shared.Select`, every ordered winner with
`shared.SelectValues`, or the number of unique winning identities in one cell
with `shared.Count`. Reads are authorized only after a complete canonical
producer sequence in the current root or `haproxy.cfg`. `shared.Count` reads an
O(1) persistent count input and invalidates the consumer only when that count
changes; replacing or promoting an owner with the same cell count does not.

The renderer stores group instances in persistent ordered indexes. Replacing
one instance updates only its output chunk, contribution winners, HTTP
observations, and Events. Materializing the requested component still copies
its complete output string. Each committed group index carries a process-local
seal that retains strong references to its exact immutable roots. An unchanged
read authenticates those roots by identity without scanning their leaves or
depending on a probabilistic digest. A changed update authenticates the old
roots, verifies its affected publication paths, and seals the new roots. A root
substitution is rejected even when a diagnostic full audit finds equivalent
content. The in-memory index is rebuilt cold after a process restart.

The declared effects supported inside an incremental component are:

- `deriveResource`, owned by the component's source object; and
- `recordEvent`, stored as a canonical logical Event and replayed into the
  current render's collector; and
- `backendPlan`, which prepares and stores detached `Profile` and `Backend`
  declarations plus logical backend-token references; and
- `publishValue`, which enables immutable keyed structured publications.

`backendPlan` exposes no mutable `PlanRegistry`. Before the first plan component
returns output, the renderer evaluates every plan-bearing group and orders all
active candidates by component, source, namespace, name, and operation index.
The first declaration for each backend identity wins. Only winning backends and
one canonical declaration for each globally referenced profile replay into the
fresh render registry; standalone profiles also replay once. A winning
backend's non-empty profile must resolve to a declaration, including when that
declaration's local backend lost arbitration. Later backend declarations and
their logical tokens produce no output. The cached result therefore carries
neither a registry pointer nor a render nonce. Exact canonical payload bytes,
not their diagnostic digest, decide replacement and arbitration. A winner's
removal promotes the next cached declaration without executing that candidate
again.

`BackendWhenAny(record, text, cell, keys)` links a backend declaration to
publications in the same component result. The backend is eligible only when
that component instance owns at least one current winner for the named keys.
The result must contain every referenced publication. This lets deletion of a
publication winner promote a cached losing publication and its conditional
backend without re-executing unrelated queries.

Other mutable collectors and shared registries aren't present in the component
environment.

### Render transaction and fail-closed boundary

One graph session covers all incremental calls in a render. A group may be
omitted only when it has no value reads and activation proves it has no active
or cached instances. Every other configured group must be called through one
or more complete sequences. Each sequence contains every component in
snippet-name order and stays within one root template. A complete sequence may
repeat in the same or another root to mount cached text again; component bodies
and effects still execute once. Calls from concurrent roots may interleave, so
validation preserves order independently within each root. Partial, reordered,
or sequences split across roots fail instead of publishing a partial cache
transaction.

Selection in a root that has started a producer sequence requires that local
sequence to be complete. A root without local producer calls may use a complete
`haproxy.cfg` sequence because main rendering finishes before auxiliary roots
start. One auxiliary root never authorizes another because their scheduling is
concurrent.

All roots and effects come from one pinned render session. The authoritative
output transaction first verifies its binding plan, its own resource
observations, HTTP observations, and active HTTP leases. It then publishes the
complete render cycle, at the store cursors the session pinned, and the HTTP
ownership needed to observe later changes. A failed, cancelled, panicking, or
conflicting transaction publishes neither one.

The transaction doesn't require the live store to still match those cursors. A
change that lands mid-render sits in the journal after the pinned cursor, so
the next render replays it. Refusing the commit instead withholds the graph on
a busy cluster, where a relevant input has always moved by commit time; every
render then replays a growing delta from a stale cursor until a warm render
costs as much as a cold one. The exception is a transaction accepting fetched
HTTP content: the check that authorised the content ran against this render's
inputs and no later render can revoke the acceptance, so that transaction
refuses inputs that moved.

Required renderer and post-process publications are prepared at the same commit
fence. Fallible publications run before HTTP, graph, or renderer-state
visibility; failure aborts them in reverse. Renderer state retains its
publication lock until the core commit succeeds, so an ACK can't be overwritten
by rollback. Post-process cache publication performs its generation CAS last,
and the remaining typed HTTP, graph, and renderer assignments contain no
fallible callbacks. Exact-cycle candidate publication is optional and runs only
after the authoritative commit; its failure discards that candidate without
invalidating the sealed core cache.

Cold graph construction isn't part of that output transaction's latency. The
successful transaction marks its exact output generation as cache-pending and
hands the still-immutable session to a bounded background builder. Until the
builder authenticates and atomically publishes both the dependency graph and
renderer snapshot, the cache is absent. A render that arrives in that interval
runs cold from its own pinned inputs; it never waits for or reads the partial
builder. Immediate HTTP ownership is not partial cache state: without it, an
HTTP change after the output commit could fail to schedule the successor that
must replace that output.

The builder admits one running and one newest pending generation. A newer
output cancels older work. Final publication verifies the output generation,
the exact renderer-snapshot base, the graph-generation identity, and every
observed input again, then installs the graph and renderer snapshot as one
commit. A stale, failed, cancelled, or panicking builder leaves the cache
absent. Its result can't replace a newer generation. Retirement cancels and
joins all builders before releasing persistent HTTP leases.

The cache-ready signal is authenticated to its render state and generation. It
completes only after deferred exact-cycle publications have run. A malformed,
foreign, stale, or internally inconsistent signal makes the next render fail
closed. Benchmarks therefore report authoritative first-result latency,
cache-ready latency, an immediate successor while the cache is pending, and
steady warm latency separately.

Source-map rendering, the benchmark command, and template validation tests use
a standalone deterministic cold component executor over static fixture inputs.
It publishes no cache and isn't a live-render fallback. Reconciliation and
admission preflight every exposed watched and controller store plus the
configured HTTP store. If any can't provide the exact protocol, the render
returns no output and publishes no component or input effects.

## Performance boundary

The target for component and graph work is:

```text
O(changed inputs + affected dependency fan-out + changed index paths)
```

Persistent ordered trees avoid scanning and decoding every cached component
result after one object changes. `backendPlan` maintains candidate arbitration,
profile requirements, conflicts, selected declarations, and logical output in
authenticated persistent indexes. An unchanged plan attaches its sealed
prepared snapshot without walking declarations. Replacing one component updates
only affected index paths, winners, requirements, conflicts, and output chunks.
Final plan assembly must still walk selected sections, and final config
materialization is at least `O(output bytes)`. Scriggo component execution,
dependency invalidation, and prepared-plan updates remain bounded by the changed
inputs and affected winners. Benchmarks cover 300, 1,000, and 3,000 inputs
for unchanged and one-change renders, on-demand snapshot pinning, and distinct
HTTP replay sets. They report component executions separately from complete
render time so a flat cache path can't hide remaining root work. The public
`RenderResult` still contains flat Go strings. Creating those strings, hashing
output, building the deployment plan, and running HAProxy validation remain
O(total output bytes).

Publication replacement updates `P` calls from the changed instance in
`O(P log N)`. Authenticating an unchanged group index is `O(1)`. Reading a cell
then depends on its winning identities and returned value bytes, not on the
number of losing owners or unrelated component instances. Backend-plan
preparation uses the same seal before reading publication winners. A seal
mismatch may run an `O(N)` audit to identify corruption, but it always rejects
the index; this diagnostic path isn't a cache hit or accepted replacement.

True flat end-to-end time as output grows requires a later chunk-native result,
checksum, validation, and deployment protocol. This decision doesn't describe
contiguous-string work as constant.

## Rejected alternatives

- **Declared dependency fingerprints:** rereading every declared dependency to
  build a fingerprint repeats the work the cache is meant to remove.
- **Using `requires` as an allowlist:** it would change optional-resource
  stripping semantics and still miss dynamic dependencies.
- **A fast path without HTTP or derived state:** it can't express the bundled
  chart and treats equivalent revision-tracked inputs differently.
- **Logging arbitrary Go heap mutations:** aliases can escape through maps,
  slices, interfaces, closures, native calls, reflection, and shared backing
  arrays.
- **Conservative dependency union:** stale dependencies cause unwarranted
  component executions and hide incorrect dynamic tracking.
- **Best-effort reuse after a journal gap:** the missing transition is the
  information needed to prove an entry current.
- **Per-component cold fallback:** combining cached effects with legacy mutable
  state preserves neither execution order nor atomic ownership.

## Consequences

- Incremental components have explicit props and context like UI components,
  with exact dynamic read tracking and atomic effect ownership.
- The source `item` is one object-valued prop, so any semantic item change
  executes an active component; inactive predicates avoid executing its body.
- A watched, controller-owned, or HTTP input change can't be missed when its
  store satisfies the snapshot protocol. If a live input can't supply the
  proof, rendering fails before component execution or publication.
- Changes that preserve an observed input or query value don't execute its
  consumers.
- Governance transformations no longer mutate the watched store or depend on
  template call order.
- Legacy roots that don't derive resources remain compatible. When incremental
  snippets are configured, every derivation producer must migrate to the one
  declared incremental owner for its source.
- Component and graph work scales with changed dependencies. Legacy root loops
  and flat output, planning, and validation still scale with the work they
  perform; a chunk-native downstream pipeline remains future work.
- Every replica holds a graph. A follower renders each trigger through the
  same authoritative transaction and discards the output, so a leadership
  change starts warm; the price is one render's CPU and one graph's memory per
  replica, and every replica fetching its own HTTP inputs.

## References

- [Salsa red-green algorithm](https://github.com/salsa-rs/salsa/blob/master/book/src/reference/algorithm.md)
- [React `memo`](https://react.dev/reference/react/memo)
- [Issue #187](https://gitlab.com/haproxy-haptic/haptic/-/work_items/187)
