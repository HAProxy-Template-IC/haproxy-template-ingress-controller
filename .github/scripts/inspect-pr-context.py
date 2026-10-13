"""Record GitHub's pull request revision identities without exposing the event."""

import importlib.util
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
spec = importlib.util.spec_from_file_location("verify_run", Path(__file__).with_name("verify-run.py"))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)
parents = subprocess.check_output(["git", "show", "-s", "--format=%P", "HEAD"], text=True).strip().split()
identity = verifier.revision_identity(os.environ["GITHUB_EVENT_NAME"], event,
                                     repository=os.environ["GITHUB_REPOSITORY"],
                                     tested_sha=os.environ["GITHUB_SHA"], parents=parents)
checked = verifier.verify(run, jobs["jobs"], [os.environ["EXPECTED_IDENTITY_JOB"]],
                          repository=os.environ["GITHUB_REPOSITORY"], sha=identity["api_sha"],
                          attempt=int(os.environ["GITHUB_RUN_ATTEMPT"]))
result = {
    "github_sha": os.environ["GITHUB_SHA"],
    "github_ref": os.environ["GITHUB_REF"],
    "head_sha": request["head"]["sha"],
    "base_sha": request["base"]["sha"],
    "merge_commit_sha": request["merge_commit_sha"],
    "api_run_head_sha": run["head_sha"],
    "api_jobs": [{"id": job["id"], "name": job["name"], "head_sha": job["head_sha"]} for job in jobs["jobs"]],
    "checkout": subprocess.check_output(["git", "show", "-s", "--format=%H %T %P", "HEAD"], text=True).strip(),
    "revision_identity": identity,
    "verified_experiment_jobs": checked,
}
Path("pr-revision-identity.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps(result, indent=2))
