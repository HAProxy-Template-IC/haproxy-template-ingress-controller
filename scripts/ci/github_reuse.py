"""Reuse complete merge-queue verification only for its unchanged merged tree."""

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import urllib.error
import zipfile

from .github import GitHub
from . import github_evidence as evidence


def push_context(event_name, event, *, repository, sha, parents):
    branch = event.get("ref", "").removeprefix("refs/heads/")
    if (event_name != "push" or event.get("forced") is not False or
            event.get("deleted") is not False or not parents or
            event.get("ref") != "refs/heads/" + branch or
            not (branch == "main" or branch.startswith("maint/")) or
            event.get("before") != parents[0]):
        return None
    if event["repository"]["full_name"] != repository or event["after"] != sha:
        raise ValueError("Reuse requires the exact canonical pushed commit")
    if not all(re.fullmatch(r"[a-f0-9]{40}", value) for value in (sha, parents[0])):
        raise ValueError("Reuse requires complete commit identifiers")
    return {"branch": branch, "base_sha": parents[0]}


def queue_source(client, repository, run_id, *, branch, base_sha, tree, manifest):
    if type(run_id) is not int or run_id <= 0:
        raise ValueError("Merge verification requires a numeric source run")
    root = f"repos/{repository}"
    run = client.get(f"{root}/actions/runs/{run_id}")
    evidence.check_canonical_run(run, repository)
    sha = run["head_sha"]
    if (run["id"] != run_id or run["event"] != "merge_group" or
            not run["head_branch"].startswith("gh-readonly-queue/" + branch + "/") or
            not re.fullmatch(r"[a-f0-9]{40}", sha)):
        raise ValueError("Reuse requires a completed canonical merge-queue run for this branch")
    commit = client.get(f"{root}/git/commits/{sha}")
    if commit["sha"] != sha or commit["tree"]["sha"] != tree:
        raise ValueError("Merge-queue verification covers a different tree")
    jobs, artifact, content = evidence.load_evidence(client, repository, run)
    receipt = evidence.artifact_receipt(
        run, jobs, artifact, content, repository=repository, producer="full / verified",
        required=["full / verified", "verified"])
    expected = {"base_sha": base_sha, "base_branch": branch,
                "merge_group_ref": "refs/heads/" + run["head_branch"], "profile": "full"}
    if any(receipt.get(key) != value for key, value in expected.items()):
        raise ValueError("Merge-queue receipt does not cover the complete tree and merged parent")
    source = evidence.check_direct_receipt(run, jobs, artifact, receipt, repository=repository,
                                           sha=sha, tree=tree, manifest=manifest)
    comparison = client.get(f"{root}/compare/{base_sha}...{sha}")
    if comparison["status"] != "ahead" or comparison["merge_base_commit"]["sha"] != base_sha:
        raise ValueError("Merge-queue commit does not contain the merged target revision")
    evidence.require_unchanged_run(client, repository, run)
    return {**source, **expected}


def find_source(client, repository, *, branch, base_sha, tree, manifest, report=print):
    cutoff = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=1)
    try:
        response = client.get(f"repos/{repository}/actions/workflows/ci.yml/runs", event="merge_group",
                              created=">=" + cutoff.strftime("%Y-%m-%dT%H:%M:%SZ"), per_page=100)
        runs = sorted(response["workflow_runs"], key=lambda run: (run["created_at"], run["id"]), reverse=True)
        for run in runs:
            if not run["head_branch"].startswith("gh-readonly-queue/" + branch + "/"):
                continue
            sha = run["head_sha"]
            if not re.fullmatch(r"[a-f0-9]{40}", sha):
                raise ValueError("Merge-queue lookup returned an invalid commit")
            commit = client.get(f"repos/{repository}/git/commits/{sha}")
            if commit["sha"] != sha:
                raise ValueError("Merge-queue lookup returned a different commit")
            if commit["tree"]["sha"] != tree:
                continue
            if run["status"] != "completed" or run["conclusion"] != "success":
                report("Latest merge-queue verification of this tree did not pass; running normal CI")
                return None
            try:
                return queue_source(client, repository, run["id"], branch=branch, base_sha=base_sha,
                                    tree=tree, manifest=manifest)
            except (ValueError, KeyError, TypeError, zipfile.BadZipFile) as error:
                report(f"Run {run['id']} cannot be reused: {error}")
                return None
    except (urllib.error.URLError, TimeoutError, ValueError, KeyError, TypeError) as error:
        report(f"Merge verification lookup unavailable; running normal CI: {error}")
    return None


