# ADR-0029: Cold renders run to completion

## Status

Accepted 2026-10-05 (issue #285). Amends the cold-cache handoff of
[ADR-0023](0023-incremental-render-graph.md) and the readiness verdict of
[ADR-0028](0028-probes-judge-the-serving-iteration.md).

## Context

A replica has no render graph after a restart or a configuration change. Its
first reconcile render is cold: it executes every component instance, so its
cost grows with the whole watched-resource set. Every render was bounded by
the 30 s render timeout. At 3,000 Ingresses with `GOMEMLIMIT` below the cold
render's live heap, the garbage collector stretched that render past 30 s. The
render then published nothing, and every retry was cold again and timed out
the same way. Admission renders were cold too and exceeded the webhook budget,
so the replica denied every routing write until someone raised its memory.

A bounded retry of the same work can't converge once that work costs more
than the bound.

## Decision

1. **The reconcile render that builds the first graph has no render timeout.**
   Its context still ends with the leadership term (Coordinator) or the
   iteration (Warmer). The service logs a warning once the render passes the
   timeout, so a template that never returns stays visible. Warm renders keep
   the timeout. The playground, the benchmark and validation tests keep it on
   every render, because nothing else would stop a template that never returns.
2. **One cold reconcile render at a time.** Until the first graph is published,
   a reconcile render takes a per-service gate and holds it until its commit
   has published the graph or its transaction is dropped. The cold commit waits
   for the graph build instead of `maxColdCacheBuildWait`. A reconcile render
   that waited for the gate finds the graph and renders warm; it never
   supersedes the build.
3. **Admission waits for that render instead of starting its own cold one.** A
   request whose budget ends first is denied with what is wrong (the first full
   render is still running), the effect (no validation until it finishes) and
   the fix (retry; raise memory and CPU if it persists).
4. **`/readyz` reports the first graph.** Its `render-graph` entry fails while
   the serving iteration has no published graph and a Ready sibling reports
   one, or no sibling check can rule one out. When no sibling can validate,
   the entry stays healthy with a note that siblings read as "no graph".

## Alternatives considered

**Resumable cold build.** A cancelled wave publishes nothing by design
(ADR-0023): its results are authenticated against the inputs pinned when it
started. Keeping partial results across attempts means re-authenticating them
against inputs that may have moved, and holds their memory across the retry,
which is the resource that was short. The total work is the same as finishing
the first attempt.

**Bounding cold-render cost.** Cold cost scales with the resource set. Any
bound that fits one cluster fails the next larger one in the same way.

**A larger timeout.** Moves the cliff without removing it.

## RULE #2 delta

The admission decision function doesn't change: same rules, same
`failurePolicy: Fail`, same pipeline. A replica without a graph still renders
or denies; it never admits unchecked. What changes is which replicas receive
requests, and why a cold replica denies:

| state | before | after |
|---|---|---|
| first render running, a sibling has a graph | Ready, every request denied after its budget | NotReady; the sibling validates |
| first render running, no sibling can validate | Ready, denied with `context deadline exceeded` | Ready, denied with the cause and the fix |
| first render longer than the render timeout | never commits; denies forever | commits; validates |

## Consequences

- A rolling upgrade waits for each new replica's first full render before it
  counts as Ready.
- A cold render that never returns holds the gate until leadership changes or
  the pod stops. The warning log and the `render-graph` entry report it.
