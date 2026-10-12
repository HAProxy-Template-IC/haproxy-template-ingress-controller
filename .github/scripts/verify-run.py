"""Bind all required GitHub job results to one source revision and run attempt."""

import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
from urllib.request import Request, urlopen


def verify(run, jobs, required, *, repository, sha, attempt):
    if run["repository"]["full_name"] != repository or run["head_sha"] != sha:
        raise ValueError("Workflow belongs to a different repository or revision")
    if run["run_attempt"] != attempt:
        raise ValueError("Workflow belongs to a different attempt")
    if not required or len(required) != len(set(required)):
        raise ValueError("Required job inventory is empty or duplicated")
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


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    sha = os.environ["GITHUB_SHA"]
    attempt = int(os.environ["GITHUB_RUN_ATTEMPT"])
    run_id = int(os.environ["GITHUB_RUN_ID"])
    base = f"repos/{repository}/actions/runs/{run_id}"
    run = api(f"{base}/attempts/{attempt}")
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
    checked = verify(run, jobs, manifest["required_jobs"], repository=repository, sha=sha, attempt=attempt)
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if head != sha:
        raise ValueError("Checkout does not match the verified revision")
    tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], text=True).strip()
    now = datetime.datetime.now(datetime.timezone.utc)
    started = datetime.datetime.fromisoformat(run["run_started_at"].replace("Z", "+00:00"))
    receipt = {
        "version": 1, "repository": repository, "run_id": run_id, "run_attempt": attempt,
        "sha": sha, "tree": tree, "jobs": checked,
        "manifest_sha256": hashlib.sha256(manifest_path.read_bytes()).hexdigest(),
        "verified_at": now.isoformat(), "seconds_to_verification": (now - started).total_seconds(),
    }
    Path("ci-verification.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(f"Verified {len(checked)} required jobs for {tree}")


if __name__ == "__main__":
    main()
