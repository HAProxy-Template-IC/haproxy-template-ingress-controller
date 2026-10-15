import copy
import hashlib
import json
from pathlib import Path
import unittest
from unittest.mock import patch

import yaml

from scripts.ci.github_snapshot import check_index, snapshot_plan, verify_image


class SnapshotPublicationTests(unittest.TestCase):
    def setUp(self):
        self.sha = "01234567" + "8" * 32
        self.manifest = b"verification manifest"
        self.receipt = {"version": 2, "repository": "owner/repo", "sha": self.sha, "tree": "tree",
                        "branch": "main", "source_sha": self.sha, "source_repository": "owner/repo",
                        "manifest_sha256": hashlib.sha256(self.manifest).hexdigest()}
        self.values = {"controller": {"image": {"repository": "ghcr.io/owner/haptic", "tag": ""}},
                       "spoaHub": {"image": {"repository": "ghcr.io/owner/haptic/spoa-hub", "tag": ""}}}
        self.platforms = ["linux/amd64", "linux/arm64", "linux/arm/v7"]
        self.index = {"manifests": [
            {"platform": {"os": "linux", "architecture": "amd64"}, "digest": "sha256:" + "1" * 64},
            {"platform": {"os": "linux", "architecture": "arm64"}, "digest": "sha256:" + "2" * 64},
            {"platform": {"os": "linux", "architecture": "arm", "variant": "v7"},
             "digest": "sha256:" + "3" * 64}]}

    def plan(self):
        return snapshot_plan(self.receipt, self.values, repository="owner/repo", sha=self.sha,
                             tree="tree", manifest=self.manifest)

    def test_requires_the_exact_verified_main_source(self):
        result = self.plan()
        self.assertEqual(result["chart_version"], "0.0.0-main.g01234567")
        self.assertEqual(result["app_version"], "main-01234567")
        for key in self.receipt:
            original = self.receipt[key]
            self.receipt[key] = "different"
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.plan()
            self.receipt[key] = original

    def test_rejects_old_registries_and_image_tag_overrides(self):
        for component in self.values:
            for key, value in (("repository", "registry.gitlab.com/haproxy-haptic/haptic"),
                               ("tag", "old-snapshot")):
                original = self.values[component]["image"][key]
                self.values[component]["image"][key] = value
                with self.subTest(component=component, key=key), self.assertRaises(ValueError):
                    self.plan()
                self.values[component]["image"][key] = original

    def test_documentation_only_verification_cannot_publish_runtime_images(self):
        self.receipt["profile"] = "prose"
        with self.assertRaises(ValueError):
            self.plan()

    def test_only_direct_receipts_are_accepted(self):
        for version in (3, 4):
            self.receipt["version"] = version
            with self.subTest(version=version), self.assertRaises(ValueError):
                self.plan()

    def test_rejects_incomplete_duplicate_or_extra_platforms(self):
        self.assertEqual(set(check_index(self.index, self.platforms)), set(self.platforms))
        for entries in (self.index["manifests"][:2], self.index["manifests"] * 2,
                        self.index["manifests"] + [{"platform": {"os": "windows", "architecture": "amd64"},
                                                  "digest": "sha256:" + "4" * 64}]):
            with self.subTest(entries=entries), self.assertRaises(ValueError):
                check_index({"manifests": entries}, self.platforms)
        self.index["manifests"][0]["digest"] = "mutable-tag"
        with self.assertRaises(ValueError):
            check_index(self.index, self.platforms)

    def test_verifies_labels_for_every_architecture_by_digest(self):
        config = {"config": {"Labels": {"org.opencontainers.image.revision": self.sha,
                                         "org.opencontainers.image.version": "main-01234567"}}}
        replies = [json.dumps("sha256:" + "a" * 64), json.dumps(self.index)] + [json.dumps(config)] * 3
        with patch("subprocess.check_output", side_effect=replies) as inspect:
            result = verify_image("ghcr.io/owner/haptic:main-01234567", self.platforms, self.sha, "main-01234567")
        self.assertEqual(result["reference"], "ghcr.io/owner/haptic@sha256:" + "a" * 64)
        self.assertTrue(all("@sha256:" in call.args[0][4] for call in inspect.call_args_list[1:]))
        for index in range(3):
            for key in config["config"]["Labels"]:
                changed = copy.deepcopy(config)
                changed["config"]["Labels"][key] = "wrong"
                invalid = replies.copy()
                invalid[index + 2] = json.dumps(changed)
                with self.subTest(architecture=index, label=key), patch("subprocess.check_output", side_effect=invalid):
                    with self.assertRaises(ValueError):
                        verify_image("ghcr.io/owner/haptic:main-01234567", self.platforms, self.sha, "main-01234567")

    def test_chart_publication_requires_images_and_native_smoke_on_both_architectures(self):
        workflow = yaml.safe_load((Path(__file__).parents[1] / "workflows/snapshot.yml").read_text())
        jobs = workflow["jobs"]
        self.assertEqual(set(jobs["chart"]["needs"]), {"source", "images", "smoke"})
        self.assertEqual(set(jobs["smoke"]["strategy"]["matrix"]["runner"]),
                         {"ubuntu-24.04", "ubuntu-24.04-arm"})
        self.assertEqual(set(jobs["images"]["strategy"]["matrix"]["phase"]), {"controller", "spoa"})
        self.assertEqual(workflow["on"]["workflow_run"], {"workflows": ["CI"], "types": ["completed"]})
        self.assertIn("workflow_dispatch", workflow["on"])
        gate = jobs["source"]["if"]
        for condition in ("head_repository.full_name == github.repository", "conclusion == 'success'", "event == 'push'", "head_branch == 'main'"):
            self.assertIn(condition, gate)
        self.assertEqual(jobs["images"]["if"], "needs.source.outputs.publish == 'true'")
        self.assertIn("profile == 'full'", jobs["source"]["outputs"]["publish"])
        self.assertEqual(jobs["prune"]["needs"], "chart")


if __name__ == "__main__":
    unittest.main()
