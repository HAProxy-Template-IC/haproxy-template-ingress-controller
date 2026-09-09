# Agent handoff: working on HAPTIC

For an agent starting a session on this repository. It covers how the work is judged, what has to be green before a push, how performance is measured here, and the failure patterns this project has already paid for. It is not a substitute for `CLAUDE.md` — read that first; this is the operating knowledge that sits around it.

Last verified against `main` at 6034b25f9 (2026-09-09).

## Orientation

HAPTIC is an event-driven Kubernetes operator that renders HAProxy configuration from templates and deploys it to a fleet of HAProxy pods without restarting them. One replica holds a lease and is the leader: it renders, gates the render through a real `haproxy -c`, and applies the result to every pod through a small agent that owns that pod's file tree.

Everything measurable splits into two halves, and every performance discussion names one of them:

- **Render** — watched Kubernetes resources to a configuration, on the leader, incremental.
- **Deploy** — that configuration onto the fleet, per pod, in parallel.

Three numbered rules in `CLAUDE.md` outrank convenience and are quoted by number in reviews: **RULE #1** resource-agnostic Go, **RULE #2** validation is never traded away, **RULE #3** the default is no comment. Know what they say before you write code.

**Worktrees.** Sessions usually run in a git worktree under `.claude/worktrees/<name>`. Run everything from there. Never redirect git at another checkout with `-C` or a path argument: other worktrees hold other people's work in progress, and the harness refuses those commands. The git stash stack is shared across every worktree, so never use bare `git stash` / `git stash pop`; make a temporary commit instead.

## How the work is judged

- **Measurements decide.** A performance claim is a number from a benchmark or a scale leg, taken before and after, on the same machine. "Should be faster" is not a result.
- **Root cause, never retry.** A red pipeline is investigated until the mechanism is named. Re-running a job to see whether it passes this time is not an answer and is not accepted as one.
- **Never stop while CI is red.** That includes `main` after your own merge.
- **Every analysis ends with a verdict**: what to build or not build, what to close, and the condition that would reopen it.
- **Finish the whole task.** If part of it is blocked, do the rest and say plainly what was left and why.
- **Merge your own merge requests** once the pipeline is green and the review is answered.

## Hard gates

Never do these, whatever the pressure:

