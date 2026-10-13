import copy
import io
import json
import unittest
import urllib.error
import zipfile
from unittest import mock

from scripts.ci.github import GitHub, jobs_for_run
from scripts.ci.github_history import successful_job_metrics
from scripts.ci.github_nightly import plan, required
from scripts.ci.github_scale import TREND


class HistoryTests(unittest.TestCase):
    def setUp(self):
        self.run = {"id": 42, "workflow_id": 7, "head_sha": "abc", "run_attempt": 2,
                    "head_branch": "main", "event": "schedule", "status": "completed", "conclusion": "success"}
        self.job = {"id": 9, "run_id": 42, "run_attempt": 2, "head_sha": "abc", "name": "nightly-scale",
                    "status": "completed", "conclusion": "success",
                    "started_at": "2026-10-13T01:00:00Z", "completed_at": "2026-10-13T01:05:00Z"}
        self.artifact = {"id": 19, "name": "nightly-scale-metrics", "expired": False,
                         "created_at": "2026-10-13T01:04:00Z",
                         "workflow_run": {"id": 42, "head_sha": "abc", "head_branch": "main"}}
        self.api = mock.Mock(spec=GitHub)
        self.api.all.side_effect = lambda path, field, **params: iter({
            "workflow_runs": [self.run], "jobs": [self.job], "artifacts": [self.artifact]}[field])
        self.api.artifact.return_value = self.archive()

    @staticmethod
    def archive(name="scale-metrics.json", contents=None):
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as archive:
            archive.writestr(name, json.dumps(contents or {"runner_cpu_model": "cpu", "controller_rss_bytes": 100}))
        return output.getvalue()

    def history(self):
        return successful_job_metrics(self.api, "owner/repo", 43, workflow="nightly.yml", branch="main",
                                      job_name="nightly-scale", artifact_name="nightly-scale-metrics",
                                      metrics_name="scale-metrics.json", max_runs=14, max_workflows=60)

    def test_successful_job_is_a_baseline_even_if_another_job_failed(self):
        self.run["conclusion"] = "failure"
        self.assertEqual(self.history(), [{"runner_cpu_model": "cpu", "controller_rss_bytes": 100, "_job_id": 9}])

    def test_ineligible_runs_and_failed_jobs_are_not_baselines(self):
        original = copy.deepcopy(self.run)
        for key, value in (("id", 43), ("head_branch", "feature"), ("event", "pull_request"), ("status", "in_progress")):
            with self.subTest(key=key):
                self.run.update(original)
                self.run[key] = value
                self.assertEqual(self.history(), [])
        self.run.update(original)
        for conclusion in ("failure", "skipped", "cancelled", "timed_out", None):
            self.job["conclusion"] = conclusion
            self.assertEqual(self.history(), [])
        self.api.artifact.assert_not_called()

    def test_expired_artifacts_are_skipped_but_api_failures_are_errors(self):
        self.artifact["expired"] = True
        self.assertEqual(self.history(), [])
        self.artifact["expired"] = False
        for code in (404, 410):
            self.api.artifact.side_effect = urllib.error.HTTPError("https://api.github.com", code, "expired", {}, None)
            self.assertEqual(self.history(), [])
        self.api.artifact.side_effect = urllib.error.HTTPError("https://api.github.com", 403, "denied", {}, None)
        with self.assertRaises(urllib.error.HTTPError):
            self.history()

    def test_cross_revision_attempt_and_stale_artifacts_fail(self):
        for key, value in (("head_sha", "other"), ("run_id", 41), ("run_attempt", 1)):
            original = self.job[key]
            self.job[key] = value
            with self.subTest(job=key), self.assertRaises(ValueError):
                self.history()
            self.job[key] = original
        for key, value in (("head_sha", "other"), ("id", 41), ("head_branch", "feature")):
            original = self.artifact["workflow_run"][key]
            self.artifact["workflow_run"][key] = value
            with self.subTest(artifact=key), self.assertRaises(ValueError):
                self.history()
            self.artifact["workflow_run"][key] = original
        self.artifact["created_at"] = "2026-10-12T01:04:00Z"
        with self.assertRaises(ValueError):
            self.history()

    def test_archive_paths_are_never_extracted_or_treated_as_metrics(self):
        for name in ("../scale-metrics.json", "other.json", "/scale-metrics.json"):
            self.api.artifact.return_value = self.archive(name)
            with self.subTest(name=name), self.assertRaises(ValueError):
                self.history()

    def test_duplicate_jobs_are_rejected(self):
        self.api.all.return_value = iter([self.job, self.job])
        self.api.all.side_effect = None
        with self.assertRaises(ValueError):
            jobs_for_run(self.api, "owner/repo", self.run)

    def test_unchanged_main_keeps_failed_nightly_evidence_without_daily_retries(self):
        current = dict(self.run, id=43)
        self.run["conclusion"] = "failure"
        self.job["conclusion"] = "failure"
        self.assertFalse(required(self.api, "owner/repo", current, branch="main"))
        self.assertTrue(required(self.api, "owner/repo", dict(current, head_sha="new"), branch="main"))
        self.assertTrue(required(self.api, "owner/repo", current, branch="main", force=True))

    def test_cancelled_and_decision_only_runs_do_not_hide_previous_work(self):
        current = dict(self.run, id=43)
        for conclusion in ("cancelled", "skipped"):
            self.job["conclusion"] = conclusion
            self.assertTrue(required(self.api, "owner/repo", current, branch="main"))

    def test_nightly_selection_rejects_non_main_and_pr_contexts(self):
        for current in (dict(self.run, head_branch="feature"), dict(self.run, event="pull_request")):
            with self.assertRaises(ValueError):
                required(self.api, "owner/repo", current, branch="main", force=True)

    def test_weekly_heartbeat_and_manual_dispatch_override_unchanged_revision(self):
        current = dict(self.run, id=43)
        self.api.get.return_value = current
        env = {"GITHUB_REPOSITORY": "owner/repo", "GITHUB_RUN_ID": "43", "GITHUB_SHA": "abc",
               "GITHUB_EVENT_NAME": "schedule"}
        event = {"repository": {"full_name": "owner/repo", "default_branch": "main"}, "schedule": "30 2 * * 1-6"}
        self.assertEqual(plan(self.api, env, event), {"run": "false", "mode": "nightly"})
        event["schedule"] = "30 3 * * 0"
        self.assertEqual(plan(self.api, env, event), {"run": "true", "mode": "weekly"})
        current["event"] = env["GITHUB_EVENT_NAME"] = "workflow_dispatch"
        event["inputs"] = {"mode": "scale"}
        self.assertEqual(plan(self.api, env, event), {"run": "true", "mode": "scale"})
        current["head_sha"] = "other"
        with self.assertRaises(ValueError):
            plan(self.api, env, event)

    def test_scale_gate_reuses_existing_comparison_contract(self):
        self.assertEqual(TREND.HARD, {"change_convergence_seconds_p95": 0.5,
                                     "haproxycfg_publication_seconds_p95": 0.5, "controller_rss_bytes": 0.5})
        self.assertEqual((TREND.WINDOW, TREND.MIN_BASELINES, TREND.MAX_RUNS), (7, 3, 14))
        history = [{"runner_cpu_model": "cpu", "_job_id": n, "controller_rss_bytes": 100} for n in range(3)]
        lines, failed = TREND.evaluate({"runner_cpu_model": "cpu", "controller_rss_bytes": 151}, history)
        self.assertTrue(failed, lines)
        lines, failed = TREND.evaluate({"runner_cpu_model": "other", "controller_rss_bytes": 151}, history)
        self.assertFalse(failed, lines)
        self.assertIn("TREND UNGATED", "\n".join(lines))


class DownloadTests(unittest.TestCase):
    @mock.patch("urllib.request.urlopen")
    @mock.patch("urllib.request.build_opener")
    def test_signed_storage_download_receives_no_github_token(self, opener, urlopen):
        opener.return_value.open.side_effect = urllib.error.HTTPError(
            "https://api.github.com", 302, "found", {"Location": "https://storage.example/artifact?signature=secret"}, None)
        urlopen.return_value.read.return_value = b"zip"
        self.assertEqual(GitHub("test-token").artifact("owner/repo", 19, 100), b"zip")
        request = opener.return_value.open.call_args.args[0]
        self.assertEqual(request.get_header("Authorization"), "Bearer test-token")
        urlopen.assert_called_once_with("https://storage.example/artifact?signature=secret", timeout=30)

    @mock.patch("urllib.request.build_opener")
    def test_oversized_download_fails(self, opener):
        opener.return_value.open.return_value.read.return_value = b"oversized"
        with self.assertRaises(ValueError):
            GitHub("test-token").artifact("owner/repo", 19, 3)


if __name__ == "__main__":
    unittest.main()
