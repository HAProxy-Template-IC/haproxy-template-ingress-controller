"""Track failures from completed default-branch verification runs."""

import hashlib
import json
import os
from pathlib import Path
import re
import urllib.error

from .github import GitHub, jobs_for_run


WORKFLOWS = {
    ".github/workflows/ci.yml": {"push", "workflow_dispatch"},
    ".github/workflows/nightly.yml": {"schedule", "workflow_dispatch"},
}
FAILURES = {"failure", "timed_out", "startup_failure", "action_required"}


def eligible(run, repository, branch):
    return (run["repository"]["full_name"] == repository
            and (run.get("head_repository") or {}).get("full_name") == repository
            and run["head_branch"] == branch
            and run["event"] in WORKFLOWS.get(run["path"], set())
            and run["status"] == "completed"
            and run["conclusion"] not in {"cancelled", "skipped"})


def family(name):
    name = name.removeprefix("full / ")
    for pattern in (r"^(unit-rest|helm|gateway)-\d+$", r"^(e2e-hp[\d.]+|e2e-minimum-kubernetes)-\d+$"):
        match = re.fullmatch(pattern, name)
        if match:
            return match[1]
    return name


def reports(repository, run, jobs):
    buckets = {"failure": [], "canary": []}
    for job in jobs:
        failed = job["conclusion"] in FAILURES or any(
            step["conclusion"] in FAILURES for step in job.get("steps", []))
        if not failed:
            continue
        canary = run["path"] == ".github/workflows/nightly.yml" and job["name"] == "nightly-gwapi-canary"
        buckets["canary" if canary else "failure"].append(job)
    if not any(buckets.values()) and run["conclusion"] in FAILURES:
        buckets["failure"].append({"name": "workflow infrastructure", "id": None})
    url = f"https://github.com/{repository}/actions/runs/{run['id']}/attempts/{run['run_attempt']}"
    result = []
    for kind, failures in buckets.items():
        if not failures:
            continue
        names = sorted({family(job["name"]) for job in failures})
        fingerprint = hashlib.sha256(("|".join(names) + "|" + kind).encode()).hexdigest()[:16]
        marker = f"<!-- github-ci-fingerprint:{fingerprint} -->"
        occurrence = f"<!-- github-ci-occurrence:{run['id']}:{run['run_attempt']}:{kind} -->"
        links = []
        for job in sorted(failures, key=lambda item: item["name"]):
            job_url = f"https://github.com/{repository}/actions/runs/{run['id']}/job/{job['id']}" if job["id"] else url
            name = job["name"].replace("`", "'").replace("\n", " ")
            links.append(f"- [`{name}`]({job_url})")
        title = "Canary failure" if kind == "canary" else "CI failure on main"
        body = (f"Run [{run['id']}, attempt {run['run_attempt']}]({url}) "
                f"failed on `{run['head_sha']}` (source: `{run['event']}`).\n\n"
                + "\n".join(links)
                + "\n\nThe run page links logs and diagnostic artifacts. Investigate the cause before rerunning."
                + ("\n\nThis upstream canary is allowed to fail." if kind == "canary" else "")
                + f"\n\n{occurrence}")
        result.append({"title": f"{title}: {', '.join(names)}"[:256], "body": body,
                       "marker": marker, "occurrence": occurrence})
    return result


def publish(api, repository, selected):
    root = f"repos/{repository}"
    for name, color in (("needs-triage", "ededed"), ("ci-failure", "d73a4a")):
        try:
            api.get(f"{root}/labels/{name}")
        except urllib.error.HTTPError as error:
            if error.code != 404:
                raise
            api.post(f"{root}/labels", {"name": name, "color": color})
    issues = [issue for issue in api.all(f"{root}/issues", None, state="open", labels="ci-failure")
              if "pull_request" not in issue]
    for report in selected:
        existing = next((issue for issue in issues if report["marker"] in (issue.get("body") or "")), None)
        if existing:
            comments = api.all(f"{root}/issues/{existing['number']}/comments", None)
            if (report["occurrence"] in (existing.get("body") or "")
                    or any(report["occurrence"] in (comment.get("body") or "") for comment in comments)):
                print(f"Failure occurrence already recorded on issue #{existing['number']}")
                continue
            api.post(f"{root}/issues/{existing['number']}/comments", {"body": "Recurred.\n\n" + report["body"]})
            print(f"Failure recurrence recorded on issue #{existing['number']}")
        else:
            created = api.post(f"{root}/issues", {"title": report["title"],
                               "body": report["body"] + "\n\n" + report["marker"],
                               "labels": ["needs-triage", "ci-failure"]})
            issues.append(created)
            print(f"Failure recorded on issue #{created['number']}")


def report_run(api, env, event):
    repository = env["GITHUB_REPOSITORY"]
    if env["GITHUB_EVENT_NAME"] != "workflow_run" or event["repository"]["full_name"] != repository:
        raise ValueError("Failure reporting requires a completed workflow event from this repository")
    expected = event["workflow_run"]
    branch = event["repository"]["default_branch"]
    if not eligible(expected, repository, branch):
        return []
    run = api.get(f"repos/{repository}/actions/runs/{expected['id']}/attempts/{expected['run_attempt']}")
    keys = ("id", "run_attempt", "head_sha", "workflow_id", "path", "head_branch", "event", "status", "conclusion")
    if not eligible(run, repository, branch) or any(run[key] != expected[key] for key in keys):
        raise ValueError("Failure evidence does not match the completed workflow event")
    selected = reports(repository, run, jobs_for_run(api, repository, run))
    if selected:
        publish(api, repository, selected)
    return selected


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    selected = report_run(GitHub(os.environ["GH_TOKEN"]), os.environ, event)
    print(f"Reported {len(selected)} failure groups")


if __name__ == "__main__":
    main()
