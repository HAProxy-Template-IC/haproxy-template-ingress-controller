# ADR-0018: Typed collection pipelines in chart templates

## Status

Accepted. Phase 1 and the `map`-identifier half of Phase 2 implemented in
`feat(templating): type-preserving collection pipelines`; the parser change
shipped as scriggo !115.

## Context

Chart templates iterate, deduplicate, accumulate and re-emit watched resources
constantly. Across `charts/haptic/charts` (~56k lines) that is ~830 `for … range`
loops, 404 `append` accumulators, 303 `[]any{}` accumulators, 66 hand-rolled
`map[string]bool{}` dedup sets, and 48 `toStringSlice()` conversions that exist
only because the accumulator was declared `[]any`.

The canonical shape, `map-pod-names-500-endpoints` in `charts/base/library.yaml`
before this ADR:

```scriggo
{%- var seenAddr = map[string]bool{} %}
{%- for _, slice := range resources.endpoints.List() %}
{%- for _, ep := range slice.Endpoints %}
{%- var pod = ep.TargetRef.Name %}
{%- if pod != "" %}
{%- for _, addr := range ep.Addresses %}
{%- if !seenAddr[addr] %}
{%- seenAddr[addr] = true %}
{{ addr }} {{ pod }}
{%- end %}{%- end %}{%- end %}{%- end %}{%- end %}
```

Five nesting levels, thirteen whitespace-control markers, and five unlabelled
`end`s that must be paired by counting backwards. The dedup set is hand-rolled
at 66 sites with no shared implementation.

Two existing mechanisms partly address this and neither is enough:

- **`{%% … %%}` multi-line blocks** already exist and `charts/CLAUDE.md` already
  names them preferred for 3+ consecutive statements. They are used 111 times
  against ~830 loops. They fix the marker noise but not the hand-rolled dedup,
  and they keep the reader tracking mutable state.
- **The Jinja-style filter family** (`selectattr`, `sort_by`, `glob_match`) is
  string-keyed. It erases element types, and it fails silently: today
  `selectattr(eps, "targetRef.name", "ne", "")` returns **zero** matches because
  the dotted path is passed to `dig` as a single key. Nothing errors.

That silent-zero is the deciding constraint. RULE #2 forbids trading validation
away, and `charts/CLAUDE.md` already sets the direction — "default to typed
resource access, not `dig()`"; "new `dig()` usage should be questioned at
review". A new collection API built on string paths would move against the
codebase's own migration.

## Measurement

Capability probes against `pkg/templating` and the Scriggo fork
(`v0.0.0-20260805043617`), each run as a rendering test:

| Capability | Result |
|---|---|
| Scriggo closure passed to a native Go func | works |
| `native.AdaptiveFunc` return-type hook | works — already in production for `append`, `shard_slice` |
| Type-preserving `filter`/`map`/`flat_map` over a **typed** slice | works |
| Typed field access on a chain's result, no cast | works |
| **Explicit** closure param types (`func(e EP) bool`) | works |
| `(any, error)` `AdaptiveFunc` Impl | works |
| Trailing-pipe chain across lines inside `{%% %%}` | works |
| Empty slice / nil inner slice through a chain | works |
| `[]T` → `[]any` at a call site | compile error |
| Nested resource types nameable | **not today** — `reflect.StructOf` produces unnamed types |
| Nested types nameable *if typegen registers them* | works, including `type EP = resources.endpoints.Endpoints` aliases |
| `map` as a function name | parse error — parser demands `[` |
| `select` as a function name | parse error — reserved keyword |
| Template-local `type` across the native boundary | runtime error — arrives as `emptyInterfaceProxy` |
| Leading-pipe line continuation | parse error — Go semicolon insertion |
| Newlines inside `{{ }}` | parse error |
| Labelled `break` | compiler panic |
| Parenthesised multi-line pipe chain | compiler panic |

The load-bearing finding: **everything needed for typed, type-preserving
pipelines already works, except that nested resource types have no name.** That
gap is in our own `pkg/k8s/typegen`, not in the fork.

## Decision

Add a closure-based, type-preserving collection pipeline to the template engine,
and make nested resource types nameable so closures can be written with explicit
types.

