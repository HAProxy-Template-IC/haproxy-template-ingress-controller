import copy
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock

import yaml

from scripts.ci.github_toolchain import resolve, selection


ROOT = Path(__file__).parents[2]


class ToolchainSelectionTests(unittest.TestCase):
    def setUp(self):
        self.inventory = json.loads((ROOT / ".github/ci-toolchains.json").read_text())
        self.reference = self.inventory["bootstrap"]
        self.current = self.inventory["images"][self.reference]["build_input_hash"]
        self.new = "a" * 64
        self.docs = next(ref for ref, item in self.inventory["images"].items() if item["kind"] == "docs")

    @staticmethod
    def image(build_hash):
        return json.dumps([{"Id": "sha256:" + "b" * 64,
                            "Config": {"Labels": {"haptic.ci.build-input-hash": build_hash}}}])

    def test_unchanged_inputs_reuse_the_pinned_image_without_a_build(self):
        run = Mock()
        output = Mock(side_effect=[self.current + "\n", self.image(self.current)])
        result = resolve(self.inventory, self.reference, run=run, output=output)
        self.assertFalse(result["rebuild"])
        self.assertEqual(result["image"], self.reference)
        self.assertEqual(run.call_count, 1)
        self.assertEqual(run.call_args.args[0], ["bash", "scripts/retry.sh", "docker", "pull", self.reference])

    def test_changed_inputs_build_and_use_each_matching_haproxy_toolchain(self):
        for reference, entry in self.inventory["images"].items():
            if entry["kind"] != "ci":
                continue
            run = Mock()
            output = Mock(side_effect=[self.new, self.image(self.new)])
            with self.subTest(version=entry["haproxy"]):
                result = resolve(self.inventory, reference, run=run, output=output)
                self.assertTrue(result["rebuild"])
                self.assertEqual(result["image"], "haptic-ci:" + self.new + "-hp" + entry["haproxy"])
                build = run.call_args.args[0]
                self.assertEqual(build[-2:], ["build-ci-image", "HAPROXY_VERSION=" + entry["haproxy"]])
                self.assertIn(self.inventory["bootstrap"], build)
                self.assertTrue(run.call_args.kwargs["check"])
                output.assert_called_with(["docker", "image", "inspect", result["image"]], text=True)

    def test_docs_legacy_cache_requires_exact_inputs_and_changes_get_a_labeled_build(self):
        for current, label, rebuild in ((self.current, None, False), (self.new, self.new, True)):
            run = Mock()
            result = resolve(self.inventory, self.docs, run=run, output=Mock(side_effect=[current, self.image(label)]))
            self.assertEqual(result["rebuild"], rebuild)
            if rebuild:
                self.assertEqual(result["image"], "haptic-docs:" + self.new)
                self.assertEqual(run.call_args.args[0][-1], "build-docs-image")
            else:
                self.assertEqual(result["image"], self.docs)
                self.assertEqual(run.call_count, 1)

    def test_missing_or_wrong_image_labels_stop_execution(self):
        for reference, current, label in ((self.reference, self.current, None),
                                           (self.reference, self.current, self.new),
                                           (self.reference, self.new, self.current),
                                           (self.docs, self.new, None)):
            with self.subTest(reference=reference, current=current), self.assertRaises(ValueError):
                resolve(self.inventory, reference, run=Mock(), output=Mock(side_effect=[current, self.image(label)]))

    def test_unknown_images_and_invalid_inputs_are_rejected_before_pulling(self):
        for reference, build_hash in (("image:latest", self.current), (self.reference, "missing")):
            run = Mock()
            with self.assertRaises(ValueError):
                resolve(self.inventory, reference, run=run, output=Mock(return_value=build_hash))
            run.assert_not_called()
        inventory = copy.deepcopy(self.inventory)
        inventory["images"][self.reference]["haproxy"] = "3.4; echo invalid"
        with self.assertRaises(ValueError):
            selection(inventory, self.reference, self.new)

    def test_failed_builds_are_not_replaced_by_the_old_toolchain(self):
        run = Mock(side_effect=[None, subprocess.CalledProcessError(1, "build")])
        output = Mock(return_value=self.new)
        with self.assertRaises(subprocess.CalledProcessError):
            resolve(self.inventory, self.reference, run=run, output=output)
        self.assertEqual(output.call_count, 1)

    def test_canonical_container_jobs_all_have_recorded_build_inputs(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/ci-speed-proof.yml").read_text())
        for job in workflow["jobs"].values():
            reference = job.get("env", {}).get("PILOT_CI_IMAGE")
            if reference:
                self.assertIn(reference, self.inventory["images"])
        for name in ("run-build-pilot.sh", "run-remaining-pilot.sh", "run-conformance-report.sh",
                     "run-release-publication.sh", "run-snapshot-publication.sh"):
            self.assertIn("python3 -m scripts.ci.github_toolchain", (ROOT / ".github/scripts" / name).read_text())
        for name in ("release-tag.yml", "prepare-spoa-release.yml"):
            self.assertIn("python3 -m scripts.ci.github_toolchain", (ROOT / ".github/workflows" / name).read_text())


if __name__ == "__main__":
    unittest.main()
