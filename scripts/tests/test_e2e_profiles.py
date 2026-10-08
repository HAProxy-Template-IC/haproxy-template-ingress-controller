"""Focused jobs retain every profile-gated test and reject stale selections."""

from pathlib import Path
import re
import unittest

from scripts.ci.configuration import instances, job, load


ROOT = Path(__file__).resolve().parents[2]
GUARDS = {"cache": "RequireCacheProfile", "rate-limit": "RequireRateLimitProfile",
          "api-gateway": "RequireAPIGatewayProfile|requireExperimentalGatewayAPI"}


class FeatureProfileTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = load(ROOT / ".gitlab-ci.yml")
        cls.tests = {}
        for path in (ROOT / "tests/e2e").glob("*_test.go"):
            for function in re.split(r"(?m)^func ", path.read_text())[1:]:
                signature = re.match(r"(Test\w+)\(t \*testing.T\)", function)
                if signature:
                    cls.tests[signature[1]] = function

    def test_feature_jobs_are_single_required_jobs_with_their_profile(self):
        for profile in GUARDS:
            name = f"test-e2e-{profile}"
            definition = job(self.config, name)
            with self.subTest(profile=profile):
                self.assertEqual(instances(self.config, name), [name])
                self.assertEqual(definition["script"], ["make test-e2e-profile"])
                self.assertEqual(definition["variables"]["HAPTIC_E2E_PROFILE"], profile)
                self.assertFalse(definition.get("allow_failure", False))
        self.assertEqual(job(self.config, "test-e2e-api-gateway")["variables"]["HAPTIC_E2E_GWAPI_CHANNEL"], "experimental")

    def test_all_profile_gated_cases_and_interaction_checks_are_selected(self):
        for profile, guards in GUARDS.items():
            selected = (ROOT / f"tests/e2e/profiles/{profile}.txt").read_text().splitlines()
            with self.subTest(profile=profile):
                self.assertEqual(selected, sorted(set(selected)))
                self.assertFalse(set(selected) - self.tests.keys(), "selection names a nonexistent test")
                gated = {name for name, body in self.tests.items() if re.search(r"\b(" + guards + r")\(", body)}
                self.assertTrue(gated, "profile guard did not identify any tests")
                self.assertFalse(gated - set(selected), f"profile tests omitted: {gated - set(selected)}")
                for interaction in ("TestIngressBasic", "TestHapticBasicAuth", "TestHapticAPIKey",
                                    "TestHapticExternalAuth", "TestHapticWAFPolicies"):
                    self.assertIn(interaction, selected)
                self.assertNotIn("TestAgentTLSRotation", selected)


if __name__ == "__main__":
    unittest.main()