```scriggo
{% type EP = resources.endpoints.Endpoints %}
{%%
  var lines = resources.endpoints.List() |
    flat_map(func(s *resources.endpoints.T) []EP { return s.Endpoints }) |
    reject(func(e EP) bool { return e.TargetRef.Name == "" }) |
    flat_map(func(e EP) []string { return e.Addresses }) |
    unique()
%%}
```

Closures, not string paths. Every field access is checked at engine compile
time against the generated type, so a typo or a CRD field rename is a chart
load failure, not a silently empty map file.

### Helpers

All are `native.AdaptiveFunc`s. Return type is computed from argument types
alone — no argument *values* are needed, which is why the closure form costs
less machinery than a path form would.

| Helper | Signature | Return type |
|---|---|---|
| `map(s, fn)` | `[]T`, `func(T) U` | `[]U` |
| `filter(s, pred)` | `[]T`, `func(T) bool` | `[]T` |
| `reject(s, pred)` | `[]T`, `func(T) bool` | `[]T` |
| `flat_map(s, fn)` | `[]T`, `func(T) []U` | `[]U` |
| `unique(s)` / `unique_by(s, key)` | `[]T`, `func(T) K` | `[]T` |
| `group_by(s, key)` | `[]T`, `func(T) string` | `map[string][]T` |

Naming is snake_case to sit beside the existing `sort_by` / `first_seen` /
`shard_slice`, not Scriggo's camelCase builtins. `select` is unavailable
(reserved word), hence `filter`/`reject`.

### `sort_by` is overloaded, not replaced

One declaration, dispatching on the runtime type of argument 2:

```scriggo
| sort_by([]string{"$.priority:desc"})        {# existing form, unchanged #}
| sort_by(func(x EP, y EP) int { … })         {# comparator, type-preserving #}
```

The criteria form keeps multi-key `:desc` / `:exists` / `| length` modifiers,
which a comparator expresses poorly. Two side effects, both improvements:
`sort_by` keeps its `(value, error)` return (verified supported), and argument 0
widens from `[]any` to any slice, so `sort_by` starts accepting typed slices —
which it cannot today.

`engine_scriggo.go` re-registers `sort_by` *after* `buildScriggoGlobals` for the
filter-debug override; that override must become the `AdaptiveFunc` too or it
silently shadows the declaration.

### Nested types become nameable

`pkg/k8s/typegen` registers each distinct nested struct as a sibling field on
the per-resource declaration struct, alongside the existing `T`:

```
resources.endpoints.T            → EndpointSlice        (exists today)
resources.endpoints.Endpoints    → the nested Endpoint  (new)
```

This reuses the mechanism that already makes `resources.<n>.T` a type
expression. Template-side `type EP = resources.endpoints.Endpoints` aliases work,
so a snippet names each type once.

### Sequencing

Each phase is independently shippable and independently useful.

**Phase 1 — no fork changes.** Nested-type registration in typegen; the helpers;
the `sort_by` overload. Pipelines work, explicitly typed and type-preserving, on
today's Scriggo.

Elementwise `map` cannot ship on Phase 1 alone: the parser rejects `map(`
outright. Shipping it under a placeholder name and renaming it later would buy
one phase of convenience for a permanent migration, so it waited for the parser
fix instead — which turned out small enough to pull forward into the same
release. `map` is therefore in the shipped set.

**Phase 2 — small fork changes.** `map` as an identifier when not followed by
`[` — **done**, scriggo !115. Remaining: unwrap template-local types at the
native boundary (restores `{% type pair struct{…} %}` intermediates); newlines
inside `{{ }}`; the parenthesised-multi-line-chain compiler panic.

Labelled `break`/`continue` is **not** in this phase after investigation. The
type-checker is deliberately spec-complete — its own conformance tests require
the labelled forms to check successfully — so the gap is purely in the emitter,
which has no lowering for a labelled jump. Turning the internal panic into a
diagnostic at check time breaks those tests; the honest fix is to implement
label→loop mapping in the emitter, which is a feature, not a robustness patch.
Chart code contains 101 `break`s and none of them are labelled, so this is
tracked separately rather than bundled here.

**Phase 3 — the ergonomics payoff.** `=>` lambda syntax with contextual
parameter typing, so `flat_map(s => s.Endpoints)` replaces
`flat_map(func(s *resources.endpoints.T) []EP { return s.Endpoints })`.

