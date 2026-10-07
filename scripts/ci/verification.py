"""Bind required GitLab job results to the code tested by the merge train."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

from .configuration import instances, load
from .gitlab import GitLab


POLICY_PATH = Path(".gitlab/ci/verification-policy.json")


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def profile(paths, policy):
    return "prose" if paths and all(
        path in policy["prose_paths"] or any(re.fullmatch(pattern, path) for pattern in policy["prose_patterns"])
        for path in paths) else "full"


def required_jobs(config, policy, selected, source_branch):
    names = list(policy["common_jobs"])
    if selected == "full":
        names.extend(policy["full_jobs"])
    if source_branch.startswith("release/v"):
        names.append("prepare-spoa-release")
    return sorted(instance for name in names for instance in instances(config, name))


def validate_jobs(jobs, required, pipeline_id, sha):
    latest = {}
    for item in jobs:
        if item["name"] not in latest or item["id"] > latest[item["name"]]["id"]:
            latest[item["name"]] = item
    errors = []
    for name in required:
        item = latest.get(name)
        if item is None:
            errors.append(f"{name}: missing")
        elif item["status"] != "success" or item.get("allow_failure"):
            errors.append(f"{name}: {item['status']}, allow_failure={item.get('allow_failure')}")
        elif item["pipeline"]["id"] != pipeline_id or item["commit"]["id"] != sha:
            errors.append(f"{name}: belongs to different source or pipeline")
    if errors:
        raise ValueError("Required CI verification failed: " + "; ".join(errors))
    return [{"name": name, "id": latest[name]["id"]} for name in required]


def context(api, project, mr_iid, env):
    if env.get("CI_MERGE_REQUEST_EVENT_TYPE") != "merge_train":
        raise ValueError("Final verification requires a merge-train pipeline")
    mr = api.get(f"projects/{project}/merge_requests/{mr_iid}")
    if mr["state"] != "opened" or mr["sha"] != env["CI_MERGE_REQUEST_SOURCE_BRANCH_SHA"]:
        raise ValueError("Merge request source changed; requeue the reviewed candidate")
    if not (mr["target_branch"] == "main" or mr["target_branch"].startswith("maint/")):
        raise ValueError("Verification only permits main and maintenance targets")
    if git("rev-parse", "HEAD") != env["CI_COMMIT_SHA"]:
        raise ValueError("Checkout does not match the pipeline commit")
    train = api.get(f"projects/{project}/merge_trains/merge_requests/{mr_iid}")
    if train["pipeline"]["id"] != int(env["CI_PIPELINE_ID"]) or train["pipeline"]["sha"] != env["CI_COMMIT_SHA"]:
        raise ValueError("Pipeline is not the current merge train")
    return mr


def verify_train(api, env, output):
    project, mr_iid = env["CI_PROJECT_ID"], env["CI_MERGE_REQUEST_IID"]
    mr = context(api, project, mr_iid, env)
    policy = json.loads(POLICY_PATH.read_text())
    target = env["CI_MERGE_REQUEST_TARGET_BRANCH_SHA"]
    if not target or not re.fullmatch(r"[a-f0-9]{40}", target):
        raise ValueError("Merge train did not identify its target revision")
    subprocess.run(["git", "merge-base", "--is-ancestor", target, "HEAD"], check=True)
    paths = subprocess.check_output(["git", "diff", "--name-only", "-z", target, "HEAD"]).decode().rstrip("\0").split("\0")
    selected = profile(paths, policy)
    required = required_jobs(load(), policy, selected, mr["source_branch"])
    pipeline_id = int(env["CI_PIPELINE_ID"])
    jobs = list(api.all(f"projects/{project}/pipelines/{pipeline_id}/jobs"))
    checked = validate_jobs(jobs, required, pipeline_id, env["CI_COMMIT_SHA"])
    receipt = {"version": 1, "project_id": int(project), "mr_iid": int(mr_iid),
               "pipeline_id": pipeline_id, "pipeline_sha": env["CI_COMMIT_SHA"],
               "source_sha": mr["sha"], "target_sha": target, "target_branch": mr["target_branch"],
               "tree": git("rev-parse", "HEAD^{tree}"), "profile": selected,
               "policy_sha256": hashlib.sha256(POLICY_PATH.read_bytes()).hexdigest(),
               "build_pins_sha256": hashlib.sha256(Path(".gitlab/ci/build-pins.yml").read_bytes()).hexdigest(),
               "ci_image_tag": env["CI_IMAGE_TAG"],
               "required_jobs": checked}
    Path(output).write_text(json.dumps(receipt, indent=2) + "\n")
    print(f"Verified {len(checked)} jobs for {receipt['tree']} ({selected})")


def validate_receipt(receipt, mr, train, jobs, project, tree, policy, config):
    pipeline = train["pipeline"]
    expected = {"version": 1, "project_id": int(project), "mr_iid": mr["iid"],
                "pipeline_id": pipeline["id"], "pipeline_sha": pipeline["sha"],
                "source_sha": mr["sha"], "target_branch": mr["target_branch"], "tree": tree,
                "policy_sha256": hashlib.sha256(POLICY_PATH.read_bytes()).hexdigest(),
                "build_pins_sha256": hashlib.sha256(Path(".gitlab/ci/build-pins.yml").read_bytes()).hexdigest()}
    if train["status"] not in {"merged", "merging"} or pipeline["status"] != "success":
        raise ValueError("Merge did not complete a successful train")
    if pipeline["ref"] != f"refs/merge-requests/{mr['iid']}/train" or train["target_branch"] != mr["target_branch"]:
        raise ValueError("Train belongs to a different merge request or target")
    for key, value in expected.items():
        if receipt.get(key) != value:
            raise ValueError(f"Train verification does not match published code: {key}")
    if receipt["profile"] not in {"prose", "full"}:
        raise ValueError("Unknown verification profile")
    required = required_jobs(config, policy, receipt["profile"], mr["source_branch"])
    checked = validate_jobs(jobs, required, pipeline["id"], pipeline["sha"])
    if receipt["required_jobs"] != checked:
        raise ValueError("Required job results changed after train verification")


def verify_publication(api, env):
    project, sha = env["CI_PROJECT_ID"], env["CI_COMMIT_SHA"]
    branch = env.get("CI_COMMIT_BRANCH")
    if branch and branch != "main" and not branch.startswith("maint/"):
        raise ValueError("Publication requires a main, maintenance, or release ref")
    if git("rev-parse", "HEAD") != sha:
        raise ValueError("Checkout does not match the publication commit")
    candidates = [mr for mr in api.all(f"projects/{project}/repository/commits/{sha}/merge_requests")
                  if mr["state"] == "merged" and mr["merge_commit_sha"] == sha]
    if len(candidates) != 1:
        raise ValueError("Publication requires one merge request for this exact merge commit")
    mr = api.get(f"projects/{project}/merge_requests/{candidates[0]['iid']}")
    if not (mr["target_branch"] == "main" or mr["target_branch"].startswith("maint/")):
        raise ValueError("Publication requires a main or maintenance merge")
    train = api.get(f"projects/{project}/merge_trains/merge_requests/{mr['iid']}")
    pipeline = api.get(f"projects/{project}/pipelines/{train['pipeline']['id']}")
    train["pipeline"] = pipeline
    jobs = list(api.all(f"projects/{project}/pipelines/{pipeline['id']}/jobs"))
    gate = validate_jobs(jobs, ["verify-merge-train"], pipeline["id"], pipeline["sha"])[0]
    receipt = api.get(f"projects/{project}/jobs/{gate['id']}/artifacts/ci-verification.json")
    policy = json.loads(POLICY_PATH.read_text())
    validate_receipt(receipt, mr, train, jobs, project, git("rev-parse", "HEAD^{tree}"), policy, load())
    target = receipt["target_sha"]
    if not re.fullmatch(r"[a-f0-9]{40}", target):
        raise ValueError("Missing tested target revision")
    subprocess.run(["git", "merge-base", "--is-ancestor", target, "HEAD"], check=True)
    paths = subprocess.check_output(["git", "diff", "--name-only", "-z", target, "HEAD"]).decode().rstrip("\0").split("\0")
    if profile(paths, policy) != receipt["profile"]:
        raise ValueError("Changed paths do not match the verified profile")
    if git("rev-parse", "HEAD^1") != target:
        raise ValueError("Target advanced beyond the code verified by the train")
    image_tag = subprocess.check_output(["scripts/ci-image-input-hash.sh"], text=True).strip()
    if receipt["ci_image_tag"] != image_tag:
        raise ValueError("CI build inputs differ from the verified train")
    print(f"Publication tree {receipt['tree']} verified by train {pipeline['id']}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default="ci-verification.json")
    parser.add_argument("--train-context", action="store_true")
    parser.add_argument("--publication", action="store_true")
    args = parser.parse_args()
    api = GitLab(os.environ["CI_API_V4_URL"])
    if args.publication:
        verify_publication(api, os.environ)
    elif args.train_context:
        context(api, os.environ["CI_PROJECT_ID"], os.environ["CI_MERGE_REQUEST_IID"], os.environ)
    else:
        verify_train(api, os.environ, args.output)
    return 0


if __name__ == "__main__":
    sys.exit(main())