- Weaken validation to make an operation succeed — narrowing a webhook selector, deleting a `validationTest`, relaxing a CRD's `required:`, widening an assertion or a timeout until a failure stops reproducing. A gate that blocks a legitimate operation is reporting a design defect upstream of itself.
- Suppress a linter with an inline directive, or add a global exclusion. Fix the code, or add a localized exclusion under `exclusions.rules`. A hook blocks the inline form outright.
- Run `git commit --no-verify`.
- Edit a `.go` file in the tree a scale run was launched from. The harness recomputes `scripts/source-hash.sh` mid-run and aborts when it moves.
- Write Go that knows about a specific watched Kubernetes resource (RULE #1). The controller's own identity — its credentials Secret, the pod fleet it manages, its own CRDs — is the documented exception.

## Before you push

All three, locally, green, on the tree you are about to push:

```bash
PKG='./pkg/dataplane/agent/... -run TestSomething' make test   # while iterating
make test                                                       # whole suite, -race
make lint
kind delete cluster --name haptic-e2e
HAPTIC_E2E_EXTRA_SET='controller.resources.limits.cpu=4' \
  HAPROXY_VERSION=3.4 KEEP_CLUSTER=false make test-e2e
```

Notes that cost time if you learn them the hard way:

- `go test` is blocked by a hook. Use `make test`, with `PKG` to narrow.
- The e2e run needs a **fresh** cluster and **CI's CPU shape**. With more cores than CI has, a gate that takes 44 seconds there finishes in 11 and hides the bug you were meant to catch.
- Go builds need `GOTMPDIR` on real disk; `/tmp` is a tmpfs and `-race` fills it.
- A failing early gate masks everything behind it. Read the exit code, never gate through `tail` or `grep`.

## Measuring a performance change

**Unit benchmarks** answer "is this code path cheaper". Run the before and after **back to back on the same machine**: a quiet-moment 24.0 ms reads 25.9 ms an hour later. Anchor the regex (`-bench='^BenchmarkX$/^routes=300$'`), because an unanchored `routes=300` also matches `routes=3000`.

**Scale legs** answer "does the fleet feel it". `scripts/bench-gateway-api.sh` ramps a kind cluster to 5,000 HTTPRoutes and then holds a steady state of roughly one change per second.

- Launch it from a `git archive` snapshot of the commit under test, never from a working tree you might touch.
- **Leave the machine alone during the ramp.** A build or a test suite running alongside pushes the ramp past its deadline and invalidates the leg. One 20-second leader CPU profile at the ramp point (around 4,300 routes) is acceptable and is where every profile in the record was taken.
- Compare legs, never a leg against a memory: p50 and p90 bucketed by routes present, the ramp duration, and the steady state after the ramp. Ramp numbers are inflated by node load; the steady state is the honest per-change cost.

**What a leg tells you.** The `deployment.completed` log line carries the phase split of the slowest pod:

| Field | Meaning |
| --- | --- |
| `deploy_prepare_ms` | the deployment before any pod was contacted |
| `pod_state_ms` | reading the pod's agent state |
| `pod_diff_ms` | composing the decision for that pod |
| `pod_send_ms` | the apply round trips, upload included |
| `pod_upload_bytes` | file content actually sent |
| `agent_stage_ms`, `agent_admit_ms`, `agent_write_ms`, `agent_ops_ms`, `agent_finish_ms`, `agent_total_ms` | the agent's own split, stamped on every apply result |
| `pod_total_ms` | that pod's whole apply |
| `deploy_settle_ms` | the deployment after the last pod answered |

Profile CPU on the leader by port-forwarding its debug port and reading `/debug/pprof/profile?seconds=20`. The leader is the holder of the `haptic` lease.

## When a profile cannot explain the time

A CPU profile shows work, not waiting. If a phase split has a gap the profile cannot account for, take an **execution trace**:

```bash
curl -s "http://127.0.0.1:8080/debug/pprof/trace?seconds=6" > trace.out
go tool trace -pprof=sync  trace.out > sync.prof     # blocking on synchronisation
go tool trace -pprof=sched trace.out > sched.prof    # scheduler latency
go tool pprof -peek 'YourFunction$' sync.prof
```

`-peek` down the stack names the mutex. This is how a 13 ms per-deployment gap turned out to be every provenance check of a render occurrence queuing behind the status applier's walk of every projected patch, under a mutex they shared. No CPU profile would have shown it.

## Baseline as of 2026-09-09

What a healthy scale leg looks like, so a regression is recognisable. Per change, p50 of the slowest pod, at 4,500–5,000 routes present during the ramp:

| Half | p50 | p90 |
| ---: | ---: | ---: |
| Render | 63 ms | 107 ms |
| Deploy | 22 ms | 41 ms |

Deploy in the steady state after the ramp is 11 ms. The ramp to 5,000 routes takes about 15:20 against a 20-minute deadline. Upload per apply is under 1 KB; a route change travels as the bytes that differ from what the pod already holds.

Leader CPU per 20 seconds of wall time at 4,500 routes, from `pprof` samples:

| Consumer | Seconds |
| --- | ---: |
| GC mark workers | 21.5 |
| Render | 14.2 |
| Status applier | 3.1 |
| Plan blob encode | 2.3 |
| Whole apply path | 0.7 |
| Total sampled | 48.9 |

## Review and merge

1. Branch off `origin/main`, one MR per change.
2. Open it with `glab mr create --source-branch ... --target-branch main --remove-source-branch --yes`. Put the measured table in the description; a perf MR without numbers is incomplete.
3. Comment `Gitar review` on the MR. The review is manual and does not start on its own.
4. Answer every finding **in its thread** — an in-thread reply resolves it. Roughly five replies per twenty minutes before the API throttles.
5. Merge when the pipeline is green and the review is answered, then **watch `main`'s pipeline for your merge commit**.

CI failures on `main` are auto-filed as issues labelled `ci-failure` + `needs-triage`, each linking the failed jobs. Triage them by pulling each job's trace, naming the mechanism, and mapping it to the commit that fixed it; close with that explanation and a reopen-if condition, or move to `needs-info` when only the next occurrence can supply the missing evidence.

## Failure patterns this repository has already paid for

Recognising one of these saves a day:

- **State learned from an event races the work that needs it.** A leader-only component that caches something from a re-published event will, sooner or later, get the work before the event. Pass inputs the iteration is built for at construction.
- **A cache whose sweep is "what this transaction touched" is emptied by a transaction that legitimately touches nothing.** Check every cache's eviction rule against the paths that skip it.
- **A snapshot compared against a live object races the two writes that produce it.** Re-read the parent on every poll rather than comparing against a captured copy.
- **A cheap check and an expensive walk sharing a mutex is a convoy.** A sealed, read-only structure wants a read-write lock.
- **Speed exposes assertions that encoded "the controller is slow."** When a change makes something faster, expect tests that quietly depended on the old timing to fail; they are the bug, not the change.
- **An unretried network step in CI fails eventually.** Module downloads and binary fetches get a bounded retry with a comment naming the failure it survives.
- **A pod-level e2e probe that reports only a status code cannot tell "refused" from "killed."** Report the underlying exit status too.

## Where state lives

- `.remember/remember.md` — the running handoff: what is in flight, what is measured, what is next. Update it at the end of a session and whenever a long-running job is left going.
- The agent's memory directory — durable, one lesson per file, indexed. Facts that would otherwise be re-learned belong there, not in this file.
- Session scratchpads hold the analysis scripts (log bucketing, phase splits, ramp timing, profile helpers). They are **session-local**: describe what a script does in the handoff rather than pointing at a path a later session cannot open.

## Open right now

- **#209** (`ci-failure`, `needs-info`) — a `TestVectorSidecar` availability probe reported HAProxy unavailable during a Vector child restart. The evidence says the probe was signalled rather than answered, and nothing known signals it. Diagnostics that name the cause are merged; this waits for the next occurrence.
- **#142** — the Renovate dependency dashboard. A bot issue, not a task.

The next performance lever, in order of expected return: garbage collection is now the largest single consumer of leader CPU, so start with an allocation profile at the ramp point rather than a code change. After that, the status applier materialises every projected patch on every deployment; a status delta driven by the render cycle is the known fix. The render's root-template loop is the largest single render item but needs a chart and engine change, so it deserves its own issue before any code.
