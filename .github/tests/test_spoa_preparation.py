import unittest

from scripts.ci.github_spoa import preparation_environment


class SPOAPreparationTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.run = {"id": 42, "run_attempt": 2, "repository": {"full_name": "Owner/repo"},
                    "head_repository": {"full_name": "Owner/repo"}, "head_sha": self.sha,
                    "head_branch": "release/v1.2.3-rc.1", "event": "workflow_dispatch",
                    "path": ".github/workflows/prepare-spoa-release.yml"}
        self.job = {"id": 123, "run_id": 42, "run_attempt": 2, "head_sha": self.sha,
                    "name": "prepare-spoa-release", "status": "in_progress"}

    def environment(self, run=None, jobs=None, **changes):
        arguments = {"repository": "Owner/repo", "sha": self.sha, "ref": "refs/heads/release/v1.2.3-rc.1",
                     "attempt": 2, "version": "1.2.3-rc.1"} | changes
        return preparation_environment(self.run if run is None else run,
                                       [self.job] if jobs is None else jobs, **arguments)

    def test_preserves_native_release_branch_and_job_identity(self):
        self.assertEqual(self.environment(), {
            "CI_REGISTRY_IMAGE": "ghcr.io/owner/haptic", "CI_COMMIT_BRANCH": "release/v1.2.3-rc.1",
            "CI_JOB_ID": "123", "CI_PROJECT_URL": "https://github.com/Owner/repo", "CI_COMMIT_SHA": self.sha})

    def test_rejects_another_branch_trigger_source_or_attempt(self):
        for field, value in (("head_branch", "main"), ("head_sha", "b" * 40), ("run_attempt", 1),
                             ("event", "pull_request"), ("path", ".github/workflows/another.yml"),
                             ("head_repository", {"full_name": "fork/repo"}),
                             ("repository", {"full_name": "fork/repo"})):
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.environment(run=dict(self.run, **{field: value}))
        for changes in ({"ref": "refs/tags/release/v1.2.3-rc.1"}, {"version": "1.2.4"},
                        {"sha": "invalid"}, {"repository": "owner/repo\nINJECT=value"}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                self.environment(**changes)

    def test_rejects_missing_duplicate_failed_or_stale_jobs(self):
        invalid = [[], [self.job, self.job]]
        for field, value in (("head_sha", "b" * 40), ("run_id", 43), ("run_attempt", 1),
                             ("status", "completed"), ("id", 0)):
            invalid.append([dict(self.job, **{field: value})])
        for jobs in invalid:
            with self.subTest(jobs=jobs), self.assertRaises(ValueError):
                self.environment(jobs=jobs)
