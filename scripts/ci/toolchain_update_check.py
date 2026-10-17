"""Verify an actual Dockerfile update reaches the selected CI toolchain."""

import argparse
import json
from pathlib import Path
import subprocess

from .github_toolchain import resolve


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("kind", choices=("ci", "docs"))
    args = parser.parse_args()
    inventory = json.loads(Path(".github/ci-toolchains.json").read_text())
    reference = inventory["bootstrap"] if args.kind == "ci" else next(
        ref for ref, item in inventory["images"].items() if item["kind"] == "docs")
    before = resolve(inventory, reference)
    dockerfile = Path("ci/images") / ("ci" if args.kind == "ci" else "python-docs") / "Dockerfile"
    original = dockerfile.read_bytes()
    probe = "HAPTIC_TOOLCHAIN_PROBE=updated-" + before["build_hash"]
    try:
        dockerfile.write_bytes(original + b"\nENV " + probe.encode() + b"\n")
        after = resolve(inventory, reference)
        inspected = json.loads(subprocess.check_output(["docker", "image", "inspect", after["image"]], text=True))[0]
        if (after["build_hash"] == before["build_hash"] or after["image_id"] == before["image_id"]
                or probe not in inspected["Config"]["Env"] or not after["rebuild"]):
            raise ValueError("Dockerfile update did not reach the selected toolchain")
        command = "go version && haproxy -v && kind version && helm version" if args.kind == "ci" else "mkdocs --version && mike --version"
        subprocess.run(["docker", "run", "--rm", "--entrypoint", "bash", after["image"], "-euc", command], check=True)
        directory = Path(".pilot")
        directory.mkdir(exist_ok=True)
        (directory / "toolchain-update.json").write_text(json.dumps({"before": before, "after": after,
                                                                    "probe": probe, "verified": True}, indent=2) + "\n")
    finally:
        dockerfile.write_bytes(original)


if __name__ == "__main__":
    main()
