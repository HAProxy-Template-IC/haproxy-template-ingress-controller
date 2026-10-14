import copy
from pathlib import Path
import unittest

import yaml

from scripts.ci.github_release import configuration


ROOT = Path(__file__).parents[2]


class GitHubReleaseTests(unittest.TestCase):
    def setUp(self):
        self.source = yaml.safe_load((ROOT / ".goreleaser.yml").read_text())

    def test_publishes_every_image_to_the_repository_owner(self):
        original = copy.deepcopy(self.source)
        result = configuration(self.source, "haproxy-haptic/HAPTIC")
        self.assertEqual(self.source, original)
        self.assertEqual(result, original)
        for image in result["dockers_v2"]:
            self.assertTrue(all(target.startswith("ghcr.io/haproxy-haptic/haptic")
                                for target in image["images"]))

    def test_rejects_another_repository(self):
        with self.assertRaisesRegex(ValueError, "another repository"):
            configuration(self.source, "owner/repo")

    def test_rejects_invalid_destination(self):
        for value in ("owner/repo/extra", "owner", "owner/repo\n", "../repo", "owner/repo name"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                configuration(self.source, value)

    def test_snapshot_binary_version_matches_chart_app_version(self):
        result = configuration(self.source, "haproxy-haptic/HAPTIC", "01234567" + "8" * 32)
        self.assertEqual(result["snapshot"]["version_template"], "main-01234567")
        for sha in ("01234567", "g" * 40, "a" * 40 + "\n"):
            with self.subTest(sha=sha), self.assertRaises(ValueError):
                configuration(self.source, "haproxy-haptic/HAPTIC", sha)


if __name__ == "__main__":
    unittest.main()
