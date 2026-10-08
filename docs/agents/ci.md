# CI spending and merge verification

Every merge to `main` or a maintenance branch goes through a successful merge
train. A local test result, a review comment, and a skipped pipeline cannot
replace required CI. These instructions apply to every agent and future session.

## Before pushing

1. Fetch the current target branch and use a dedicated worktree.
2. Finish the implementation, changelog, local validation, and known review fixes.
3. Run `make lint audit` before `make test`; both can modify or consume `vendor`.
4. For chart changes, also run `./scripts/test-templates.sh`.
5. Estimate the candidate's compute minutes from recent comparable jobs and run
   the budget report. Include a train run, publication work, and downstream jobs.

For a candidate estimated at 600 compute minutes:

```bash
make ci-budget CI_CANDIDATE_MINUTES=600
```

The report reads live namespace usage, purchased minutes, recent job durations,
and active pipelines across the namespace, including child pipelines. Active work
reserves its entire projected cost to cover billing lag. Unknown jobs reserve the
project's timeout. The default reserves are 600 minutes each for release and
recovery; raise them when recent measurements exceed those amounts. The report
does not reserve funds or start a pipeline. Recheck it immediately before starting
expensive work, since other sessions can spend the same allowance.

HAPTIC's public projects receive GitLab's Open Source billing factor of 0.5 compute
minutes per job minute. Pipeline wall-clock time is not compute usage. The report
fails if it cannot determine usage or a billing factor; missing data is not zero.

## While iterating

- Batch fixes before pushing. Ordinary MR pipelines provide early feedback, with
  Go unit tests selected by Go inputs. Every code train runs the full unit suite
  and complete required verification before merging, including CI-only changes.
- Never add `[skip ci]`, `[ci skip]`, or a skip push option to bypass verification.
- Keep the existing cancellation of superseded interruptible pipelines. Cancel
  only superseded work owned by this task, never another session's active work.
- Diagnose failures before retrying. Retry a specific ordinary-pipeline job once
  for a demonstrated infrastructure failure. Fix test failures instead of retrying
  until they pass. A failed train must be fixed and queued again.
- A second full run needs a stated cause, updated estimate, and available budget.
  Ask for additional budget only when existing authorization does not cover it.
- Rebase only for a conflict or another concrete requirement. The train tests the
  combined code; routine rebases invalidate CI and review evidence.

## Before entering the train

1. Read every Gitar discussion and summary for the final source revision.
2. Fix valid findings and resolve discussions with evidence.
3. Inspect a fresh Gitar review after each push; an older summary does not cover
   new changes. Record the source SHA and review note in the MR evidence. Core
   provides review comments, so this is an agent workflow requirement; GitLab
   cannot enforce a native Gitar approval with the current plan.
4. Confirm the MR pipeline passed and the source revision is unchanged.
5. Recheck the budget and use GitLab auto-merge to enter the train.

Use `auto_merge=true` and the expected source SHA with the merge API. Never request
an immediate merge or bypass the train. Finish review before queueing: later
discussion threads do not automatically remove an MR already on a train.

The project permits one train pipeline at a time. Final verification checks the
required job inventory, successful non-optional results, and pipeline/source
identity. Missing, skipped, canceled, or failed required jobs
block the merge. The receipt records the tested tree, target revision, source
revision, policy and build-input hashes, and job IDs.

## Completion and budget limits

Report the MR, source SHA, authoritative pipeline, review state, and whether the
change is merged, published, or deployed. Keep those states separate. Report a
running pipeline as running, not verified.

If there is insufficient budget, leave the MR unmerged and state the missing
verification and estimated cost. Do not weaken a check, mark skipped CI successful,
or claim local validation is hosted CI. Publication must retain the complete
existing gates until equivalent train coverage and provenance checks are verified.

## Enable post-merge reuse

`REUSE_TRAIN_VERIFICATION` defaults to `false`. First verify a real full train,
its required job inventory, the merged tree, and the successful post-merge
pipeline. Then set the protected project CI variable `REUSE_TRAIN_VERIFICATION`
to `true` and read the setting back.

Ordinary pushes to `main` and `maint/*` then verify the train receipt and build
publication artifacts. Weekly, explicit verification, and release-tag pipelines
retain full verification. Every code train still runs the full matrix. A missing
or mismatched receipt stops publication even when reuse is enabled.

## Scheduled verification

The nightly schedule uses `SCHEDULE_KIND=nightly` at 02:30 UTC Monday through
Saturday on `main`. It runs `nightly-dispatch`, which compares the current `main`
SHA with the most recent
`Nightly checks` pipeline. If they match, no build or test pipeline starts.
Canceled and skipped pipelines do not count. A failed nightly remains failed and
reported; an unchanged commit is not automatically retried. Explicit API or web
runs with `SCHEDULE_KIND=nightly` remain available for an investigated retry.

When the SHA changes, the dispatcher uses its temporary `CI_JOB_TOKEN` to start
`Nightly checks` on `main`. That downstream pipeline runs the old Gateway API
release matrix, churn, scale, and upstream canary jobs, plus their image builds.
Its result and failure report belong to the downstream pipeline linked from the
dispatcher; a successful dispatch does not mean the tests passed.
Dispatcher failures use the existing issue reporter through a small failure-only
job, without building CI images.

A separate Sunday schedule uses `SCHEDULE_KIND=weekly` at 03:30 UTC on `main`.
It runs the complete verification matrix and the focused nightly checks even
without new commits, to detect changes in runners, registries, upstream Gateway
API, and other external dependencies. Both verification schedules exclude
publication and release creation.

Feature E2E jobs use `make test-e2e-profile` and the exact-name lists in
`tests/e2e/profiles/`. Each feature runs once, including its authentication, WAF,
and ordinary-routing interaction checks. Core HAProxy and Kubernetes matrices
still enumerate and shard the complete test inventory. `make test-ci-rules`
checks schedule dependencies, merge-train coverage, and inclusion of profile-gated
tests. The feature runner also rejects names absent from the compiled inventory.
