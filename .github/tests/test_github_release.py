import copy
from pathlib import Path
import unittest

import yaml

from scripts.ci.github_release import SOURCE_REGISTRY, configuration


ROOT = Path(__file__).parents[2]


class GitHubReleaseTests(unittest.TestCase):
    def setUp(self):
        self.source = yaml.safe_load((ROOT / ".goreleaser.yml").read_text())

    def test_preserves_builds_platforms_signatures_and_release_behavior(self):
        original = copy.deepcopy(self.source)
        result = configuration(self.source, "HAProxy-Template-IC/haproxy-template-ingress-controller")
        self.assertEqual(self.source, original)
        self.assertNotIn("gitlab_urls", result)
        self.assertEqual(result["release"]["github"], {
            "owner": "HAProxy-Template-IC", "name": "haproxy-template-ingress-controller"})
        for image in result["dockers_v2"]:
            self.assertTrue(all(target.startswith("ghcr.io/haproxy-template-ic/haptic")
                                for target in image["images"]))
            image["images"] = [target.replace("ghcr.io/haproxy-template-ic/haptic", SOURCE_REGISTRY, 1)
                               for target in image["images"]]
        result["release"].pop("github")
        result["release"]["gitlab"] = original["release"]["gitlab"]
        result["gitlab_urls"] = original["gitlab_urls"]
        result["snapshot"] = original["snapshot"]
        self.assertEqual(result, original)

    def test_rejects_unmapped_registry(self):
        self.source["dockers_v2"][0]["images"] = [SOURCE_REGISTRY + "-unrelated"]
        with self.assertRaisesRegex(ValueError, "Unmapped"):
            configuration(self.source, "owner/repo")

    def test_rejects_changed_source_repository(self):
        self.source["release"]["gitlab"]["name"] = "different"
        with self.assertRaisesRegex(ValueError, "source repository changed"):
            configuration(self.source, "owner/repo")

    def test_rejects_invalid_destination(self):
        for value in ("owner/repo/extra", "owner", "owner/repo\n", "../repo", "owner/repo name"):
            with self.subTest(value=value), self.assertRaises(ValueError):
                configuration(self.source, value)


if __name__ == "__main__":
    unittest.main()
