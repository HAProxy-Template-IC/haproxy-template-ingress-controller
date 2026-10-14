# Issue tracker

GitLab remains authoritative until the GitHub migration is explicitly completed.
The presence of GitHub workflows or migration branches does not establish cutover.
Use the issue's full URL when identifying historical work: GitLab issue numbers
and GitHub issue or PR numbers are different namespaces.

## GitLab before cutover

Issues and PRDs live as GitLab issues. Use the
[`glab`](https://gitlab.com/gitlab-org/cli) CLI for these operations.

- **Create an issue**: `glab issue create --title "..." --description "..."`. Use a heredoc for multi-line descriptions. Pass `--description -` to open an editor.
- **Read an issue**: `glab issue view <number> --comments`. Use `-F json` for machine-readable output.
- **List issues**: `glab issue list --state opened -F json` with appropriate `--label` filters. Note that GitLab uses `opened` (not `open`) for the state value.
- **Comment on an issue**: `glab issue note <number> --message "..."`. GitLab calls comments "notes".
- **Apply / remove labels**: `glab issue update <number> --label "..."` / `--unlabel "..."`. Multiple labels can be comma-separated or by repeating the flag.
- **Close**: `glab issue close <number>`. `glab issue close` does not accept a closing comment, so post the explanation first with `glab issue note <number> --message "..."`, then close.
- **Merge requests**: GitLab calls PRs "merge requests". Use `glab mr create`, `glab mr view`, `glab mr note`, etc. — the same shape as `gh pr ...` with `mr` in place of `pr` and `note`/`--message` in place of `comment`/`--body`.

Infer the repo from `git remote -v` — `glab` does this automatically when run inside a clone.

## GitHub after cutover

Use the [`gh`](https://cli.github.com/) CLI with the explicit repository:

```bash
haptic_repo=HAProxy-Template-IC/haproxy-template-ingress-controller
```

| Operation | Command |
| --- | --- |
| Read | `gh issue view "$issue_number" --repo "$haptic_repo" --comments` |
| List open issues | `gh issue list --repo "$haptic_repo" --state open --json number,title,labels,url` |
| Filter by triage label | `gh issue list --repo "$haptic_repo" --label ready-for-agent` |
| Create from a prepared file | `gh issue create --repo "$haptic_repo" --title "$issue_title" --body-file "$body_file"` |
| Comment from a prepared file | `gh issue comment "$issue_number" --repo "$haptic_repo" --body-file "$body_file"` |
| Add a label | `gh issue edit "$issue_number" --repo "$haptic_repo" --add-label ready-for-agent` |
| Remove a label | `gh issue edit "$issue_number" --repo "$haptic_repo" --remove-label needs-triage` |
| Close after posting the resolution | `gh issue close "$issue_number" --repo "$haptic_repo" --reason completed` |
| Read a PR and its conversation | `gh pr view "$pr_number" --repo "$haptic_repo" --comments` |
| Inspect checks | `gh pr checks "$pr_number" --repo "$haptic_repo"` |

Set the issue or PR number from its GitHub URL. Prepare multiline bodies in a
file and pass `--body-file`; do not put them into a shell command. The five
[triage roles](triage-labels.md) keep their existing labels.

PR comments are not the complete review inventory. Inspect review threads,
review summaries, and checks before queueing; follow [CI verification](ci.md).

## When a skill says "publish to the issue tracker"

Create an issue in the authoritative tracker for the current migration stage.

## When a skill says "fetch the relevant ticket"

Read the ticket at its original URL. Do not reinterpret a GitLab number as a
GitHub number; follow the recorded migration link when an issue has moved.
