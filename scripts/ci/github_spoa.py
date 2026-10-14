"""Bind immutable SPOA preparation to a GitHub release branch job."""

import json
import os
from pathlib import Path
import re
import subprocess

from .github import GitHub, jobs_for_run


def preparation_environment(run, jobs, *, repository, sha, ref, attempt, version):
    if (not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+", repository) or
            not re.fullmatch(r"[a-f0-9]{40}", sha) or
            not re.fullmatch(r"\d+\.\d+\.\d+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?", version)):
        raise ValueError("SPOA preparation requires a repository, source commit and release version")
    branch = "release/v" + version
    if (run["repository"]["full_name"] != repository or run["head_repository"]["full_name"] != repository or
            run["head_sha"] != sha or run["head_branch"] != branch or ref != "refs/heads/" + branch or
            run["run_attempt"] != attempt or run["event"] != "workflow_dispatch" or
            run["path"] != ".github/workflows/prepare-spoa-release.yml"):
        raise ValueError("SPOA preparation requires the matching release branch workflow and source")
    selected = [job for job in jobs if job["name"] == "prepare-spoa-release"]
    if len(selected) != 1:
        raise ValueError("SPOA preparation must identify exactly one publication job")
    job = selected[0]
    if (job["run_id"] != run["id"] or job["run_attempt"] != attempt or job["head_sha"] != sha or
            job["status"] != "in_progress" or not isinstance(job["id"], int) or job["id"] <= 0):
        raise ValueError("SPOA preparation job does not belong to this running attempt")
    return {"CI_REGISTRY_IMAGE": "ghcr.io/" + repository.split("/")[0].lower() + "/haptic",
            "CI_COMMIT_BRANCH": branch, "CI_JOB_ID": str(job["id"]),
            "CI_PROJECT_URL": "https://github.com/" + repository, "CI_COMMIT_SHA": sha}


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    api = GitHub(os.environ["GH_TOKEN"])
    run = api.get(f"repos/{repository}/actions/runs/{int(os.environ['GITHUB_RUN_ID'])}")
    sha = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if sha != os.environ["GITHUB_SHA"]:
        raise ValueError("SPOA preparation checkout differs from the workflow source")
    environment = preparation_environment(
        run, jobs_for_run(api, repository, run), repository=repository, sha=sha,
        ref=os.environ["GITHUB_REF"], attempt=int(os.environ["GITHUB_RUN_ATTEMPT"]),
        version=Path("VERSION").read_text().strip())
    with open(os.environ["GITHUB_ENV"], "a", encoding="utf-8") as output:
        for key, value in environment.items():
            output.write(f"{key}={value}\n")
    evidence = Path(".publication/spoa-preparation-source.json")
    evidence.parent.mkdir(exist_ok=True)
    evidence.write_text(json.dumps({"run_id": run["id"], "attempt": run["run_attempt"],
                                    "environment": environment}, indent=2) + "\n")


if __name__ == "__main__":
    main()
