import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest

import yaml


ROOT = Path(__file__).parents[2]
SELECT = "bash .github/scripts/spoa-test-image.sh select\n"
BUILD = "setsid bash .github/scripts/spoa-test-image.sh build > .pilot/spoa-build.log 2>&1 &\n"
WAIT = 'wait "$spoa_build_pid" || exit 1\n'
# An early exit kills the build's whole process group, so buildx doesn't outlive the job, and prints its log.
REAP = '-- "-$spoa_build_pid"'


class SPOATestImageTests(unittest.TestCase):
    def test_cluster_suites_deploy_the_hub_built_from_the_checkout(self):
        for script, controller_build, suite in (
            ("run-arm-pilot.sh", 'make "$build_target"', 'make "$test_target"'),
            ("run-lifecycle-arm.sh", 'make "$build_target"', 'case "$PILOT_SUITE" in'),
            ("run-remaining-suite.sh", "make docker-build-test\n", "acceptance) make test-acceptance-parallel"),
        ):
            with self.subTest(script=script):
                text = (ROOT / ".github/scripts" / script).read_text()
                for step in (SELECT, BUILD, WAIT, REAP):
                    self.assertEqual(text.count(step), 1, step)
                self.assertLess(text.index("trap "), text.index(controller_build))
                self.assertLess(text.index(SELECT), text.index(BUILD))
                self.assertLess(text.index(BUILD), text.index(controller_build))
                self.assertLess(text.index(controller_build), text.index(WAIT))
                self.assertLess(text.index(WAIT), text.rindex(suite))

    def test_acceptance_skips_the_hub_it_disables(self):
        text = (ROOT / ".github/scripts/run-remaining-suite.sh").read_text()
        self.assertLess(text.index('if [[ "$PILOT_SUITE" != acceptance ]]; then'), text.index(SELECT))

    def test_every_hub_build_restores_plugin_downloads_and_one_job_saves_them(self):
        remaining = (ROOT / ".github/scripts/run-remaining-suite.sh").read_text()
        no_hub = {label for labels in re.findall(r"^    ([a-z|-]+)\)", remaining.split("esac")[0], re.M)
                  for label in labels.split("|")} | {"acceptance"}
        hub_builders = {"run-arm-pilot.sh": lambda suite: True, "run-lifecycle-arm.sh": lambda suite: True,
                        "run-remaining-pilot.sh": lambda suite: suite["suite"] not in no_hub,
                        "run-build-pilot.sh": lambda suite: suite["suite"] == "spoa"}
        savers = []
        for name, job in yaml.safe_load((ROOT / ".github/workflows/ci-speed-proof.yml").read_text())["jobs"].items():
            if "CI_SUITE" not in job.get("env", {}):
                continue
            suite = json.loads(job["env"]["CI_SUITE"])
            active = [step for step in job["steps"] if "if" not in step or self.evaluate(step["if"], suite)]
            caches = [index for index, step in enumerate(active)
                      if step.get("with", {}).get("path") == ".cache/spoa-plugins"]
            builds = [index for index, step in enumerate(active) for script, builds_hub in hub_builders.items()
                      if f".github/scripts/{script}" in step.get("run", "") and builds_hub(suite)]
            with self.subTest(job=name):
                self.assertEqual(len(caches), 1 if builds else 0)
                if builds:
                    self.assertLess(caches[0], builds[0])
                    cache = active[caches[0]]
                    self.assertEqual(cache["with"]["key"],
                                     "spoa-plugins-${{ hashFiles('versions-spoa.env', 'scripts/prep-spoa-plugins.sh') }}")
                    self.assertEqual(cache["with"]["restore-keys"], "spoa-plugins-")
                    if not cache["uses"].startswith("actions/cache/restore@"):
                        savers.append(suite["suite"])
        self.assertEqual(savers, ["spoa"])

    @staticmethod
    def evaluate(condition, suite):
        python = re.sub(r"fromJSON\(env\.CI_SUITE\)\.(\w+)", r"suite['\1']", condition)
        python = re.sub(r"contains\(fromJSON\('(\[.*?\])'\), (.*?)\)", r"(\2 in \1)", python)
        python = python.replace("||", " or ").replace("&&", " and ").replace("always()", "True")
        return eval(python, {}, {"suite": {key: str(value) for key, value in suite.items()}})

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
