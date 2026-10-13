"""Select changed-main nightly runs without repeating unchanged revisions."""

import itertools
import json
import os
from pathlib import Path

from .github import GitHub, jobs_for_run


def required(api, repository, current, *, branch, force=False):
    if current["head_branch"] != branch or current["event"] not in {"schedule", "workflow_dispatch"}:
        raise ValueError("Nightly checks require the default branch schedule or manual dispatch")
    if force:
        return True
    path = f"repos/{repository}/actions/workflows/{current['workflow_id']}/runs"
    for run in itertools.islice(api.all(path, "workflow_runs", branch=branch), 60):
        if run["id"] >= current["id"] or run["head_branch"] != branch:
            continue
        if run["event"] not in {"schedule", "workflow_dispatch"} or run["conclusion"] in {"cancelled", "skipped"}:
            continue
        suites = {job["name"]: job for job in jobs_for_run(api, repository, run)}
        executed = {name for name, job in suites.items() if job["conclusion"] not in {"cancelled", "skipped"}}
        if {"nightly-scale", "nightly-gateway-churn"} <= executed:
            return run["head_sha"] != current["head_sha"]
    return True


def plan(api, env, event):
    repository = env["GITHUB_REPOSITORY"]
    if event["repository"]["full_name"] != repository:
        raise ValueError("Nightly trigger belongs to a different repository")
    run = api.get(f"repos/{repository}/actions/runs/{env['GITHUB_RUN_ID']}")
    if (run["id"] != int(env["GITHUB_RUN_ID"]) or run["head_sha"] != env["GITHUB_SHA"]
            or run["event"] != env["GITHUB_EVENT_NAME"]):
        raise ValueError("Nightly run does not match its trigger revision")
    if run["event"] == "schedule":
        schedules = {"30 2 * * 1-6": "nightly", "30 3 * * 0": "weekly"}
        mode = schedules.get(event["schedule"])
    else:
        mode = event.get("inputs", {}).get("mode", "nightly")
    if mode not in {"nightly", "weekly", "scale", "canary"}:
        raise ValueError("Unknown nightly schedule or requested suite")
    selected = required(api, repository, run, branch=event["repository"]["default_branch"],
                        force=mode == "weekly" or run["event"] == "workflow_dispatch")
    return {"run": str(selected).lower(), "mode": mode}


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    result = plan(GitHub(os.environ["GH_TOKEN"]), os.environ, event)
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        for key, value in result.items():
            output.write(f"{key}={value}\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
