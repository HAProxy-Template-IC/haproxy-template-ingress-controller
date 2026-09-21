#!/usr/bin/env python3
"""Bind a conformance report to the source and the binaries actually tested."""

import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def run(*args):
    return subprocess.check_output(args, text=True, timeout=120).strip()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def chart_digest():
    checksum = hashlib.sha256()
    root = Path("charts/haptic")
    for path in sorted(root.rglob("*")):
        if path.is_file():
            checksum.update(str(path.relative_to(root)).encode() + b"\0")
            checksum.update(path.read_bytes() + b"\0")
    return checksum.hexdigest()


def version_fields(output):
    fields = {}
    for label, key in (("Version", "version"), ("Commit", "commit"), ("Source Hash", "source_hash")):
        match = re.search(rf"^\s*{label}:\s*(\S+)\s*$", output, re.MULTILINE)
        if not match:
            raise ValueError(f"controller version output is missing {label}")
        fields[key] = match.group(1)
    return fields


def controller_provenance(args, expected_source_hash):
    info = json.loads(run("docker", "image", "inspect", "--format", "{{json .}}", args.controller_image))
    identity = version_fields(run("docker", "run", "--rm", "--entrypoint", "/usr/local/bin/haptic", args.controller_image, "version"))
    if identity["source_hash"] != expected_source_hash or identity["version"] != args.implementation_version:
        raise ValueError("controller image does not match the report version and source hash")
    binary_hash = run("docker", "run", "--rm", "--entrypoint", "sha256sum", args.controller_image, "/usr/local/bin/haptic").split()[0]
    if not re.fullmatch(r"[0-9a-f]{64}", binary_hash):
        raise ValueError("controller binary digest is invalid")
    kubectl = ["kubectl", "--kubeconfig", args.kubeconfig]
    selector = f"app.kubernetes.io/instance={args.release},app.kubernetes.io/component=controller"
    pods = json.loads(run(*kubectl, "get", "pods", "-n", args.namespace, "-l", selector, "-o", "json"))["items"]
    running = []
    for pod in pods:
        if pod["metadata"].get("deletionTimestamp"):
            continue
        statuses = [entry for entry in pod["status"].get("containerStatuses", []) if entry["name"] == "controller"]
        if len(statuses) != 1 or not statuses[0].get("ready"):
            raise ValueError("every controller replica must be ready for conformance evidence")
        name = pod["metadata"]["name"]
        actual_hash = run(*kubectl, "exec", "-n", args.namespace, name, "-c", "controller", "--", "sha256sum", "/usr/local/bin/haptic").split()[0]
        if actual_hash != binary_hash:
            raise ValueError("a running controller binary differs from the tested image")
        running.append({"pod": name, "uid": pod["metadata"]["uid"], "image_id": statuses[0]["imageID"], "binary_sha256": actual_hash})
    if not running:
        raise ValueError("no running controller was found for conformance evidence")
    return {
        **identity,
        "image_reference": args.controller_image,
        "image_id": info["Id"],
        "registry_digests": info.get("RepoDigests", []),
        "binary_sha256": binary_hash,
        "running_replicas": running,
    }


def checkout_dirty():
    return bool(run("git", "status", "--porcelain", "--untracked-files=all"))


def validate_release_controller(controller, commit):
    if not controller["registry_digests"]:
        raise ValueError("release evidence requires a registry digest for the published image")
    if controller["commit"] != commit:
        raise ValueError("release image was not built from the checked-out release commit")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--controller-image", required=True)
    parser.add_argument("--implementation-version", required=True)
    parser.add_argument("--test-exit-code", required=True, type=int)
    parser.add_argument("--release-evidence", action="store_true")
    parser.add_argument("--kubeconfig", default=os.environ.get("HAPTIC_E2E_KUBECONFIG_PATH", "/tmp/haproxy-e2e-kubeconfig"))
    parser.add_argument("--namespace", default=os.environ.get("CTRL_NAMESPACE", "haptic"))
    parser.add_argument("--release", default=os.environ.get("RELEASE_NAME", "haptic"))
    args = parser.parse_args()
    record = {
        "schema_version": 1,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "test_exit_code": args.test_exit_code,
        "release_evidence": args.release_evidence,
        "selection": "full-unsharded-suite",
        "job_url": os.environ.get("CI_JOB_URL"),
    }
    errors = []
    try:
        if os.environ.get("TEST_RUN_PATTERN"):
            raise ValueError("filtered runs cannot produce conformance evidence")
        record["git_commit"] = run("git", "rev-parse", "HEAD")
        record["dirty_checkout"] = checkout_dirty()
        if args.release_evidence:
            if record["dirty_checkout"]:
                raise ValueError("release evidence requires a clean source checkout")
            if run("git", "describe", "--exact-match", "--tags", "HEAD") != "v" + args.implementation_version:
                raise ValueError("release evidence requires the tested version's exact tag")
        source_hash = run("scripts/source-hash.sh")
        record["source_hash"] = source_hash
        record["chart_sha256"] = chart_digest()
        record["controller"] = controller_provenance(args, source_hash)
        if args.release_evidence:
            validate_release_controller(record["controller"], record["git_commit"])
        record["kubernetes_version"] = json.loads(run("kubectl", "--kubeconfig", args.kubeconfig, "version", "-o", "json"))["serverVersion"]["gitVersion"]
        matches = re.findall(r"^\s*sigs\.k8s\.io/gateway-api/conformance\s+(\S+)", Path("go.mod").read_text(), re.MULTILINE)
        if len(matches) != 1:
            raise ValueError("the pinned Gateway API conformance version is missing or ambiguous")
        record["gateway_api_version"] = matches[0]
        record["conformance_binary_sha256"] = digest(Path("/tmp/haptic-conformance.test"))
        record["report_sha256"] = digest(args.report)
        validator = subprocess.run([
            sys.executable, "scripts/validate-conformance-report.py", str(args.report),
            "--implementation-version", args.implementation_version,
            "--gateway-version", matches[0],
        ], capture_output=True, text=True, check=False, timeout=30)
        if validator.returncode:
            raise ValueError("report validation failed; inspect the retained report's coverage and version")
        record["profiles"] = json.loads(validator.stdout)
    except (OSError, ValueError, KeyError, subprocess.SubprocessError) as error:
        errors.append(str(error))
    if args.test_exit_code != 0:
        errors.append("the conformance test command failed")
    record["errors"] = errors
    record["verified"] = not errors
    args.output.write_text(json.dumps(record, indent=2, sort_keys=True) + "\n")
    for error in errors:
        print(f"Conformance evidence failed: {error}", file=sys.stderr)
    return int(bool(errors))


if __name__ == "__main__":
    sys.exit(main())
