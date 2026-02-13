---
name: mr
description: Create or update a GitLab merge request for the current branch
disable-model-invocation: true
---

## Current state

- Branch: !`git branch --show-current`
- Working tree: !`git status --short`
- Upstream: !`git rev-parse --abbrev-ref @{upstream} 2>/dev/null || echo "(no upstream)"`
- Commits ahead of main: !`git log --oneline main..HEAD 2>/dev/null || echo "(none)"`
- Diff stat: !`git diff main...HEAD --stat 2>/dev/null || echo "(no diff)"`
- Existing MR: !`glab mr list --source-branch="$(git branch --show-current)" 2>/dev/null | head -5 || echo "(none)"`

## Task

Create or update a GitLab MR for the current branch.

### 1. Commit pending changes

If there are uncommitted changes:
1. If on `main`, create a feature branch first
2. `git add -A` — the user expects ALL changes to be included
3. Commit using the project's conventional commit style

### 2. Rebase onto default branch

Before pushing, rebase onto the default branch (`main`) to avoid merge conflicts:
```sh
git fetch origin main && git rebase origin/main
```
Resolve any conflicts if they arise.

### 3. Push and create/update MR

**New MR** — create atomically with push to avoid duplicate CI pipelines:
```sh
git push -u origin HEAD \
  -o merge_request.create \
  -o merge_request.title="<title>" \
  -o merge_request.target=main
```
If a longer description is needed, push with `-o ci.skip` first, then `glab mr create`.

**Existing MR** — just push. Update title/description with `glab mr update` if scope has changed.

### 4. Title and description

Analyze the full diff `main..HEAD` (all commits, not just the latest).

- **Title**: <70 chars, conventional commit style (e.g. `fix: correct route ordering`)
- **Description**: brief summary of the problem and notable changes. No sections for small changes. No emojis. No mention of Claude or AI.

### 5. After pushing

Monitor the CI pipeline until completion or a reasonable checkpoint.
