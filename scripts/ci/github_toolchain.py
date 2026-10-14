"""Use cached CI images only when they match the current build inputs."""

import argparse
import json
from pathlib import Path
import re
import subprocess
import sys


PUBLISHED = "ghcr.io/haproxy-haptic/haptic-ci"


def selection(inventory, reference, build_hash):
    if not re.fullmatch(r"[a-f0-9]{64}", build_hash):
        raise ValueError("CI build input hash is invalid")
    entry = inventory["images"].get(reference)
    if entry is None:
        raise ValueError("CI image has no recorded build inputs")
    kind = entry["kind"]
    if kind == "ci" and re.fullmatch(r"3\.[0-9]+", entry["haproxy"]):
        target = "build-ci-image"
        arguments = ["HAPROXY_VERSION=" + entry["haproxy"]]
        local = "haptic-ci:" + build_hash + "-hp" + entry["haproxy"]
        published = PUBLISHED + ":" + build_hash + "-hp" + entry["haproxy"]
    elif kind == "docs":
        target, arguments, local = "build-docs-image", [], "haptic-docs:" + build_hash
        published = PUBLISHED + ":docs-" + build_hash
    else:
        raise ValueError("CI image kind or HAProxy version is invalid")
    return {"source": reference, "kind": kind, "build_hash": build_hash,
            "cached_hash": entry["build_input_hash"], "target": target, "arguments": arguments,
            "image": reference if build_hash == entry["build_input_hash"] else local,
            "published": published, "rebuild": build_hash != entry["build_input_hash"], "built": False}


def resolve(inventory, reference, *, run=subprocess.run, output=subprocess.check_output):
    build_hash = output(["bash", "scripts/ci-image-input-hash.sh"], text=True).strip()
    selected = selection(inventory, reference, build_hash)
    # Only main's CI images workflow pushes these tags; the label check below still applies.
    if selected["rebuild"] and run(["docker", "pull", selected["published"]], stdout=sys.stderr).returncode == 0:
        run(["docker", "tag", selected["published"], selected["image"]], check=True)
        return verified(selected, output)
    bootstrap = inventory["bootstrap"] if selected["rebuild"] else reference
    if bootstrap not in inventory["images"] or not re.fullmatch(r"[a-zA-Z0-9./_-]+@sha256:[a-f0-9]{64}", bootstrap):
        raise ValueError("CI bootstrap must be a recorded immutable image")
    run(["bash", "scripts/retry.sh", "docker", "pull", bootstrap], check=True, stdout=sys.stderr)
    if selected["rebuild"]:
        root = str(Path.cwd())
        run(["docker", "run", "--rm", "--volume", "/var/run/docker.sock:/var/run/docker.sock",
             "--volume", root + ":" + root, "--workdir", root, "--entrypoint", "bash", bootstrap,
             "-euc", 'git config --global --add safe.directory "$PWD"; make "$@"', "--",
             selected["target"], *selected["arguments"]], check=True, stdout=sys.stderr)
        selected["built"] = True
    return verified(selected, output)


def verified(selected, output):
    image = json.loads(output(["docker", "image", "inspect", selected["image"]], text=True))[0]
    label = (image["Config"].get("Labels") or {}).get("haptic.ci.build-input-hash")
    if (selected["kind"] == "ci" or selected["rebuild"]) and label != selected["build_hash"]:
        raise ValueError("Selected CI image differs from the current build inputs")
    return {**selected, "image_id": image["Id"]}


def publish(inventory, *, run=subprocess.run, output=subprocess.check_output):
    """Push every CI image that the current build inputs had to rebuild."""
    for reference in sorted(inventory["images"]):
        result = resolve(inventory, reference, run=run, output=output)
        if result["built"]:
            run(["docker", "tag", result["image"], result["published"]], check=True)
            run(["bash", "scripts/retry.sh", "docker", "push", result["published"]], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("reference", nargs="?")
    parser.add_argument("--evidence", type=Path)
    parser.add_argument("--publish", action="store_true")
    args = parser.parse_args()
    inventory = json.loads(Path(".github/ci-toolchains.json").read_text())
    if args.publish:
        publish(inventory)
        return
    if args.reference is None:
        parser.error("a CI image reference is required")
    result = resolve(inventory, args.reference)
    if args.evidence:
        args.evidence.parent.mkdir(parents=True, exist_ok=True)
        args.evidence.write_text(json.dumps(result, indent=2) + "\n")
    print(result["image"])


if __name__ == "__main__":
    main()
