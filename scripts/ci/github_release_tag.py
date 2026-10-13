"""Tag a fully verified release and explicitly dispatch its publication workflow."""

import json
import os
from pathlib import Path
import re
import subprocess
import urllib.error

import yaml

from .github import GitHub


def release_plan(version, chart, receipt, *, repository, sha):
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?", version):
        raise ValueError("VERSION is not a release version")
    if chart["version"] != version or chart["appVersion"] != version:
        raise ValueError("VERSION and chart versions differ; prepare the complete release first")
    if (receipt["version"] != 2 or receipt["repository"] != repository or receipt["sha"] != sha or
            not re.fullmatch(r"[a-f0-9]{40}", sha) or not receipt["jobs"] or
            not (receipt["branch"] == "main" or receipt["branch"].startswith("maint/"))):
        raise ValueError("Release source differs from its canonical verification receipt")
    return {"tag": "v" + version, "sha": sha, "run_id": receipt["run_id"]}


def tag_and_dispatch(api, repository, plan):
    root = f"repos/{repository}"
    ref_path = root + "/git/ref/tags/" + plan["tag"]
    try:
        api.get(ref_path)
    except urllib.error.HTTPError as error:
        error.close()
        if error.code != 404:
            raise
    else:
        return {**plan, "created": False, "dispatched": False}
    annotation = api.post(root + "/git/tags", {
        "tag": plan["tag"], "message": "Release " + plan["tag"].removeprefix("v"),
        "object": plan["sha"], "type": "commit",
    })
    if (annotation["object"]["type"], annotation["object"]["sha"]) != ("commit", plan["sha"]):
        raise ValueError("Created release tag points to a different object")
    api.post(root + "/git/refs", {"ref": "refs/tags/" + plan["tag"], "sha": annotation["sha"]})
    if api.get(ref_path)["object"]["sha"] != annotation["sha"]:
        raise ValueError("Published release tag differs from the verified annotation")
    api.post(root + "/actions/workflows/release.yml/dispatches", {
        "ref": plan["tag"], "inputs": {"verification_run": str(plan["run_id"])},
    })
    return {**plan, "created": True, "dispatched": True}


def main():
    repository = os.environ["GITHUB_REPOSITORY"]
    receipt = json.loads(Path(".publication/verified-source.json").read_text())
    sha = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    plan = release_plan(Path("VERSION").read_text().strip(),
                        yaml.safe_load(Path("charts/haptic/Chart.yaml").read_text()), receipt,
                        repository=repository, sha=sha)
    result = tag_and_dispatch(GitHub(os.environ["GH_TOKEN"]), repository, plan)
    Path(".publication/release-tag.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
