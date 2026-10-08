"""An unchanged main must not spend another nightly's compute allowance."""

import io
import json
import unittest
from unittest.mock import Mock, patch

from scripts.ci.nightly import dispatch, trigger


class NightlyTests(unittest.TestCase):
    def setUp(self):
        self.env = {"CI_PIPELINE_SOURCE": "schedule", "SCHEDULE_KIND": "nightly",
                    "CI_DEFAULT_BRANCH": "main", "CI_COMMIT_BRANCH": "main",
                    "CI_PROJECT_ID": "123", "CI_API_V4_URL": "https://gitlab.example/api/v4",
                    "CI_JOB_TOKEN": "temporary-job-token"}
        self.api = Mock()
        self.api.get.return_value = {"commit": {"id": "new"}}
        self.api.all.return_value = []
        self.start = Mock(return_value={"sha": "new", "web_url": "https://gitlab.example/pipeline/2"})

    def run_dispatch(self):
        return dispatch(self.api, self.env, self.start)

    def test_trigger_posts_the_profile_with_the_temporary_job_token(self):
        response = {"sha": "new", "web_url": "https://gitlab.example/pipeline/2"}
        with patch("scripts.ci.nightly.urllib.request.urlopen", return_value=io.BytesIO(json.dumps(response).encode())) as post:
            self.assertEqual(trigger(self.env["CI_API_V4_URL"], "123", "main", "job-token"), response)
        request = post.call_args.args[0]
        self.assertEqual(request.full_url, "https://gitlab.example/api/v4/projects/123/trigger/pipeline")
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(json.loads(request.data), {"ref": "main", "token": "job-token",
                                                   "variables": {"SCHEDULE_KIND": "nightly"}})

    def test_first_nightly_runs(self):
        self.run_dispatch()
        self.start.assert_called_once_with(self.env["CI_API_V4_URL"], "123", "main", "temporary-job-token")
        self.api.all.assert_called_once_with("projects/123/pipelines", ref="main", name="Nightly checks",
                                             order_by="id", sort="desc")

    def test_new_commit_runs(self):
        self.api.all.return_value = [{"id": 1, "sha": "old", "status": "success"}]
        self.run_dispatch()
        self.start.assert_called_once()

    def test_unchanged_commit_does_not_repeat_success_failure_or_active_run(self):
        for status in ("success", "failed", "created", "pending", "running"):
            with self.subTest(status=status):
                self.api.all.return_value = [{"id": 1, "sha": "new", "status": status}]
                self.assertIsNone(self.run_dispatch())
        self.start.assert_not_called()

    def test_canceled_and_skipped_runs_are_not_evidence_of_execution(self):
        self.api.all.return_value = [{"id": 3, "sha": "new", "status": "canceled"},
                                    {"id": 2, "sha": "new", "status": "skipped"},
                                    {"id": 1, "sha": "old", "status": "success"}]
        self.run_dispatch()
        self.start.assert_called_once()

    def test_queries_live_head_instead_of_the_queued_schedule_sha(self):
        self.env["CI_COMMIT_SHA"] = "old"
        self.api.all.return_value = [{"id": 1, "sha": "old", "status": "success"}]
        self.run_dispatch()
        self.start.assert_called_once()

    def test_lookup_failure_does_not_start_an_unaccounted_pipeline(self):
        self.api.all.side_effect = RuntimeError("API unavailable")
        with self.assertRaisesRegex(RuntimeError, "API unavailable"):
            self.run_dispatch()
        self.start.assert_not_called()

    def test_trigger_failure_is_not_recorded_as_a_completed_nightly(self):
        self.start.side_effect = RuntimeError("trigger rejected")
        with self.assertRaisesRegex(RuntimeError, "trigger rejected"):
            self.run_dispatch()

    def test_only_the_default_branch_nightly_schedule_can_dispatch(self):
        for key, value in (("CI_PIPELINE_SOURCE", "merge_request_event"),
                           ("SCHEDULE_KIND", "weekly"), ("CI_COMMIT_BRANCH", "feature/example")):
            with self.subTest(key=key):
                env = {**self.env, key: value}
                with self.assertRaises(ValueError):
                    dispatch(self.api, env, self.start)
        self.start.assert_not_called()


if __name__ == "__main__":
    unittest.main()
