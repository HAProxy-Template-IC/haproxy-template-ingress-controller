"""Choose the existing documentation-only policy from the actual Git comparison."""

import importlib.util
import json
import os
from pathlib import Path


SPEC = importlib.util.spec_from_file_location("verify_run", Path(__file__).with_name("verify-run.py"))
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


def main():
    event_name = os.environ["GITHUB_EVENT_NAME"]
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    sha = os.environ["GITHUB_SHA"]
    identity = VERIFY.checkout_identity(event_name, event, os.environ["GITHUB_REPOSITORY"], sha)
    manifest = json.loads(Path(".github/ci-verification.json").read_text())
    result = VERIFY.selection(manifest, event_name, event, identity, sha=sha)
    required = VERIFY.required_jobs(manifest, result["profile"])
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write(f"profile={result['profile']}\n")
    print(f"Selected {result['profile']} verification: {len(required)} required jobs")


if __name__ == "__main__":
    main()
