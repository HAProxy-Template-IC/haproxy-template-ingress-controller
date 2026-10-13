# GitHub verification scheduling

Full verification includes the three Gateway API compatibility versions, churn,
scale, and upstream canary alongside the PR suites. Each keeps a fresh cluster,
its original test selection, and its deadline. Only the upstream canary retains
GitLab's allowed-failure policy; missing, skipped, or cancelled execution fails
the verification receipt. The receipt records its actual conclusion and binds
all jobs to the same source and run attempt.

`.github/job-duration-weights.json` records runner durations and their source run
IDs. `make schedule-github-verification` places long suites first and fills 59
execution lanes with shorter suites, reserving one of the current plan's 60
slots for coverage aggregation. Coverage producers get priority for their final
aggregation step. The aggregate gate still waits for every suite. These weights
predict scheduling; a completed first-attempt workflow proves lead time.

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
