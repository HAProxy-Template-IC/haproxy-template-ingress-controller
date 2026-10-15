"""Require complete canonical CI evidence before publishing its exact source."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys


sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from scripts.ci.github import GitHub
from scripts.ci.github_evidence import (
    VERIFY, check_evidence, check_run, load_evidence, publication_changes, require_unchanged_run,
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", required=True, type=int)
    parser.add_argument("--resolve", action="store_true")
    parser.add_argument("--allow-prose", action="store_true")
    args = parser.parse_args()
    repository = os.environ["GITHUB_REPOSITORY"]
    root = f"repos/{repository}/actions/runs/{args.run}"
    run = VERIFY.api(root)
    check_run(run, repository)
    if args.resolve:
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"sha={run['head_sha']}\n")
        return
    client = GitHub(os.environ["GH_TOKEN"])
    jobs, artifact, content = load_evidence(client, repository, run)
    sha = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    tree = subprocess.check_output(["git", "rev-parse", "HEAD^{tree}"], text=True).strip()
    manifest = Path(".github/ci-verification.json").read_bytes()
    arguments = dict(repository=repository, sha=sha, tree=tree, manifest=manifest)
    receipt = check_evidence(run, jobs, artifact, content, **arguments,
                             allow_prose=args.allow_prose, comparison=publication_changes)
    require_unchanged_run(client, repository, run)
    output = Path(".publication/verified-source.json")
    output.parent.mkdir(exist_ok=True)
    output.write_text(json.dumps(receipt, indent=2) + "\n")
    if "GITHUB_OUTPUT" in os.environ:
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as stream:
            stream.write(f"profile={receipt['profile']}\n")
    print(f"Publication verified {len(receipt['jobs'])} jobs for {sha} ({tree})")


if __name__ == "__main__":
    main()