def reuse_receipt(run, source, *, repository, sha, tree, branch, base_sha, manifest):
    if (run["event"] != "push" or run["head_sha"] != sha or run["head_branch"] != branch or
            run["repository"]["full_name"] != repository or
            run["head_repository"]["full_name"] != repository or
            run["path"] != ".github/workflows/ci.yml"):
        raise ValueError("Reuse receipt requires canonical post-merge CI")
    return {"version": 3, "repository": repository, "run_id": run["id"],
            "run_attempt": run["run_attempt"], "sha": sha, "tree": tree, "branch": branch,
            "base_sha": base_sha, "event": "push", "source_sha": sha, "source_repository": repository,
            "profile": "full", "manifest_sha256": hashlib.sha256(manifest).hexdigest(),
            "reused_from": source, "jobs": source["jobs"]}


def check_reused_evidence(client, run, jobs, artifact, content, *, repository, sha, tree,
                          base_sha, manifest):
    evidence.check_run(run, repository)
    receipt = evidence.artifact_receipt(run, jobs, artifact, content, repository=repository,
                                        producer="verified", required=["selection", "verified"])
    source = queue_source(client, repository, receipt["reused_from"]["run_id"],
                          branch=run["head_branch"], base_sha=base_sha, tree=tree, manifest=manifest)
    expected = reuse_receipt(run, source, repository=repository, sha=sha, tree=tree,
                             branch=run["head_branch"], base_sha=base_sha, manifest=manifest)
    if receipt != expected:
        raise ValueError("Reused receipt differs from the original complete merge-queue evidence")
    return {**expected, "artifact_id": artifact["id"], "artifact_digest": artifact["digest"]}


def checkout():
    sha = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], text=True).strip()
    parents = subprocess.check_output(["git", "show", "-s", "--format=%P", "HEAD"], text=True).split()
    if sha != os.environ["GITHUB_SHA"]:
        raise ValueError("Reuse requires the exact workflow checkout")
    return sha, tree, parents


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("select", "verify"))
    parser.add_argument("--run", type=int)
    args = parser.parse_args()
    repository = os.environ["GITHUB_REPOSITORY"]
    sha, tree, parents = checkout()
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    context = push_context(os.environ["GITHUB_EVENT_NAME"], event,
                           repository=repository, sha=sha, parents=parents)
    manifest = Path(".github/ci-verification.json").read_bytes()
    client = GitHub(os.environ["GH_TOKEN"])
    if args.mode == "select":
        source = None
        if context and os.environ.get("REUSE_MERGE_VERIFICATION") == "true":
            source = find_source(client, repository, **context, tree=tree, manifest=manifest)
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"source_run_id={source['run_id'] if source else ''}\n")
        print(f"Selected merge verification {source['run_id']}" if source else "Selected normal CI")
        return
    if not context or not args.run:
        raise ValueError("Reuse verification requires a canonical merge and original run")
    source = queue_source(client, repository, args.run, **context, tree=tree, manifest=manifest)
    run = client.get(f"repos/{repository}/actions/runs/{os.environ['GITHUB_RUN_ID']}")
    if run["run_attempt"] != int(os.environ["GITHUB_RUN_ATTEMPT"]):
        raise ValueError("Post-merge verification attempt changed")
    receipt = reuse_receipt(run, source, repository=repository, sha=sha, tree=tree,
                            manifest=manifest, **context)
    Path("ci-verification.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(f"Verified {len(source['jobs'])} original merge-queue jobs for {sha} ({tree})")


if __name__ == "__main__":
    main()
