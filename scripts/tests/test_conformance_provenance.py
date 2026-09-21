"""Conformance evidence must identify the running binary, not just an image tag."""

import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch

SCRIPT = Path(__file__).resolve().parents[1] / "conformance-provenance.py"
SPEC = importlib.util.spec_from_file_location("conformance_provenance", SCRIPT)
PROVENANCE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROVENANCE)


class ControllerProvenanceTest(unittest.TestCase):
    def setUp(self):
        self.args = SimpleNamespace(controller_image="haptic:test", implementation_version="0.2.0", kubeconfig="/tmp/test", namespace="haptic", release="haptic")
        self.version = "Version: 0.2.0\nCommit: abcdef1\nSource Hash: 123456789abc"
        self.binary_hash = "1" * 64
        self.pod_hash = self.binary_hash
        self.pods = [{
            "metadata": {"name": "controller-1", "uid": "uid-1"},
            "status": {"containerStatuses": [{"name": "controller", "ready": True, "imageID": "sha256:running"}]},
        }]

    def run_command(self, *args):
        if args[:3] == ("docker", "image", "inspect"):
            return json.dumps({"Id": "sha256:image", "RepoDigests": ["registry/haptic@sha256:image"], "Config": {"Env": ["PASSWORD=private"]}})
        if args[0] == "docker" and args[-1] == "version":
            return self.version
        if args[0] == "docker" and "sha256sum" in args:
            return self.binary_hash + "  /usr/local/bin/haptic"
        if args[0] == "kubectl" and "get" in args:
            return json.dumps({"items": self.pods})
        if args[0] == "kubectl" and "exec" in args:
            return self.pod_hash + "  /usr/local/bin/haptic"
        raise AssertionError(args)

    def collect(self):
        with patch.object(PROVENANCE, "run", side_effect=self.run_command):
            return PROVENANCE.controller_provenance(self.args, "123456789abc")

    def test_matches_running_binary_and_excludes_image_environment(self):
        result = self.collect()
        self.assertEqual(result["running_replicas"][0]["binary_sha256"], self.binary_hash)
        self.assertNotIn("PASSWORD", json.dumps(result))
        self.assertEqual(result["registry_digests"], ["registry/haptic@sha256:image"])

    def test_rejects_version_or_source_mismatch(self):
        for field in ("0.2.0", "123456789abc"):
            original = self.version
            self.version = original.replace(field, "mismatch")
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.collect()
            self.version = original

    def test_rejects_running_binary_mismatch(self):
        self.pod_hash = "2" * 64
        with self.assertRaises(ValueError):
            self.collect()

    def test_rejects_unready_and_missing_replicas(self):
        self.pods[0]["status"]["containerStatuses"][0]["ready"] = False
        with self.assertRaises(ValueError):
            self.collect()
        self.pods = []
        with self.assertRaises(ValueError):
            self.collect()

    def test_missing_version_fields_fail(self):
        with self.assertRaises(ValueError):
            PROVENANCE.version_fields("Version: 0.2.0\nCommit: abcdef1")

    def test_source_check_includes_untracked_files(self):
        with patch.object(PROVENANCE, "run", return_value="?? pkg/uncommitted.go") as command:
            self.assertTrue(PROVENANCE.checkout_dirty())
            command.assert_called_once_with("git", "status", "--porcelain", "--untracked-files=all")

    def test_release_requires_published_image_from_the_exact_commit(self):
        controller = {"registry_digests": ["registry/haptic@sha256:image"], "commit": "a" * 40}
        PROVENANCE.validate_release_controller(controller, "a" * 40)
        with self.assertRaises(ValueError):
            PROVENANCE.validate_release_controller(controller, "b" * 40)
        controller["registry_digests"] = []
        with self.assertRaises(ValueError):
            PROVENANCE.validate_release_controller(controller, "a" * 40)


if __name__ == "__main__":
    unittest.main()
