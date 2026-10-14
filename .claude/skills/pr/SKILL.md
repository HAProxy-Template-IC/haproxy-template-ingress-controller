---
name: pr
description: Create or update a GitHub pull request for the current branch
disable-model-invocation: true
---

## Current state

- Branch: !`git branch --show-current`
- Working tree: !`git status --short`
- Upstream: !`git rev-parse --abbrev-ref @{upstream} 2>/dev/null || echo "(no upstream)"`
- Commits ahead of main: !`git log --oneline main..HEAD 2>/dev/null || echo "(none)"`
- Diff stat: !`git diff main...HEAD --stat 2>/dev/null || echo "(no diff)"`
- Existing PR: !`gh pr list --repo haproxy-haptic/HAPTIC --head "$(git branch --show-current)" 2>/dev/null | head -5 || echo "(none)"`

## Task

Create or update a GitHub pull request for the current branch.

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

### 3. Push and create/update the pull request

Branch pushes don't run CI; the pull request does.

**New pull request:**
```sh
git push -u origin HEAD
gh pr create --repo haproxy-haptic/HAPTIC --base main --title "<title>" --body-file <file>
```

**Existing pull request** — just push. Update title/description with `gh pr edit` if scope has changed.

### 4. Title and description

Analyze the full diff `main..HEAD` (all commits, not just the latest).

- **Title**: <70 chars, conventional commit style (e.g. `fix: correct route ordering`)
- **Description**: brief summary of the problem and notable changes. No sections for small changes. No emojis. No mention of Claude or AI.

### 5. After pushing

Monitor the CI run until completion or a reasonable checkpoint.
