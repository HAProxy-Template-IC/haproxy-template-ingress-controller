# GitHub verification scheduling

`.github/workflows/ci-speed-proof.yml` holds the verification matrix. No job is
allowed to fail; a missing, skipped, or cancelled required job fails the
verification receipt, which binds all jobs to the same source and run attempt.
Prose-only changes run eight common checks.

Pull requests and merge-queue groups use the `pre-merge` profile: the full matrix
except the jobs in `pre_merge_skipped_jobs` of `.github/ci-verification.json`.
Pushes to `main` and `maint/*`, manual dispatches, and the weekly run use `full`,
which adds those jobs. Release tagging and website publication accept only a
`full` receipt for the exact commit.

`.github/job-duration-weights.json` records runner durations and their source run
IDs. `make schedule-github-verification` places long suites first and fills all
60 execution lanes with shorter suites. Coverage aggregation takes over the lane
of its longest measured producer and still waits for all seven inputs. Coverage
producers start immediately, alongside the command and general unit-test cache
producers. Command shards run sequentially on fresh runners, and support tests
follow the general unit tests, so consumers reuse their predecessors' compiled
packages. Every suite still executes when its predecessor fails and no cache is
available. After the initial placement, the scheduler swaps groups between lanes
to shorten the longest chains while keeping cache consumers together and
coverage producers at the start. Weights predict scheduling; only a completed
first-attempt workflow measures lead time.

Renderer verification uses three disjoint shards, selected from the compiled
inventory with tests, examples, and fuzz seeds included. All retain `-race` and
`-count=1`, and restore the same renderer compiler cache with one writer.

Native suites share the architecture- and Go-version-specific compiler and
module cache. The dependency key covers `go.mod`, `go.sum`, and the PGO profile;
Go's build cache checks source, compiler options, and compiler identity itself.
The native image is built from the current checkout with source identity checks.

## Post-merge suites

The Gateway API compatibility versions, churn, and scale run only in `full`.
Each keeps a fresh cluster: compatibility jobs change cluster-wide CRDs, churn
changes diagnostic settings, and scale measures latency and memory in isolation.
They stay out of `pre-merge` because they lengthen it: with them, runs
`38534640184` and `38555150518` finished their last suite after 22m12s and
21m42s, against 20m53s without them in run `38432655131`. Dispatch `ci.yml` on a
branch to run them before merging.

`.github/workflows/nightly.yml` runs the upstream Gateway API canary daily and
the `full` matrix every Sunday. The canary may fail; its failures open
canary-labelled issues.

`make check-chart-upgrade-baselines` compares the registry's released versions
with the CI upgrade jobs. CI must cover each baseline on current and minimum
Kubernetes, use distinct node images for those variants, and require every
upgrade job in its final verification gate. Missing, duplicate, or unexpected
baselines fail the check.
