---
name: steward
description: Drive a HAPTIC pull request from creation to a mergeable state - open it, watch CI and reviews, and fix what they report
---

# Pull request stewardship

Applies to every pull request you create or are asked to drive. Conventions come
from [CI and merge verification](../../../docs/agents/ci.md); this skill says
when to act.

## Open

- After implementing and verifying a change, open the pull request without
  asking. Verification is the local checks in `docs/agents/ci.md` for the
  touched area; say in the description which checks you could not run.
- Subscribe to the pull request's activity immediately, without asking.

## Watch

Handle every CI result and review until the pull request is merged or closed.

- **CI red**: root-cause and push a fix. Rerun a job once only for a
  demonstrated infrastructure failure. Never skip, disable, or weaken a test
  (RULE #2).
- **Merge conflict**: bring in `main` and resolve it. Don't rebase otherwise.
- **Gitar review**: Gitar must review the final source SHA.
  - If Gitar posts that automatic reviews are paused (monthly limit), or has
    not reviewed the current head after CI has started, comment `Gitar review`
    on the pull request. Once per head SHA.
  - Fix valid findings. Answer each thread with the fix commit or, for an
    invalid finding, the evidence; then resolve it.
- **Human review**: implement small asks; propose larger ones to the user
  first.

## Done

The pull request is ready when the required `verified` check passed and Gitar
reviewed the head SHA with no open findings. Report the SHA, the run, and the
review state to the user. Merging stays with the user unless they ask you to
merge.
