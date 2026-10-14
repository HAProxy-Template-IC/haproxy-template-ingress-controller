from pathlib import Path
import unittest

import yaml



ROOT = Path(__file__).parents[2]


class NightlyWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.github = yaml.safe_load((ROOT / ".github/workflows/nightly.yml").read_text())

    def test_nightly_preserves_test_selection_timeouts_and_failure_policy(self):
        expected = {
            "compatibility": (25, False, {"TEST_RUN_PATTERN": "TestGatewayAPIReleaseMatrix"}),
            "churn": (30, False, {"TEST_RUN_PATTERN": "TestGatewayChurn", "HAPTIC_E2E_CHURN": "1"}),
            "scale": (45, False, {"TEST_RUN_PATTERN": "TestScale", "HAPTIC_E2E_SCALE": "1",
                                  "HAPTIC_E2E_AGENT_DEBUG": "1"}),
            "canary": (60, True, {"HAPTIC_E2E_PROFILE": "conformance", "HAPTIC_E2E_GWAPI_CHANNEL": "experimental"}),
        }
        keys = ("TEST_RUN_PATTERN", "HAPTIC_E2E_CHURN", "HAPTIC_E2E_SCALE", "HAPTIC_E2E_AGENT_DEBUG",
                "HAPTIC_E2E_PROFILE", "HAPTIC_E2E_GWAPI_CHANNEL")
        for name, (timeout, allowed_failure, env) in expected.items():
            candidate = self.github["jobs"][name]
            with self.subTest(job=name):
                self.assertEqual(candidate["timeout-minutes"], timeout)
                self.assertEqual(candidate.get("continue-on-error", False), allowed_failure)
                self.assertEqual({key: candidate["env"][key] for key in keys if key in candidate["env"]}, env)
                self.assertEqual(candidate["env"]["HAPROXY_VERSION"], "3.4")
        self.assertEqual(self.github["jobs"]["compatibility"]["strategy"]["matrix"]["release"],
                         ["v1.1.0", "v1.4.0", "v1.5.1"])

    def test_weekly_reuses_full_verification_without_publication_permissions(self):
        self.assertEqual(self.github["permissions"], {"contents": "read", "actions": "read"})
        full = self.github["jobs"]["full"]
        self.assertEqual(full["uses"], "./.github/workflows/ci-speed-proof.yml")
        self.assertEqual(full["with"]["verification_job_prefix"], "full / ")
        scale = self.github["jobs"]["scale"]
        self.assertEqual({key for key in scale["env"] if key.startswith("SCALE_")}, {"SCALE_SEED_WORKERS"})
        for name in ("churn", "scale"):
            commands = [step.get("run", "") for step in self.github["jobs"][name]["steps"]]
            self.assertIn("KUBECONFIG=/tmp/haproxy-e2e-kubeconfig make check-controller-output", commands)
        self.assertIn("python3 -m scripts.ci.github_scale", [step.get("run", "") for step in scale["steps"]])

    def test_pr_integration_preserves_all_six_isolated_suites_and_native_cache(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/ci-speed-proof.yml").read_text())
        integrated = {job["name"]: job for job in workflow["jobs"].values()}
        cases = [("gateway-compatibility-" + release, "compatibility", release)
                 for release in self.github["jobs"]["compatibility"]["strategy"]["matrix"]["release"]]
        cases += [("gateway-churn", "churn", None), ("scale", "scale", None), ("gateway-canary", "canary", None)]
        for name, source, release in cases:
            with self.subTest(name=name):
                baseline = self.github["jobs"][source]
                candidate = integrated[name]
                self.assertEqual(candidate["timeout-minutes"], baseline["timeout-minutes"])
                self.assertEqual(candidate.get("continue-on-error"), baseline.get("continue-on-error"))
                self.assertEqual(candidate["runs-on"], "ubuntu-24.04-arm")
                for key, value in baseline["env"].items():
                    self.assertEqual(candidate["env"][key], release if key == "HAPTIC_E2E_GWAPI_VERSION" else value)
                self.assertEqual([step["run"] for step in candidate["steps"] if "run" in step],
                                 [step["run"] for step in baseline["steps"] if "run" in step])
                native = next(step for step in candidate["steps"] if step.get("uses", "").startswith("actions/setup-go@"))
                self.assertEqual(native["with"]["cache-dependency-path"].split(),
                                 ["go.mod", "go.sum", "cmd/haptic/default.pgo", ".pilot/compiler-cache-variant"])
                self.assertNotIn("weekly", baseline["if"])
        scale = next(step for step in integrated["scale"]["steps"] if step.get("name") == "Compare scale history")
        self.assertEqual(scale["env"]["SCALE_JOB_NAME"], "${{ inputs.verification_job_prefix }}scale")


if __name__ == "__main__":
    unittest.main()