Phase 3 is deliberately last and deliberately optional. With explicit types the
chain is verbose enough that it is closer to a wash against a well-written
`{%% %%}` block; `=>` is what makes it decisively better. Putting it last means
the compiler work lands on machinery already proven in production rather than
being a prerequisite for any of it.

### Where pipelines do not apply

The chart contains ~450 `fail()`, 92 `gf[…] =`, 64 `fileRegistry.Register`, 42
`WebhookRejectOrWarn`, 18 `recordEvent`, 10 `statusPatch` calls, and 101
`break`s. Cross-library accumulation and short-circuit have no pipeline form.

Two idioms, each obviously right for its half: **pipelines for the pure
resource→text paths** (the `map-*` snippets, the ~30 map-file builders, every
collect→sort→join), **`{%% %%}` Go blocks for the effectful ones**. Roughly a
quarter to a third of the loops move. Chains longer than ~5 stages should drop
to a block.

## Performance

A pipeline replaces VM-native loop iterations with one **native→VM closure call
per element**. That is not free, and it was initially far worse than expected.

Measured on the pod-names workload (`benchmark_pipeline_test.go`), pipeline
versus the equivalent `{%% %%}` loop:

| Elements | Loop | Pipeline (initial) | Pipeline (after the fix below) |
|---|---|---|---|
| 500 | 0.74 ms / 0.32 MB | 5.95 ms / **32.0 MB** | 1.18 ms / 0.47 MB |
| 5000 | 6.9 ms / 2.98 MB | 62.6 ms / **312 MB** | 11.9 ms / 4.36 MB |

The cost was **not** reflection. Hoisting the argument slice and preallocating
the output changed nothing measurable. The tell was `unique_by`: the *path* form
ran 5.4x faster and allocated 10x less than the identical logic expressed as a
*closure*, because the path form never re-enters the VM.

Root cause: `callable.Value` built a **fresh VM per closure invocation**, and a
VM carries four register banks of `stackSize` entries — ~28 KB, allocated and
discarded on every element. `vmPool` already existed and was already used by the
parallel-render and goroutine paths; this one path simply bypassed it. Fixed in
scriggo !116: 8.9x faster and 329x less garbage at 1000 calls, benefiting every
template that hands a closure to native code — `ComputeIfAbsent` memoisation and
macro callbacks take the same path.

That left a pipeline at ~1.6-1.7x the time of the equivalent loop — a constant
factor rather than an order-of-magnitude cliff, but still a real price for the
compile-time field checking.

**Compile-time lowering removed it.** scriggo !119 adds a `desugarPipelines`
pass that rewrites a chain of literal-closure stages into the loop a human would
have written, fused into one pass with no intermediate slices. The closures stop
crossing the native boundary because there is no native call left:

| Elements | Loop | Pipeline (native) | Pipeline (lowered) |
|---|---|---|---|
| 50 | 73.2 us | 126 us | 76.0 us |
| 500 | 685 us | 1.21 ms | **662 us** |
| 5000 | 6.74 ms | 11.6 ms | **6.63 ms** |

Time parity with the hand-written loop. Allocations stay ~20% above it (the
accumulator plus the hoisted closure values), which is the remaining gap.

Only `map`/`filter`/`reject`/`flat_map` lower, and only when the stage's
function argument is a **literal** closure — Kotlin's `inline fun` rule.
`unique*`, `group_by` and `sort_by` are not loop-shaped and stay native, so a
chain lowers its longest lowerable *prefix* and re-applies the rest.

**This constrains Phase 3.** The pass runs before type checking and therefore
has no type information; it works only because a literal closure's result type
is written in the source, so the accumulator's element type is lifted out of the
AST. An inferred `=>` lambda that drops explicit result types removes that fact
and would force the lowering after type checking. Lowering is worth more than
the arrow.

### Guidance

- **Prefer a path form over a closure** where both exist (`unique_by("host")`
  over `unique_by(func…)`). It is roughly 2x cheaper because it stays on the Go
  side.
- **Keep the hottest per-element work in a `{%% %%}` loop.** `pod-names.map`
  re-renders on every endpoint change and can reach thousands of addresses; its
  inner per-address loop stays a loop, with the pipeline confined to the
  per-EndpointSlice flatten and filter. Pipeline stage count matters less than
  the *element* count flowing through it.
