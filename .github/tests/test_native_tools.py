import copy
import json
from pathlib import Path
import unittest
from unittest.mock import Mock

import yaml

from scripts.ci.github_native_tools import plan, verify


ROOT = Path(__file__).parents[2]


class NativeToolTests(unittest.TestCase):
    def setUp(self):
        self.pins = yaml.safe_load((ROOT / "ci/build-pins.yml").read_text())["variables"]
        self.checksums = json.loads((ROOT / ".github/native-tool-checksums.json").read_text())
        self.version = self.pins["GO_VERSION"] + ".2"
        self.images = {"linux/arm64": {"config": {"Env": ["GOLANG_VERSION=" + self.version]}}}

    def test_current_shared_pins_have_checksums_and_determine_every_tool_version(self):
        selected = plan(self.pins, self.checksums, self.images)
        self.assertEqual(selected["go_version"], self.version)
        self.assertEqual(len(self.checksums["yq"][self.checksums["bootstrap_yq_version"]]), 64)
        for tool in selected["tools"]:
            self.assertEqual(tool["version"], self.pins[tool["name"].upper() + "_VERSION"])
            self.assertIn(tool["version"], tool["url"])
            self.assertEqual(len(tool["sha256"]), 64)

    def test_updates_select_the_new_tools_and_require_their_actual_checksums(self):
        self.pins["KIND_VERSION"] = "99.0.0"
        with self.assertRaisesRegex(ValueError, "Add the ARM64 kind 99.0.0 checksum"):
            plan(self.pins, self.checksums, self.images)
        self.checksums["kind"]["99.0.0"] = "b" * 64
        self.images["linux/arm64"]["config"]["Env"] = ["GOLANG_VERSION=" + self.pins["GO_VERSION"] + ".3"]
        selected = plan(self.pins, self.checksums, self.images)
        self.assertEqual(selected["go_version"], self.pins["GO_VERSION"] + ".3")
        self.assertEqual(selected["tools"][0]["version"], "99.0.0")
        self.assertIn("v99.0.0/", selected["tools"][0]["url"])

    def test_floating_images_mismatched_go_and_invalid_checksums_are_rejected(self):
        original = copy.deepcopy(self.images)
        for environment in ([], ["GOLANG_VERSION=99.0.0"], ["GOLANG_VERSION=latest"],
                            ["GOLANG_VERSION=1.27.2", "GOLANG_VERSION=1.27.3"]):
            self.images["linux/arm64"]["config"]["Env"] = environment
            with self.subTest(environment=environment), self.assertRaises(ValueError):
                plan(self.pins, self.checksums, self.images)
        self.images = original
        self.pins["GO_BUILD_IMAGE"] = "golang:1.27"
        with self.assertRaisesRegex(ValueError, "immutable"):
            plan(self.pins, self.checksums, self.images)

    def test_installed_tools_must_match_the_selected_versions(self):
        selected = plan(self.pins, self.checksums, self.images)
        outputs = ["go" + self.version, "kind v" + self.pins["KIND_VERSION"], "v" + self.pins["HELM_VERSION"],
                   json.dumps({"clientVersion": {"gitVersion": "v" + self.pins["KUBECTL_VERSION"]}}),
                   "yq version v" + self.pins["YQ_VERSION"],
                   json.dumps({"gitVersion": "v" + self.pins["COSIGN_VERSION"]})]
        self.assertEqual(verify(selected, output=Mock(side_effect=outputs))["go"], self.version)
        outputs[0] = "go99.0.0\n"
        with self.assertRaisesRegex(ValueError, "differ"):
            verify(selected, output=Mock(side_effect=outputs))

    def test_workflows_resolve_shared_pins_before_installing_the_compiler(self):
        for path in (ROOT / ".github/workflows").glob("*.yml"):
            if "install-arm-pilot-tools.sh" not in path.read_text():
                continue
            workflow = yaml.safe_load(path.read_text())
            for name, job in workflow["jobs"].items():
                steps = job.get("steps", [])
                installs = [i for i, step in enumerate(steps) if step.get("run") == "bash .github/scripts/install-arm-pilot-tools.sh"]
                if not installs:
                    continue
                with self.subTest(workflow=path.name, job=name):
                    compilers = [i for i, step in enumerate(steps) if step.get("uses", "").startswith("actions/setup-go@")]
                    self.assertEqual(len(compilers), 1)
                    self.assertLess(installs[0], compilers[0])
                    self.assertEqual(steps[compilers[0]]["with"]["go-version"], "${{ env.HAPTIC_NATIVE_GO_VERSION }}")


if __name__ == "__main__":
    unittest.main()
