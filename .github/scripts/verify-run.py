"""Bind all required GitHub job results to one source revision and run attempt."""

import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.request import Request, urlopen


def verify(run, jobs, required, *, repository, sha, attempt, prefix=""):
    if run["repository"]["full_name"] != repository or run["head_sha"] != sha:
        raise ValueError("Workflow belongs to a different repository or revision")
    if run["run_attempt"] != attempt:
        raise ValueError("Workflow belongs to a different attempt")
    if not required or len(required) != len(set(required)):
        raise ValueError("Required job inventory is empty or duplicated")
    if prefix and not prefix.endswith(" / "):
        raise ValueError("Reusable workflow job prefix must include its separator")
    required = [prefix + name for name in required]
    selected = {}
    for job in jobs:
        if job["run_id"] != run["id"] or job["head_sha"] != sha or job["run_attempt"] != attempt:
            raise ValueError("Job belongs to a different workflow or revision")
        if job["name"] in selected:
            raise ValueError("Job inventory contains a duplicate name")
        selected[job["name"]] = job
    failures = []
    for name in required:
        job = selected.get(name)
        if job is None:
            failures.append(f"{name}: missing")
        elif job["status"] != "completed" or job["conclusion"] != "success":
            failures.append(f"{name}: {job['status']}/{job['conclusion']}")
        elif not job.get("started_at") or not job.get("completed_at"):
            failures.append(f"{name}: missing execution timestamps")
    if failures:
        raise ValueError("Required CI verification failed: " + "; ".join(failures))
    return [{"name": name, "id": selected[name]["id"]} for name in sorted(required)]


def api(path):
    request = Request("https://api.github.com/" + path, headers={
        "Authorization": "Bearer " + os.environ["GH_TOKEN"],
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
    })
    with urlopen(request, timeout=30) as response:
        return json.load(response)


def revision_identity(event_name, event, *, repository, tested_sha, parents, is_ancestor=None):
    if event["repository"]["full_name"] != repository:
        raise ValueError("Trigger belongs to a different repository")
    if event_name == "merge_group":
        group = event["merge_group"]
        branch = group["base_ref"].removeprefix("refs/heads/")
        if (event["action"] != "checks_requested" or group["head_sha"] != tested_sha or
                not re.fullmatch(r"[a-f0-9]{40}", group["base_sha"]) or
                not re.fullmatch(r"[a-f0-9]{40}", tested_sha) or group["base_sha"] == tested_sha):
            raise ValueError("Checkout is not the requested merge group")
        if (group["base_ref"] != "refs/heads/" + branch or
                not (branch == "main" or branch.startswith("maint/")) or
                not group["head_ref"].startswith("refs/heads/gh-readonly-queue/" + branch + "/")):
            raise ValueError("Merge group targets an unexpected branch")
        if is_ancestor is None or not is_ancestor(group["base_sha"], tested_sha):
            raise ValueError("Merge group does not contain its declared target revision")
        return {"api_sha": tested_sha, "source_sha": tested_sha, "source_repository": repository,
                "base_sha": group["base_sha"], "base_branch": branch, "merge_group_ref": group["head_ref"]}
    if event_name == "pull_request":
        request = event["pull_request"]
        if request["base"]["repo"]["full_name"] != repository:
            raise ValueError("Pull request targets a different repository")
        head = request["head"]["sha"]
        base = request["base"]["sha"]
        if parents != [base, head] or tested_sha in {base, head}:
            raise ValueError("Checkout is not the pull request merge of the declared head and base")
        return {"api_sha": head, "source_sha": head, "base_sha": base,
                "source_repository": request["head"]["repo"]["full_name"],
                "base_branch": request["base"]["ref"], "pull_request": request["number"]}
    if event_name not in {"push", "schedule", "workflow_dispatch"}:
        raise ValueError("Unsupported verification trigger")
    if event_name == "push" and event["after"] != tested_sha:
        raise ValueError("Checkout is not the pushed revision")
    return {"api_sha": tested_sha, "source_sha": tested_sha, "source_repository": repository}


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    sha = os.environ["GITHUB_SHA"]
    attempt = int(os.environ["GITHUB_RUN_ATTEMPT"])
    run_id = int(os.environ["GITHUB_RUN_ID"])
    base = f"repos/{repository}/actions/runs/{run_id}"
    run = api(f"{base}/attempts/{attempt}")
    event_name = os.environ["GITHUB_EVENT_NAME"]
    if run["event"] != event_name:
        raise ValueError("Workflow belongs to a different trigger")
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    parents = subprocess.check_output(["git", "show", "-s", "--format=%P", "HEAD"], text=True).strip().split()
    identity = revision_identity(event_name, event, repository=repository, tested_sha=sha, parents=parents,
                                 is_ancestor=lambda base, head: subprocess.run(
                                     ["git", "merge-base", "--is-ancestor", base, head], check=False).returncode == 0)
    jobs = []
    page = 1
    while True:
        response = api(f"{base}/attempts/{attempt}/jobs?per_page=100&page={page}")
        jobs.extend(response["jobs"])
        if len(jobs) >= response["total_count"]:
            break
        if not response["jobs"]:
            raise ValueError("Incomplete workflow job inventory")
        page += 1
    manifest_path = Path(".github/ci-verification.json")
    manifest = json.loads(manifest_path.read_text())
    prefix = os.environ.get("CI_JOB_PREFIX", "")
    checked = verify(run, jobs, manifest["required_jobs"], repository=repository,
                     sha=identity["api_sha"], attempt=attempt, prefix=prefix)
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if head != sha:
        raise ValueError("Checkout does not match the verified revision")
    tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], text=True).strip()
    now = datetime.datetime.now(datetime.timezone.utc)
    started = datetime.datetime.fromisoformat(run["run_started_at"].replace("Z", "+00:00"))
    receipt = {
        "version": 2, "repository": repository, "run_id": run_id, "run_attempt": attempt,
        "sha": sha, "tree": tree, "jobs": checked,
        "job_prefix": prefix,
        "event": event_name, **identity,
        "manifest_sha256": hashlib.sha256(manifest_path.read_bytes()).hexdigest(),
        "verified_at": now.isoformat(), "seconds_to_verification": (now - started).total_seconds(),
    }
    Path("ci-verification.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(f"Verified {len(checked)} required jobs for {tree}")


if __name__ == "__main__":
    main()
