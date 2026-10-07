"""Budget failures must never turn missing accounting into spare capacity."""

import unittest

from scripts.ci.budget import billed_minutes, cost_factor, forecast_jobs, remaining_minutes, report


class BudgetTests(unittest.TestCase):
    def setUp(self):
        self.project = {"id": 1, "visibility": "public", "path_with_namespace": "haptic/test", "build_timeout": 3600}
        self.namespace = {"plan": "opensource", "ci_minutes_usage": {"monthly_minutes_used": 49733, "purchased_minutes_used": 0}}
        self.group = {"shared_runners_minutes_limit": 50000, "extra_shared_runners_minutes_limit": 3000}

    def test_purchased_minutes_are_added_and_usage_is_subtracted(self):
        self.assertEqual(remaining_minutes(self.group, self.namespace), 3267)
        self.namespace["ci_minutes_usage"].update(monthly_minutes_used=51000, purchased_minutes_used=1000)
        self.assertEqual(remaining_minutes(self.group, self.namespace), 2000)

    def test_missing_usage_and_unlimited_quota_do_not_report_free_capacity(self):
        with self.assertRaises(KeyError):
            remaining_minutes(self.group, {})
        self.group["shared_runners_minutes_limit"] = 0
        with self.assertRaises(ValueError):
            remaining_minutes(self.group, self.namespace)

    def test_open_source_discount_is_not_multiplied_by_runner_size(self):
        self.assertEqual(billed_minutes(self.project, self.namespace, [
            {"name": "test", "duration": 120, "tag_list": ["saas-linux-2xlarge-amd64"]}]), 1)

    def test_unknown_private_runner_factor_fails(self):
        self.project["visibility"] = "private"
        with self.assertRaises(ValueError):
            cost_factor(self.project, self.namespace, {"name": "test", "tag_list": []})
        self.assertEqual(cost_factor(self.project, self.namespace,
                                     {"name": "test", "tag_list": ["saas-linux-xlarge-amd64"]}), 6)

    def test_unknown_active_jobs_reserve_the_timeout_and_manual_required_jobs_count(self):
        jobs = [{"name": "new", "status": "pending"},
                {"name": "release", "status": "manual", "allow_failure": False},
                {"name": "optional", "status": "manual", "allow_failure": True}]
        self.assertEqual(forecast_jobs(self.project, self.namespace, jobs, {}), 60)

    def test_active_work_uses_headroom_and_includes_completed_and_retried_jobs(self):
        jobs = [{"name": "test", "status": "running", "duration": 100},
                {"name": "test", "status": "failed", "duration": 80},
                {"name": "build", "status": "success", "duration": 120}]
        self.assertAlmostEqual(forecast_jobs(self.project, self.namespace, jobs, {"test": [240]}), 500 / 120)

    def test_cross_project_reservations_and_recovery_reserve_block_overspend(self):
        projects = [{"reservations": [{"minutes": 900}]}, {"reservations": [{"minutes": 800}]}]
        result = report(self.group, self.namespace, projects, 400, 600, 600)
        self.assertEqual(result["available_for_candidate_minutes"], 367)
        self.assertFalse(result["fits"])


if __name__ == "__main__":
    unittest.main()
