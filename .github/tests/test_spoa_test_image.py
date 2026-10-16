from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).parents[2]
SELECT = "bash .github/scripts/spoa-test-image.sh select\n"
BUILD = "bash .github/scripts/spoa-test-image.sh build > .pilot/spoa-build.log 2>&1 &\n"
WAIT = 'wait "$spoa_build_pid" || { cat .pilot/spoa-build.log; exit 1; }\n'


class SPOATestImageTests(unittest.TestCase):
    def test_cluster_suites_deploy_the_hub_built_from_the_checkout(self):
        for script, controller_build, suite in (
            ("run-arm-pilot.sh", 'make "$build_target"', 'make "$test_target"'),
            ("run-lifecycle-arm.sh", 'make "$build_target"', 'case "$PILOT_SUITE" in'),
            ("run-remaining-suite.sh", "make docker-build-test\n", "acceptance) make test-acceptance-parallel"),
        ):
            with self.subTest(script=script):
                text = (ROOT / ".github/scripts" / script).read_text()
                for step in (SELECT, BUILD, WAIT):
                    self.assertEqual(text.count(step), 1, step)
                self.assertLess(text.index(SELECT), text.index(BUILD))
                self.assertLess(text.index(BUILD), text.index(controller_build))
                self.assertLess(text.index(controller_build), text.index(WAIT))
                self.assertLess(text.index(WAIT), text.rindex(suite))

    def test_selection_points_every_hub_container_at_the_local_image(self):
        with tempfile.TemporaryDirectory() as temp:
            work = Path(temp)
            shutil.copytree(ROOT / "charts/haptic", work / "charts/haptic")
            subprocess.run(["bash", str(ROOT / ".github/scripts/spoa-test-image.sh"), "select"],
                           cwd=work, check=True)
            image = subprocess.run(["bash", str(ROOT / "scripts/chart-spoa-image.sh"), str(work / "charts/haptic")],
                                   check=True, capture_output=True, text=True).stdout.strip()
        self.assertEqual(image, "haptic-spoa:test")


if __name__ == "__main__":
    unittest.main()
