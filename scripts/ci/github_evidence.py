"""Validate canonical GitHub verification artifacts and their producing job attempts."""

import datetime
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import re
import zipfile

from .github import jobs_for_run


SPEC = importlib.util.spec_from_file_location("verify_run", Path(__file__).resolve().parents[2] / ".github/scripts/verify-run.py")
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


def check_run(run, repository):
    check_canonical_run(run, repository)
    if run["event"] not in {"push", "workflow_dispatch"}:
        raise ValueError("Publication requires canonical branch CI")
    if run["head_branch"] != "main" and not run["head_branch"].startswith("maint/"):
        raise ValueError("Publication requires main or a maintenance branch")


def check_canonical_run(run, repository):
    if (run["repository"]["full_name"] != repository or
            run["head_repository"]["full_name"] != repository):
        raise ValueError("Publication requires verification in the canonical repository")
    if run["path"] != ".github/workflows/ci.yml":
        raise ValueError("Publication requires canonical branch CI")
    if run["status"] != "completed" or run["conclusion"] != "success":
        raise ValueError("Publication requires a completed successful workflow")


def check_evidence(run, jobs, artifact, content, *, repository, sha, tree, manifest,
                   allow_prose=False, comparison=None):
    check_run(run, repository)
    receipt = artifact_receipt(run, jobs, artifact, content, repository=repository,
                               producer="full / verified", required=["full / verified", "verified"])
    return check_direct_receipt(run, jobs, artifact, receipt, repository=repository,
                                sha=sha, tree=tree, manifest=manifest,
                                allow_prose=allow_prose, comparison=comparison)


def artifact_receipt(run, jobs, artifact, content, *, repository, producer, required):
    sha = run["head_sha"]
    VERIFY.verify(run, jobs, required, repository=repository, sha=sha,
                  attempt=run["run_attempt"])
    producer = next(job for job in jobs if job["name"] == producer)
    origin = artifact["workflow_run"]
    if (artifact["name"] != "ci-verification" or artifact["expired"] or
            (origin["id"], origin["head_sha"], origin["head_branch"]) !=
            (run["id"], sha, run["head_branch"])):
        raise ValueError("Verification artifact belongs to another source")
    created = datetime.datetime.fromisoformat(artifact["created_at"])
    if not (datetime.datetime.fromisoformat(producer["started_at"]) <= created <=
            datetime.datetime.fromisoformat(producer["completed_at"])):
        raise ValueError("Verification artifact was not produced by this successful job attempt")
    return unpack_receipt(artifact, content)


def unpack_receipt(artifact, content):
    if len(content) > 2 * 1024 * 1024 or artifact["digest"] != "sha256:" + hashlib.sha256(content).hexdigest():
        raise ValueError("Verification artifact digest or size differs from GitHub's record")
    with zipfile.ZipFile(io.BytesIO(content)) as archive:
        if archive.namelist() != ["ci-verification.json"] or archive.infolist()[0].file_size > 1024 * 1024:
            raise ValueError("Verification artifact has unexpected contents")
        return json.loads(archive.read("ci-verification.json"))


def check_direct_receipt(run, jobs, artifact, receipt, *, repository, sha, tree, manifest,
                         allow_prose=False, comparison=None):
    profile = receipt.get("profile", "full")
    policy = json.loads(manifest)
    if profile == "prose":
        base = receipt.get("comparison_base", "")
        if not allow_prose or comparison is None:
            raise ValueError("This publication requires full CI verification")
        paths = comparison(base, sha)
        if (VERIFY.paths_profile(policy, paths) != "prose" or
                receipt.get("changed_paths_sha256") != VERIFY.paths_digest(paths)):
            raise ValueError("Documentation verification does not match the published changes")
    required = VERIFY.required_jobs(policy, profile)
    checked = VERIFY.verify(run, jobs, required, repository=repository, sha=sha,
                            attempt=run["run_attempt"], prefix="full / ",
                            allowed_failures=VERIFY.allowed_failure_jobs(policy, profile))
    expected = {
        "version": 2, "repository": repository, "run_id": run["id"], "run_attempt": run["run_attempt"],
        "sha": sha, "tree": tree, "jobs": checked, "job_prefix": "full / ", "event": run["event"],
        "api_sha": sha, "source_sha": sha, "source_repository": repository,
        "manifest_sha256": hashlib.sha256(manifest).hexdigest(),
    }
    if any(receipt.get(key) != value for key, value in expected.items()):
        raise ValueError("Verification receipt does not cover this complete source and job inventory")
    return {**expected, "artifact_id": artifact["id"], "artifact_digest": artifact["digest"],
            "branch": run["head_branch"], "profile": profile}


def load_evidence(client, repository, run):
    root = f"repos/{repository}/actions/runs/{run['id']}"
    jobs = jobs_for_run(client, repository, run)
    artifacts = [artifact for artifact in client.all(root + "/artifacts", "artifacts")
                 if artifact["name"] == "ci-verification" and not artifact["expired"]]
    if len(artifacts) != 1:
        raise ValueError("Publication requires exactly one retained verification artifact")
    artifact = artifacts[0]
    content = client.artifact(repository, artifact["id"], 2 * 1024 * 1024)
    return jobs, artifact, content


def require_unchanged_run(client, repository, run):
    refreshed = client.get(f"repos/{repository}/actions/runs/{run['id']}")
    check_canonical_run(refreshed, repository)
    keys = ("id", "head_sha", "head_branch", "run_attempt", "event")
    if any(refreshed[key] != run[key] for key in keys):
        raise ValueError("Verification changed while publication evidence was being checked")


def publication_changes(base, sha):
    if (not re.fullmatch(r"[a-f0-9]{40}", base) or base == sha or
            not VERIFY.git_is_ancestor(base, sha)):
        raise ValueError("Documentation publication requires the verified comparison base")
    return VERIFY.git_changed_paths(base, sha)
