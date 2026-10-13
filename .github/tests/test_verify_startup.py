import importlib.util
from pathlib import Path
import unittest


SPEC = importlib.util.spec_from_file_location("verify_startup", Path(__file__).parents[1] / "scripts/verify-startup.py")
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


def pod(name, ready=True, restarts=0):
    return {"metadata": {"name": name, "labels": {"app.kubernetes.io/component": "controller"}},
            "status": {"containerStatuses": [{"name": "controller", "ready": ready, "restartCount": restarts}]}}


def result(name, passed=1116):
    return (f'[pod/{name}/controller] time=2026-10-12T21:00:00Z level=INFO msg="HAProxy Template Ingress Controller starting"\n'
            f'[pod/{name}/controller] time=2026-10-12T21:00:00Z level=DEBUG msg="Test run completed" '
            f'component=test-runner total=1116 passed={passed} failed=0 skipped=0 duration=1m30s\n')


class StartupVerificationTests(unittest.TestCase):
    def test_runtime_environment_matches_for_every_replica(self):
        pods = {"items": [pod("a"), pod("b")]}
        for item in pods["items"]:
            item["spec"] = {"containers": [{"name": "controller", "env": [{"name": "GOMAXPROCS", "value": "2"}]}]}
        VERIFY.verify_environment(pods, {"GOMAXPROCS": "2"})
        for environment in ([], [{"name": "GOMEMLIMIT", "value": "1000"}],
                            [{"name": "GOMAXPROCS", "value": "4"}],
                            [{"name": "GOMAXPROCS", "value": "2"}, {"name": "GOMAXPROCS", "value": "2"}]):
            with self.subTest(environment=environment):
                pods["items"][1]["spec"]["containers"][0]["env"] = environment
                with self.assertRaises(ValueError):
                    VERIFY.verify_environment(pods, {"GOMAXPROCS": "2"})

    def test_complete_first_startup_passes_for_both_replicas(self):
        self.assertEqual(len(VERIFY.verify(result("a") + result("b"), {"items": [pod("a"), pod("b")]})), 2)

    def test_later_success_cannot_hide_incomplete_first_attempt(self):
        with self.assertRaises(ValueError):
            VERIFY.verify(result("a", 1098) + result("a") + result("b"), {"items": [pod("a"), pod("b")]})

    def test_version_ineligible_tests_are_reported_separately_from_runnable_total(self):
        log = (result("a") + result("b")).replace("skipped=0", "skipped=2")
        verdict = VERIFY.verify(log, {"items": [pod("a"), pod("b")]})
        self.assertEqual({record["skipped"] for record in verdict.values()}, {2})

    def test_truncated_startup_logs_cannot_prove_first_attempt(self):
        log = result("a").split("\n", 1)[1] + result("b")
        with self.assertRaisesRegex(ValueError, "missing or truncated"):
            VERIFY.verify(log, {"items": [pod("a"), pod("b")]})

    def test_both_replicas_need_execution_and_readiness_without_restarts(self):
        for pods, logs in [([pod("a")], result("a")),
                           ([pod("a"), pod("b")], result("a")),
                           ([pod("a"), pod("b", ready=False)], result("a") + result("b")),
                           ([pod("a"), pod("b", restarts=1)], result("a") + result("b"))]:
            with self.subTest(pods=pods, logs=logs), self.assertRaises(ValueError):
                VERIFY.verify(logs, {"items": pods})


if __name__ == "__main__":
    unittest.main()
