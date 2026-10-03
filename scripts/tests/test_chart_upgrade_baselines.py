import importlib.util
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]
SPEC = importlib.util.spec_from_file_location("chart_upgrade_baselines", ROOT / "scripts/chart-upgrade-baselines.py")
baselines_module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(baselines_module)
baselines = baselines_module.baselines

TAGS = ["0.1.0", "0.2.0-alpha.3", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.3.0-rc.1", "0.3.0", "main-abc123"]


class ChartUpgradeBaselinesTests(unittest.TestCase):
    def test_maintenance_branch_never_upgrades_from_a_newer_line(self):
        self.assertEqual(baselines(TAGS, "0.2.2"), ["0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.0-alpha.3"])

    def test_unreleased_patch_upgrades_from_every_lower_release(self):
        self.assertEqual(baselines(TAGS, "0.2.3"), ["0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.0-alpha.3"])

    def test_versions_compare_numerically(self):
        self.assertEqual(baselines(TAGS, "0.2.10"),
                         ["0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.2.0-alpha.3"])

    def test_prerelease_under_test_excludes_its_own_stable_release(self):
        self.assertEqual(baselines(TAGS, "0.3.0-alpha.1"),
                         ["0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.2.0-alpha.3"])

    def test_newest_line_upgrades_from_every_release(self):
        self.assertEqual(baselines(TAGS, "0.3.0"),
                         ["0.1.0", "0.2.0", "0.2.1", "0.2.2", "0.2.10", "0.3.0", "0.2.0-alpha.3"])

    def test_missing_required_baseline_fails(self):
        with self.assertRaisesRegex(ValueError, "0.2.0-alpha.3"):
            baselines(["0.1.0", "0.2.0"], "0.2.2")

    def test_no_stable_release_fails(self):
        with self.assertRaisesRegex(ValueError, "No published stable"):
            baselines(["0.2.0-alpha.3"], "0.2.2")

    def test_non_semver_version_under_test_fails(self):
        with self.assertRaisesRegex(ValueError, "not semver"):
            baselines(TAGS, "dev")


if __name__ == "__main__":
    unittest.main()
