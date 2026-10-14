# GitHub verification scheduling

Full verification includes the three Gateway API compatibility versions, churn,
scale, and upstream canary alongside the PR suites. Each keeps a fresh cluster,
its original test selection, and its deadline. Only the upstream canary is
allowed to fail; missing, skipped, or cancelled execution fails
the verification receipt. The receipt records its actual conclusion and binds
all jobs to the same source and run attempt.

`.github/job-duration-weights.json` records runner durations and their source run
IDs. `make schedule-github-verification` places long suites first and fills all
60 execution lanes with shorter suites. Coverage aggregation takes over the lane
of its longest measured producer and still waits for all seven inputs. This keeps
one runner from sitting idle until coverage is ready. Coverage producers start
immediately, alongside the command and general unit-test cache producers. Command shards run sequentially
on fresh runners, and support tests follow the general unit tests, so consumers
can reuse their predecessors' compiled packages. Compatibility jobs follow native jobs with the
same compiler cache. Every suite still executes, including when its predecessor
fails and no cache is available. The aggregate gate waits for every suite. These
weights predict scheduling; a completed first-attempt workflow proves lead time.
After the initial placement, the scheduler swaps groups between lanes to shorten
the longest chains while keeping cache consumers together and coverage producers
at the start.

Coverage aggregation took 67 seconds in run `38483303507`. Including it in the
schedule reduces the longest predicted lane from 20m00s to 19m46s. Every scheduled job
belongs to one of the 60 sequential lanes; the additional coverage dependencies
cannot increase concurrency. This estimate excludes runner provisioning and the
final verification gate, and is not a new hosted speed result.

Run `38479981671` passed all 103 jobs in 24m07s, including all six added suites.
It exposed late compiler-cache producers and coverage dependencies. Its native
cache started cold; the earlier 20m53s comparison run used a warm native cache.
Duration weights retain the earlier warm native timings, use the combined run
for other jobs, and use the separate cache-transfer proof for command consumers.
Run `38483303507` finished its final required check in 21m57s. Coverage completed early, but the renderer
unit suite became the last job: it started at 10m53s and took 10m50s, compared with
8m37s in the preceding run. Both restored their compiler cache. The workflow also
failed the HAProxy 3.0 rolling-restart test, so it is not a successful timing proof.

Renderer verification now uses three disjoint shards, selected from the compiled
inventory with tests, examples, and fuzz seeds included. All retain `-race` and
`-count=1`. Their weights use top-level JUnit durations from both combined runs;
subtest times are not counted twice. They restore the same renderer compiler
cache, with one writer. Job weights take the slower comparable native measurement
from the earlier warm-cache baseline and the second combined run.

Focused run `38487659388` passed all three renderer shards in 5m19s overall,
with individual jobs taking 3m59s, 5m15s, and 5m17s. Every compiler cache missed.
The retained inventories cover all 941 compiled entry points exactly once;
their JUnit reports contain the same 1,694 cases and five existing skips as the
unsplit run. The scheduler uses these measured job durations. This validates
the split, not the complete workflow's lead time.

The failing rollout returned two 503s, at `23:30:18.015` and `23:30:18.216` UTC.
The controller received the replacement's Ready endpoint at `23:30:17.809` and
applied the updated plan to both HAProxy pods by `23:30:18.294`. The old backend
handled its last request at `23:30:17.818`, then stopped on SIGTERM. HAProxy's
active worker marked it down while the replacement was still in maintenance;
both failed requests had no available server. No reload fired in that interval.
Packet captures show the second 503 on a fresh TCP connection to the same pod.
This is a propagation gap, distinct from ADR-0013's leaving-worker residual.
Its cause remains under investigation; the traffic assertion and fixture are
unchanged. The daily schedule cannot be retired on this evidence.

Native suites share the architecture- and Go-version-specific compiler and
module cache. The dependency key covers `go.mod`, `go.sum`, and the PGO profile.
Makefile and Dockerfile edits no longer discard reusable Go packages: Go's build
cache checks source, compiler options, and compiler identity itself. Tests still
execute, and the native image is built from the current checkout with source
identity checks. No test waits for another runner to publish an image.

Compatibility jobs change cluster-wide CRDs. Churn changes diagnostic settings;
scale measures latency and memory in isolation. Combining these clusters would
change the checks. Shared compilation avoids work without sharing their state.

Prose-only changes keep the existing eight common checks. Weekly verification
calls the combined workflow once; it does not also launch separate copies of
the integrated suites. The daily schedule remains during migration until the
combined hosted run proves no increase over the prior PR lead time.

`make check-chart-upgrade-baselines` compares the registry's released versions
with the CI upgrade jobs. CI must cover each baseline on current and minimum
Kubernetes, use distinct node images for those variants, and require every
upgrade job in its final verification gate. Missing, duplicate, or unexpected
baselines fail the check.
