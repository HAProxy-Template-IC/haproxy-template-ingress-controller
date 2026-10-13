"""Bind snapshot versions and image manifests to a verified main commit."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

import yaml


def snapshot_plan(receipt, values, *, repository, sha, tree, manifest):
    if (not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+", repository) or
            not re.fullmatch(r"[0-9a-f]{40}", sha)):
        raise ValueError("Snapshot publication requires a repository and full commit SHA")
    expected = {"version": 2, "repository": repository, "sha": sha, "tree": tree,
                "branch": "main", "source_sha": sha, "source_repository": repository,
                "manifest_sha256": hashlib.sha256(manifest).hexdigest()}
    if receipt.get("profile", "full") != "full" or any(receipt.get(key) != value for key, value in expected.items()):
        raise ValueError("Snapshot publication requires the complete verified main source")
    destination = "ghcr.io/" + repository.split("/")[0].lower() + "/haptic"
    if (values["controller"]["image"]["repository"] != destination or
            values["spoaHub"]["image"]["repository"] != destination + "/spoa-hub" or
            values["controller"]["image"].get("tag") or values["spoaHub"]["image"].get("tag")):
        raise ValueError("Chart image repositories or tags override GitHub snapshot publication")
    return {"sha": sha, "repository": destination, "chart_version": "0.0.0-main.g" + sha[:8],
            "app_version": "main-" + sha[:8]}


def check_index(index, platforms):
    result = {}
    for child in index["manifests"]:
        descriptor = child["platform"]
        platform = "/".join(filter(None, (descriptor["os"], descriptor["architecture"],
                                          descriptor.get("variant"))))
        digest = child["digest"]
        if platform in result or not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
            raise ValueError("Snapshot image contains a duplicate platform or invalid manifest digest")
        result[platform] = digest
    if set(result) != set(platforms):
        raise ValueError("Snapshot image does not contain exactly the supported platforms")
    return result


def verify_image(reference, platforms, sha, version):
    def inspect(target, *arguments):
        return subprocess.check_output(["docker", "buildx", "imagetools", "inspect", target,
                                        *arguments], text=True)

    digest = json.loads(inspect(reference, "--format", "{{json .Manifest.Digest}}"))
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise ValueError("Snapshot image has no manifest digest")
    repository = reference.rsplit(":", 1)[0]
    immutable = repository + "@" + digest
    children = check_index(json.loads(inspect(immutable, "--raw")), platforms)
    for child in children.values():
        config = json.loads(inspect(repository + "@" + child, "--format", "{{json .Image}}"))
        labels = config["config"]["Labels"]
        if labels.get("org.opencontainers.image.revision") != sha or labels.get(
                "org.opencontainers.image.version") != version:
            raise ValueError("Snapshot image does not match the verified source and version")
    return {"reference": immutable, "platforms": children}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-image")
    parser.add_argument("--platforms")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    sha = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if args.verify_image:
        result = verify_image(args.verify_image, args.platforms.split(","), sha, "main-" + sha[:8])
    else:
        tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], text=True).strip()
        subprocess.run(["git", "diff", "--exit-code", "HEAD", "--"], check=True)
        result = snapshot_plan(json.loads(Path(".publication/verified-source.json").read_text()),
                               yaml.safe_load(Path("charts/haptic/values.yaml").read_text()),
                               repository=os.environ["GITHUB_REPOSITORY"], sha=sha, tree=tree,
                               manifest=Path(".github/ci-verification.json").read_bytes())
    args.output.parent.mkdir(exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
