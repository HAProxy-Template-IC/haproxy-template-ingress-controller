from pathlib import Path
import unittest

import yaml

from scripts.ci.configuration import job, load


ROOT = Path(__file__).parents[2]


class NightlyWorkflowTests(unittest.TestCase):
    def setUp(self):
        self.gitlab = load(ROOT / ".gitlab-ci.yml")
        self.github = yaml.safe_load((ROOT / ".github/workflows/nightly.yml").read_text())

    def test_nightly_preserves_test_selection_timeouts_and_failure_policy(self):
        mapping = {"compatibility": "nightly-gwapi-matrix", "churn": "nightly-gateway-churn",
                   "scale": "nightly-scale", "canary": "nightly-gwapi-canary"}
        for github_name, gitlab_name in mapping.items():
            baseline = job(self.gitlab, gitlab_name)
            candidate = self.github["jobs"][github_name]
            with self.subTest(job=gitlab_name):
                self.assertEqual(candidate["timeout-minutes"], int(baseline["timeout"].split()[0]))
                self.assertEqual(candidate.get("continue-on-error", False), baseline.get("allow_failure", False))
                for key in ("TEST_RUN_PATTERN", "HAPTIC_E2E_CHURN", "HAPTIC_E2E_SCALE", "HAPTIC_E2E_AGENT_DEBUG",
                            "HAPTIC_E2E_PROFILE", "HAPTIC_E2E_GWAPI_CHANNEL", "HAPROXY_VERSION"):
                    self.assertEqual(candidate["env"].get(key), baseline["variables"].get(key))
        releases = self.gitlab["nightly-gwapi-matrix"]["parallel"]["matrix"][0]["GWAPI_RELEASE"]
        self.assertEqual(self.github["jobs"]["compatibility"]["strategy"]["matrix"]["release"], releases)

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


if __name__ == "__main__":
    unittest.main()
