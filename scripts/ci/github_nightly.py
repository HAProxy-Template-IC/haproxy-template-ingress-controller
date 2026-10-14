"""Select the daily upstream canary or the weekly full verification."""

import json
import os
from pathlib import Path

from .github import GitHub


def plan(api, env, event):
    repository = env["GITHUB_REPOSITORY"]
    if event["repository"]["full_name"] != repository:
        raise ValueError("Nightly trigger belongs to a different repository")
    run = api.get(f"repos/{repository}/actions/runs/{env['GITHUB_RUN_ID']}")
    if (run["id"] != int(env["GITHUB_RUN_ID"]) or run["head_sha"] != env["GITHUB_SHA"]
            or run["event"] != env["GITHUB_EVENT_NAME"]):
        raise ValueError("Nightly run does not match its trigger revision")
    if run["event"] == "schedule":
        if run["head_branch"] != event["repository"]["default_branch"]:
            raise ValueError("Scheduled nightly checks run only on the default branch")
        mode = {"30 2 * * 1-6": "nightly", "30 3 * * 0": "weekly"}.get(event["schedule"])
    elif run["event"] == "workflow_dispatch":
        mode = event.get("inputs", {}).get("mode", "nightly")
    else:
        raise ValueError("Nightly checks run on a schedule or manual dispatch")
    if mode not in {"nightly", "weekly"}:
        raise ValueError("Unknown nightly schedule or mode")
    return {"mode": mode}


def main():
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    result = plan(GitHub(os.environ["GH_TOKEN"]), os.environ, event)
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        for key, value in result.items():
            output.write(f"{key}={value}\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
