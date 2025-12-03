# Create or Update MR Command

Create a new pull request or update an existing MR for the current branch.

Usage: `/mr`

## CRITICAL: Commit ALL Modified Files

**IMPORTANT**: When the user runs `/mr`, they expect ALL modified files in the working tree to be committed and pushed, not just a subset. This includes:

- All staged files (`git status` shows files with `M` prefix)
- All unstaged files (modified but not staged)
- All untracked files that are part of the feature (if relevant)

**DO NOT** selectively stage only "your" files or files you think are related to the current feature. The user is explicitly requesting to commit EVERYTHING in the current branch state.

**Before creating/updating MR:**
1. Create a new branch if the current branch is main 
2. Run `git status` to see ALL changes
3. Run `git add .` to stage ALL changes (or `git add -A` for comprehensive staging)
4. Commit with descriptive message covering all changes
5. Push to remote

## What this does

1. Commits and pushes ALL changes in the current branch to the remote repository (see above)
2. Checks if a MR already exists for the current branch
3. If MR exists:
   - Updates the existing MR with any new commits
   - Updates the MR title and description to reflect the full git diff between the current branch and the main branch
4. If no MR exists:
   - Creates a new pull request using Gitlab CLI
   - Auto-generates title and description based on commits

## Requirements

- Current branch must have unpushed commits (for new MRs) or new commits (for updates)
- Gitlab CLI must be authenticated
- Branch must be different from main

## Example

```
/mr
```

This will either create a new MR or update an existing one, ensuring the MR title and description comprehensively reflects the current state of all changes in the branch.
