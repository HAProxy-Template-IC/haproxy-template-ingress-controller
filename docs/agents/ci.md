# CI and merge verification

Every merge to `main` or a maintenance branch goes through GitHub's merge queue,
which runs the canonical `CI` workflow on the exact merge result. A local test
result, a review comment, and a skipped run cannot replace required CI. These
instructions apply to every agent and future session.

## Before pushing

1. Fetch the current target branch and use a dedicated worktree.
2. Finish the implementation, changelog, local validation, and known review fixes.
3. Run `make lint audit` before `make test`; both can modify or consume `vendor`,
   so never run them in parallel (`make -j` included).
4. For chart changes, also run `./scripts/test-templates.sh`.

## While iterating

- Batch fixes before pushing. Every pull request runs the complete matrix,
  including the former nightly suites; prose-only changes run the common checks.
- Never add `[skip ci]` or otherwise skip verification.
- Diagnose failures before retrying. Rerun a specific job once for a demonstrated
  infrastructure failure. Fix test failures instead of rerunning until they pass.
- Rebase only for a conflict or another concrete requirement. The merge queue
  tests the combined code; routine rebases invalidate CI and review evidence.

## Before queueing

1. Read every Gitar review, thread, and summary for the final source SHA. Fix
   valid findings and resolve threads with evidence. A missing review is a
   blocker, not permission to waive review.
2. Confirm the required `verified` check passed for that SHA.
3. Queue the exact reviewed source:

```bash
gh pr merge "$pr_number" \
  --repo haproxy-haptic/HAPTIC \
  --squash --auto --match-head-commit "$reviewed_sha"
```

Never use `--admin`, disable a required check, or merge outside the queue. The
`CI` workflow runs on `merge_group`; success from a manually dispatched workflow
cannot authorize a merge or publication. The receipt checks the exact source,
the complete required job inventory (`.github/ci-verification.json`), and the
run attempt.

## Completion and budget

Report the PR, source SHA, authoritative run, review state, and whether the
change is merged, published, or deployed. Keep those states separate. Report a
running workflow as running, not verified.

Keep CI within the user's EUR 20 monthly limit. Use standard GitHub-hosted
runners, which are free for the public repository, with short artifact retention
and bounded caches. Do not enable paid larger runners, change spending limits,
or change the plan without authorization. Gross metered usage on the billing page
is not the payable amount; check the net amount before a campaign. Cancel only
superseded work owned by your task.

## Post-merge reuse

Reuse is disabled unless the repository variable `REUSE_MERGE_VERIFICATION` is
exactly `true`. Enable it only after a real merge-queue run, its required jobs,
and the resulting full post-merge run have passed. Record their run IDs, attempts,
commit IDs, and equal trees before changing the variable, then read it back.

Only ordinary pushes to `main` or `maint/*` qualify. The pushed commit's first
parent must match both the previous branch tip and the merge queue's tested base.
The original `CI` run must contain every required job and both successful
aggregate gates. Its tree and policy must match the merged checkout, and its
retained artifact must match GitHub's digest and producing job attempt. Pull
requests, merge groups, manual runs, and scheduled verification keep their
checks.

Discovery examines the latest 100 merge-queue runs created within one day.
Missing, expired, mismatched, or unavailable evidence runs normal CI. A newer
failed run of the same tree cannot be replaced by an older passing run.
Publication independently downloads and revalidates the original jobs and
artifact. A reused receipt cannot itself supply reuse, and reuse does not replace
publication builds or release conformance.

## Scheduled verification

`.github/workflows/nightly.yml` runs Monday through Saturday at 02:30 UTC and on
Sunday at 03:30 UTC. The weekly run calls the complete matrix even when `main` is
unchanged, to detect changes in runners, registries, upstream Gateway API, and
other external dependencies. Schedules never publish or create releases.

Feature E2E jobs use `make test-e2e-profile` and the exact-name lists in
`tests/e2e/profiles/`. Each feature runs once, including its authentication, WAF,
and ordinary-routing interaction checks. Core HAProxy and Kubernetes matrices
enumerate and shard the complete test inventory. `make test-ci-rules` checks
profile coverage and Go test sharding; `make test-github-verification` checks the
workflow inventory, scheduling, and evidence gates.
