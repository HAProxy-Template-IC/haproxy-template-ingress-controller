"""Check canary revision selection and post-tidy module identity."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "prepare-gateway-api-canary.sh"
REVISION = "1" * 40
ROOT = "sigs.k8s.io/gateway-api"
SUITE = ROOT + "/conformance"
MOCK_GO = r'''#!/usr/bin/env python3
import json
import os
import sys

args = sys.argv[1:]
with open(os.environ["MOCK_GO_LOG"], "a", encoding="utf-8") as log:
    log.write(json.dumps(args) + "\n")
if " ".join(args).startswith(os.environ.get("MOCK_GO_FAIL", "never")):
    sys.exit(9)
module = args[-1].split("@")[0]
key = "SUITE" if module.endswith("/conformance") else "ROOT"
version = "v0.0.0-20260912002310-111111111111" if key == "SUITE" else "v1.3.1-0.20260912002310-111111111111"
if args[0] == "list" and "-json" in args:
    metadata = {"Path": module, "Version": version, "Origin": {"Hash": os.environ.get("MOCK_HASH", "1" * 40)}}
    if os.environ.get("MOCK_NO_VERSION"):
        del metadata["Version"]
    print(json.dumps(metadata))
elif args[0] == "list":
    print(os.environ.get("MOCK_SELECTED_" + key, version))
elif args[0] == "get":
    print("dependency progress on stdout")
'''


class PrepareGatewayAPICanaryTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix="gateway-canary-test-")
        self.addCleanup(directory.cleanup)
        self.directory = Path(directory.name)
        mock_go = self.directory / "go"
        mock_go.write_text(MOCK_GO, encoding="utf-8")
        mock_go.chmod(0o755)
        self.log = self.directory / "calls.jsonl"

    def run_script(self, *args, **overrides):
        environment = dict(os.environ)
        environment.update(
            PATH=str(self.directory) + os.pathsep + environment["PATH"],
            MOCK_GO_LOG=str(self.log),
            **overrides,
        )
        return subprocess.run(
            ["bash", str(SCRIPT), *args],
            env=environment,
            capture_output=True,
            text=True,
            check=False,
        )

    def calls(self):
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines()]

    def test_resolve_returns_only_exact_commit(self):
        result = self.run_script("resolve")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, REVISION + "\n")
        self.assertEqual(self.calls(), [["list", "-mod=mod", "-m", "-json", ROOT + "@main"]])

    def test_prepare_uses_one_commit_and_checks_after_tidy(self):
        result = self.run_script("prepare", REVISION)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "")
        calls = self.calls()
        self.assertEqual(calls[2], ["get", ROOT + "@" + REVISION, SUITE + "@" + REVISION])
        self.assertEqual(calls[3], ["mod", "tidy"])
        self.assertEqual(calls[4:], [
            ["list", "-mod=mod", "-m", "-f", "{{.Version}}", ROOT],
            ["list", "-mod=mod", "-m", "-f", "{{.Version}}", SUITE],
        ])

    def test_post_tidy_drift_is_rejected(self):
        for module in ("ROOT", "SUITE"):
            with self.subTest(module=module):
                result = self.run_script("prepare", REVISION, **{"MOCK_SELECTED_" + module: "v1.6.2"})
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("Fix its imports", result.stderr)

    def test_bad_revision_does_not_call_go(self):
        result = self.run_script("prepare", "main")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_foreign_revision_is_rejected_before_mutation(self):
        result = self.run_script("prepare", REVISION, MOCK_HASH="2" * 40)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 1)

    def test_missing_version_is_rejected_before_mutation(self):
        result = self.run_script("prepare", REVISION, MOCK_NO_VERSION="1")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(len(self.calls()), 1)

    def test_resolve_rejects_missing_commit(self):
        result = self.run_script("resolve", MOCK_HASH="")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")

    def test_dependency_command_failure_propagates(self):
        for command in ("list", "get", "mod tidy"):
            with self.subTest(command=command):
                result = self.run_script("prepare", REVISION, MOCK_GO_FAIL=command)
                self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