- **Do not reach for parallelism here.** Per-element parallelism is wrong on
  three counts: template closures may have side effects (`fail()`, `gf[…] =`,
  `recordEvent`), rendered output must stay byte-deterministic or it costs a
  spurious reload, and it would multiply peak memory by the worker count to
  solve a problem that was really one allocation. The correct granularity
  already exists one level up — `shard_slice` plus `go render` shards *snippets*
  across goroutines, where the work is independent and the ordering is
  reassembled deterministically.
- **Batching is what the path form already is:** one native loop over `dig`
  instead of N VM round-trips. That is why it is kept alongside the closure form
  rather than deprecated.

## Alternatives considered

### Alternative 1: string-path filters (`flat_map("endpoints")`)

Consistent with Jinja and with today's `selectattr`. Rejected: it is unchecked,
and its silent-failure mode is already demonstrated in this codebase — the
`selectattr` dotted-path bug returns zero matches with no error. It also costs
*more* fork machinery, because `AdaptiveFunc.ReturnType` receives argument types
and not values, so a path form cannot compute its own return type.

Retained only as a possible convenience overload once the closure form ships.

### Alternative 2: `{%% %%}` blocks alone, no pipeline API

The cheapest option, and it is genuinely most of the readability win for the
effectful half — this ADR keeps it for exactly that half. Rejected as the whole
answer: it leaves 66 hand-rolled dedup sets with no shared implementation, and
it keeps the reader tracking mutable state through five nesting levels.

### Alternative 3: fluent method chaining (`items.Filter(…).Map(…)`)

Works today. Rejected: it needs a wrapper type, which erases the element type —
the opposite of the goal — and it abandons the `|` vocabulary the chart already
uses 55+ times.

### Alternative 4: `=>` first

Rejected as a *starting* point, not on merit. It is the best syntax for this —
unambiguous (`=>` appears nowhere in Go, unlike `func(s) {…}` which parses today
as an unnamed parameter of type `s`), and it makes contextual typing simpler to
implement because the arrow is an unconditional "infer my parameters" marker.
Sequenced third so the compiler change is not a prerequisite.

### Alternative 5: leading-pipe line continuation

Rejected. It contradicts Go's semicolon-insertion rule, which puts the operator
at line end, and trailing-pipe already works. No payoff once closures are short.

## Consequences

### Positive

- Field access in collection code is checked at engine compile time. The
  `selectattr` silent-zero class of bug becomes a load-gate failure.
- One dedup implementation replaces 66 hand-rolled `map[string]bool{}` sets.
- `sort_by` accepts typed slices for the first time.
- Nested types become nameable everywhere — macro parameters and type switches
  over nested shapes stop requiring `any` + `dig`.
- Phase 1 carries no fork risk.

### Negative

- Explicit closure types are verbose until Phase 3.
- Two idioms to learn instead of one, with a judgement call about which fits.
- Nested-type registration enlarges the per-resource declaration struct; the
  naming scheme must handle collisions across nested paths deterministically.
- A runtime panic inside stage N of a chain needs a source position that points
  at that stage. The error formatter must be checked against this.

## Do not re-suggest

- **String-path collection filters as the primary API.** See Alternative 1; the
  silent-failure evidence is decisive under RULE #2.
- **`select` as a helper name.** Reserved word; it does not parse.
- **Renaming `map` to `mapped`/`transform` to dodge the parser.** The one-token
  lookahead fix is correct and only accepts programs that do not compile today.
- **Leading-pipe continuation.** See Alternative 5.
- **Deleting `sort_by`'s criteria form** in favour of comparators. It expresses
  multi-key ordering that comparators do not, and every existing call site uses
  it.

## Verification

- Engine unit tests per helper: type preservation, empty input, nil inner slice,
  explicit and inferred parameter types.
- A typegen test asserting nested-type registration and alias resolution for a
  real bundled schema.
- `sort_by` tests covering both call shapes and the debug-override path.
- `./scripts/test-templates.sh` full suite green on every profile after each
  chart snippet is converted.

## Related

- ADR-0010 — typed watched resources; this ADR extends its type surface to
  nested shapes.
- `charts/CLAUDE.md` — "Typed Resource Access", "Multi-line Statement Blocks".
- `pkg/templating/CLAUDE.md` — RULE #1 for engine helpers. Every helper here is
  resource-agnostic: they operate on slices, never on a named kind.
