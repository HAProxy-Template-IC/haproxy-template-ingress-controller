import copy
import importlib.util
from pathlib import Path
import unittest


SPEC = importlib.util.spec_from_file_location("verify_run", Path(__file__).parents[1] / "scripts/verify-run.py")
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


class VerifyRunTests(unittest.TestCase):
    def setUp(self):
        self.run = {"id": 42, "head_sha": "abc", "run_attempt": 2, "repository": {"full_name": "owner/repo"}}
        self.job = {
            "id": 123, "run_id": 42, "run_attempt": 2, "head_sha": "abc", "name": "unit", "status": "completed",
            "conclusion": "success", "started_at": "2026-10-12T20:00:00Z", "completed_at": "2026-10-12T20:01:00Z",
        }

    def check(self, jobs, run=None, required=None):
        return VERIFY.verify(self.run if run is None else run, jobs, ["unit"] if required is None else required,
                             repository="owner/repo", sha="abc", attempt=2)

    def test_all_required_jobs_must_have_run_and_passed(self):
        self.assertEqual(self.check([self.job]), [{"name": "unit", "id": 123}])
        for conclusion in ("failure", "skipped", "cancelled", "neutral", "timed_out", None):
            with self.subTest(conclusion=conclusion), self.assertRaises(ValueError):
                self.check([dict(self.job, conclusion=conclusion)])
        for jobs in ([], [dict(self.job, name="other")], [dict(self.job, status="in_progress")]):
            with self.subTest(jobs=jobs), self.assertRaises(ValueError):
                self.check(jobs)

    def test_provenance_cannot_cross_revision_repository_run_or_attempt(self):
        for key, value in (("head_sha", "stale"), ("run_attempt", 1), ("repository", {"full_name": "another/repo"})):
            run = copy.deepcopy(self.run)
            run[key] = value
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.check([self.job], run=run)
        for key, value in (("head_sha", "stale"), ("run_id", 41), ("run_attempt", 1)):
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.check([dict(self.job, **{key: value})])

    def test_rejects_empty_duplicate_or_incomplete_evidence(self):
        for required in ([], ["unit", "unit"]):
            with self.subTest(required=required), self.assertRaises(ValueError):
                self.check([self.job], required=required)
        with self.assertRaises(ValueError):
            self.check([self.job, self.job])
        for key in ("started_at", "completed_at"):
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.check([dict(self.job, **{key: None})])


class RevisionIdentityTests(unittest.TestCase):
    def setUp(self):
        self.event = {"repository": {"full_name": "owner/repo"}, "pull_request": {
            "number": 43, "merge_commit_sha": None,
            "head": {"sha": "head", "repo": {"full_name": "fork/repo"}},
            "base": {"sha": "base", "ref": "main", "repo": {"full_name": "owner/repo"}},
        }}

    def identity(self, event_name="pull_request", event=None, parents=None, tested_sha="merge"):
        return VERIFY.revision_identity(event_name, self.event if event is None else event,
                                        repository="owner/repo", tested_sha=tested_sha,
                                        parents=["base", "head"] if parents is None else parents)

    def test_pr_binds_api_head_and_tested_merge_without_relying_on_nullable_payload_sha(self):
        self.assertEqual(self.identity(), {"api_sha": "head", "source_sha": "head", "base_sha": "base",
                                           "source_repository": "fork/repo", "base_branch": "main", "pull_request": 43})

    def test_pr_rejects_head_checkout_stale_base_and_other_repository(self):
        for parents in (["base"], ["head", "base"], ["stale", "head"], ["base", "other"], []):
            with self.subTest(parents=parents), self.assertRaises(ValueError):
                self.identity(parents=parents)
        with self.assertRaises(ValueError):
            self.identity(tested_sha="head")
        for target in (self.event["repository"], self.event["pull_request"]["base"]["repo"]):
            target["full_name"] = "other/repo"
            with self.assertRaises(ValueError):
                self.identity()
            target["full_name"] = "owner/repo"

    def test_push_and_scheduled_runs_still_bind_the_exact_checkout(self):
        self.event["after"] = "merge"
        for event_name in ("push", "schedule", "workflow_dispatch"):
            identity = self.identity(event_name=event_name)
            self.assertEqual(identity["api_sha"], "merge")
            self.assertEqual(identity["source_sha"], "merge")
        self.event["after"] = "other"
        with self.assertRaises(ValueError):
            self.identity(event_name="push")
        with self.assertRaises(ValueError):
            self.identity(event_name="pull_request_target")


if __name__ == "__main__":
    unittest.main()
