# Issue tracker

Issues and pull requests live on GitHub in `haproxy-haptic/HAPTIC`. Issues that
predate the move were copied from GitLab; each copy links its original. Use an
issue's full URL when identifying historical work: GitLab issue numbers and
GitHub issue or PR numbers are different namespaces.

Use the [`gh`](https://cli.github.com/) CLI with the explicit repository:

```bash
haptic_repo=haproxy-haptic/HAPTIC
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

Create a GitHub issue in `haproxy-haptic/HAPTIC`.

## When a skill says "fetch the relevant ticket"

Read the ticket at its original URL. Do not reinterpret a GitLab number as a
GitHub number; follow the recorded migration link when an issue has moved.
