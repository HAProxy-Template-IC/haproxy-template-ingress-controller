"""Record GitHub's pull request revision identities without exposing the event."""

import json
import os
from pathlib import Path
import subprocess


def api(path):
    return json.loads(subprocess.check_output(["gh", "api", path], text=True))


event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
request = event["pull_request"]
base = f"repos/{os.environ['GITHUB_REPOSITORY']}/actions/runs/{os.environ['GITHUB_RUN_ID']}"
run = api(base)
jobs = api(base + "/jobs")
result = {
    "github_sha": os.environ["GITHUB_SHA"],
    "github_ref": os.environ["GITHUB_REF"],
    "head_sha": request["head"]["sha"],
    "base_sha": request["base"]["sha"],
    "merge_commit_sha": request["merge_commit_sha"],
    "api_run_head_sha": run["head_sha"],
    "api_jobs": [{"id": job["id"], "head_sha": job["head_sha"]} for job in jobs["jobs"]],
    "checkout": subprocess.check_output(["git", "show", "-s", "--format=%H %T %P", "HEAD"], text=True).strip(),
}
Path("pr-revision-identity.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps(result, indent=2))
