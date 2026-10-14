import json
from pathlib import Path
import unittest

import yaml


ROOT = Path(__file__).parents[2]


class NightlyWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.nightly = yaml.safe_load((ROOT / ".github/workflows/nightly.yml").read_text())
        self.matrix = yaml.safe_load((ROOT / ".github/workflows/ci-speed-proof.yml").read_text())
        self.manifest = json.loads((ROOT / ".github/ci-verification.json").read_text())

    def test_nightly_runs_the_canary_daily_and_the_full_matrix_weekly(self):
        self.assertEqual(set(self.nightly["jobs"]), {"plan", "canary", "full"})
        self.assertEqual(self.nightly["permissions"], {"contents": "read", "actions": "read"})
        canary = self.nightly["jobs"]["canary"]
        self.assertEqual((canary["timeout-minutes"], canary["continue-on-error"]), (60, True))
        self.assertEqual({key: canary["env"][key] for key in ("HAPTIC_E2E_PROFILE", "HAPTIC_E2E_GWAPI_CHANNEL")},
                         {"HAPTIC_E2E_PROFILE": "conformance", "HAPTIC_E2E_GWAPI_CHANNEL": "experimental"})
        full = self.nightly["jobs"]["full"]
        self.assertEqual(full["if"], "needs.plan.outputs.mode == 'weekly'")
        self.assertEqual(full["uses"], "./.github/workflows/ci-speed-proof.yml")
        self.assertEqual(full["with"], {"verification_job_prefix": "full / "})

    def test_isolated_suites_gate_post_merge_runs_but_not_pull_requests(self):
        expected = {
            "gateway-compatibility-v1.1.0": (25, {"TEST_RUN_PATTERN": "TestGatewayAPIReleaseMatrix",
                                                   "HAPTIC_E2E_GWAPI_VERSION": "v1.1.0"}),
            "gateway-compatibility-v1.4.0": (25, {"TEST_RUN_PATTERN": "TestGatewayAPIReleaseMatrix",
                                                   "HAPTIC_E2E_GWAPI_VERSION": "v1.4.0"}),
            "gateway-compatibility-v1.5.1": (25, {"TEST_RUN_PATTERN": "TestGatewayAPIReleaseMatrix",
                                                   "HAPTIC_E2E_GWAPI_VERSION": "v1.5.1"}),
            "gateway-churn": (30, {"TEST_RUN_PATTERN": "TestGatewayChurn", "HAPTIC_E2E_CHURN": "1"}),
            "scale": (45, {"TEST_RUN_PATTERN": "TestScale", "HAPTIC_E2E_SCALE": "1", "HAPTIC_E2E_AGENT_DEBUG": "1"}),
        }
        self.assertEqual(self.manifest["pre_merge_skipped_jobs"], list(expected))
        jobs = {job["name"]: job for job in self.matrix["jobs"].values()}
        for name, (timeout, env) in expected.items():
            with self.subTest(job=name):
                job = jobs[name]
                self.assertEqual(job["if"], "${{ !cancelled() && inputs.verification_profile == 'full' }}")
                self.assertEqual((job["timeout-minutes"], job["runs-on"]), (timeout, "ubuntu-24.04-arm"))
                self.assertEqual({key: job["env"][key] for key in env}, env)
                self.assertNotIn("continue-on-error", job)
        scale = next(step for step in jobs["scale"]["steps"] if step.get("name") == "Compare scale history")
        self.assertEqual(scale["env"]["SCALE_JOB_NAME"], "${{ inputs.verification_job_prefix }}scale")
        others = [job["if"] for name, job in jobs.items() if name not in expected and "if" in job]
        self.assertFalse([condition for condition in others if "== 'full'" in condition])


if __name__ == "__main__":
    unittest.main()
