"""Prepare the GoReleaser configuration for the repository that publishes it."""

import argparse
import copy
from pathlib import Path
import re

import yaml


def configuration(source, repository, snapshot_sha=None):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+", repository):
        raise ValueError("GitHub repository must be an owner/name pair")
    owner, name = repository.split("/")
    result = copy.deepcopy(source)
    if result["release"]["github"] != {"owner": owner, "name": name}:
        raise ValueError(f"Release configuration publishes to another repository than {repository}")
    if snapshot_sha is not None:
        if not re.fullmatch(r"[0-9a-f]{40}", snapshot_sha):
            raise ValueError("Snapshot source must be a full commit SHA")
        result["snapshot"]["version_template"] = "main-" + snapshot_sha[:8]
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--source", type=Path, default=Path(".goreleaser.yml"))
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--snapshot-sha")
    args = parser.parse_args()
    result = configuration(yaml.safe_load(args.source.read_text()), args.repository, args.snapshot_sha)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(yaml.safe_dump(result, sort_keys=False))


if __name__ == "__main__":
    main()
