"""Download website build artifacts from their verified workflow attempt."""

import datetime
import hashlib
import io
import shutil
import zipfile

from .github import jobs_for_run
from .github_evidence import require_unchanged_run
from .github_site_versions import extract


def check_artifact(artifact, job, run, suite, content):
    origin = artifact["workflow_run"]
    expected = (run["id"], run["head_sha"], run["head_branch"])
    if (artifact["name"] != "ci-evidence-" + suite or artifact["expired"] or
            (origin["id"], origin["head_sha"], origin["head_branch"]) != expected or
            job["conclusion"] != "success" or job["status"] != "completed" or
            (job["run_id"], job["head_sha"], job["run_attempt"]) !=
            (run["id"], run["head_sha"], run["run_attempt"])):
        raise ValueError("Website artifact does not belong to the verified job attempt")
    created = datetime.datetime.fromisoformat(artifact["created_at"])
    if not (datetime.datetime.fromisoformat(job["started_at"]) <= created <=
            datetime.datetime.fromisoformat(job["completed_at"])):
        raise ValueError("Website artifact was created outside the verified job attempt")
    if artifact["digest"] != "sha256:" + hashlib.sha256(content).hexdigest():
        raise ValueError("Website artifact differs from GitHub's SHA-256 record")


def download_inputs(api, repository, receipt, scratch):
    source = receipt.get("reused_from", receipt)
    root = f"repos/{repository}/actions/runs/{source['run_id']}"
    run = api.get(root)
    if (run["head_sha"] != source["sha"] or run["run_attempt"] != source["run_attempt"] or
            run["repository"]["full_name"] != repository or run["head_repository"]["full_name"] != repository):
        raise ValueError("Website verification source changed after it was checked")
    require_unchanged_run(api, repository, run)
    jobs = jobs_for_run(api, repository, run)
    artifacts = list(api.all(root + "/artifacts", "artifacts"))
    provenance = {"source": receipt, "artifacts": {}}
    directories = {}
    selections = {"docs": ".pilot/docs-preview.tar.gz"}
    if source.get("profile", "full") == "full":
        selections["wasm"] = ".pilot/playground.tar.gz"
    for suite, filename in selections.items():
        verified = [job for job in source["jobs"] if job["name"] == source["job_prefix"] + suite]
        if len(verified) != 1:
            raise ValueError("Website build is missing from the verification receipt")
        producing = [job for job in jobs if job["id"] == verified[0]["id"] and job["name"] == verified[0]["name"]]
        matching = [artifact for artifact in artifacts if artifact["name"] == "ci-evidence-" + suite and not artifact["expired"]]
        if len(producing) != 1 or len(matching) != 1:
            raise ValueError("Website publication requires a unique retained build artifact")
        artifact, job = matching[0], producing[0]
        content = api.artifact(repository, artifact["id"], 1_000_000_000)
        check_artifact(artifact, job, run, suite, content)
        bundle_path = scratch / (suite + ".tar.gz")
        with zipfile.ZipFile(io.BytesIO(content)) as bundle:
            members = [member for member in bundle.infolist() if member.filename == filename]
            if len(members) != 1 or members[0].file_size > 1_000_000_000:
                raise ValueError("Website artifact payload is missing, duplicated, or oversized")
            with bundle.open(members[0]) as stream, bundle_path.open("xb") as output:
                shutil.copyfileobj(stream, output)
        del content
        destination = scratch / suite
        extract(bundle_path, destination)
        directories[suite] = destination
        area = "playground" if suite == "wasm" else "docs"
        provenance[area] = {"version": "dev", "commit": source["source_sha"],
                            "pipeline": str(source["run_id"]), "job": "suite_" + suite}
        provenance["artifacts"][suite] = {"id": artifact["id"], "digest": artifact["digest"], "job_id": job["id"]}
    require_unchanged_run(api, repository, run)
    return directories, provenance
