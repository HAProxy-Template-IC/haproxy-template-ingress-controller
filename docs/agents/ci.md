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

- Batch fixes before pushing. Pull requests run the matrix without the
  compatibility, churn, and scale suites, which run after merge and gate releases;
  prose-only changes run the common checks. Dispatch `ci.yml` on your branch when
  a change needs those suites before merging.
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

## Post-merge verification

Every push to `main` or `maint/*` runs the `full` matrix, including the suites
that pull requests and merge groups skip. Merge-queue reuse
(`REUSE_MERGE_VERIFICATION`) can't apply: a merge group's `pre-merge` receipt
never covers a `full` push, so it falls back to normal CI. Leave the variable
unset.

## Scheduled verification

`.github/workflows/nightly.yml` runs the upstream Gateway API canary Monday
through Saturday at 02:30 UTC. On Sunday at 03:30 UTC it also runs the complete
matrix, even when `main` is unchanged, to detect changes in runners, registries,
upstream Gateway API, and other external dependencies. Schedules never publish or
create releases. See [GitHub verification scheduling](github-scheduling.md).

Feature E2E jobs use `make test-e2e-profile` and the exact-name lists in
`tests/e2e/profiles/`. Each feature runs once, including its authentication, WAF,
and ordinary-routing interaction checks. Core HAProxy and Kubernetes matrices
enumerate and shard the complete test inventory. `make test-ci-rules` checks
profile coverage and Go test sharding; `make test-github-verification` checks the
workflow inventory, scheduling, and evidence gates.
