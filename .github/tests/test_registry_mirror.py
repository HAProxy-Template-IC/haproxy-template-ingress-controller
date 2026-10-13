import json
from pathlib import Path
import re
import unittest

from scripts.ci.github_mirror import copy_images, plan


ROOT = Path(__file__).parents[2]


class RegistryMirrorTests(unittest.TestCase):
    def setUp(self):
        self.sources = json.loads((ROOT / ".github/ci-registry-mirror.json").read_text())["sources"]

    def test_inventory_covers_every_pinned_ci_toolchain_and_varnish_image(self):
        workflow = (ROOT / ".github/workflows/ci-speed-proof.yml").read_text()
        references = set(re.findall(r"registry\.gitlab\.com/haproxy-haptic/haptic/[a-z/-]+@sha256:[a-f0-9]{64}", workflow))
        self.assertEqual(set(self.sources), references)
        self.assertEqual(len(plan(self.sources, "Owner/repo")), 7)

    def test_mapping_preserves_every_digest_and_uses_immutable_tags(self):
        for image in plan(self.sources, "Owner/repo"):
            self.assertTrue(image["reference"].startswith("ghcr.io/owner/haptic-ci@sha256:"))
            self.assertEqual(image["source"].split("@")[1], image["digest"])
            self.assertEqual(image["destination"].split(":")[-1], image["digest"].replace(":", "-"))

    def test_rejects_unpinned_foreign_empty_or_duplicate_sources(self):
        for sources in ([], [self.sources[0]] * 2, ["registry.gitlab.com/haproxy-haptic/haptic/ci:latest"],
                        [self.sources[0].replace("haproxy-haptic", "other")], [self.sources[0] + "\n"]):
            with self.subTest(sources=sources), self.assertRaises(ValueError):
                plan(sources, "Owner/repo")

    def test_copy_requires_destination_digest_to_match_and_never_clobbers(self):
        images = plan(self.sources[:1], "Owner/repo")
        commands = []

        def execute(command):
            commands.append(command)
            return images[0]["digest"] if command[:2] == ["crane", "digest"] else ""

        copy_images(images, execute)
        self.assertIn("--no-clobber", commands[0])
        self.assertNotIn("--platform", commands[0])
        with self.assertRaisesRegex(ValueError, "differs"):
            copy_images(images, lambda command: "sha256:" + "0" * 64)


if __name__ == "__main__":
    unittest.main()
