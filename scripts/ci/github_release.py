"""Derive GitHub publication settings from the canonical release configuration."""

import argparse
import copy
from pathlib import Path
import re

import yaml


SOURCE_REGISTRY = "registry.gitlab.com/haproxy-haptic/haptic"


def configuration(source, repository):
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+", repository):
        raise ValueError("GitHub repository must be an owner/name pair")
    owner, name = repository.split("/")
    result = copy.deepcopy(source)
    if result["release"].pop("gitlab") != {"owner": "haproxy-haptic", "name": "haptic"}:
        raise ValueError("Release source repository changed; review the GitHub publication mapping")
    result.pop("gitlab_urls", None)
    result["release"]["github"] = {"owner": owner, "name": name}
    result["snapshot"]["version_template"] = "ci-{{ .Env.GITHUB_RUN_ID }}"
    destination = f"ghcr.io/{owner.lower()}/haptic"
    for image in result["dockers_v2"]:
        targets = []
        for original in image["images"]:
            if original != SOURCE_REGISTRY and not original.startswith(SOURCE_REGISTRY + "/"):
                raise ValueError(f"Unmapped release image: {original}")
            targets.append(destination + original[len(SOURCE_REGISTRY):])
        image["images"] = targets
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repository", required=True)
    parser.add_argument("--source", type=Path, default=Path(".goreleaser.yml"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    result = configuration(yaml.safe_load(args.source.read_text()), args.repository)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(yaml.safe_dump(result, sort_keys=False))


if __name__ == "__main__":
    main()
