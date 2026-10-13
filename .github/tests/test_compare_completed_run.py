import importlib.util
import copy
from pathlib import Path
import unittest


SPEC = importlib.util.spec_from_file_location("compare_run", Path(__file__).parents[1] / "scripts/compare-completed-run.py")
COMPARE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(COMPARE)


class CompleteRunComparisonTests(unittest.TestCase):
    def setUp(self):
        self.run = {
            "id": 42, "head_sha": "abc", "run_attempt": 1, "repository": {"full_name": "owner/repo"},
            "status": "completed", "conclusion": "success", "html_url": "https://example.test/run/42",
            "created_at": "2026-10-13T00:00:00Z",
        }
        job = {
            "id": 123, "run_id": 42, "run_attempt": 1, "head_sha": "abc", "name": "unit", "status": "completed",
            "conclusion": "success", "started_at": "2026-10-13T00:00:20Z", "completed_at": "2026-10-13T00:01:00Z",
        }
        self.jobs = [job, dict(job, id=124, name="verified", completed_at="2026-10-13T00:01:10Z")]
        self.gitlab = {
            "pipeline": {"status": "success", "sha": "def", "web_url": "https://example.test/pipeline/12",
                         "created_at": self.run["created_at"], "finished_at": "2026-10-13T00:03:00Z"},
            "jobs": [{"name": "test", "status": "success", "started_at": "2026-10-13T00:00:10Z",
                      "finished_at": "2026-10-13T00:01:20Z"},
                     {"name": "cleanup", "status": "success", "started_at": "2026-10-13T00:01:30Z",
                      "finished_at": "2026-10-13T00:02:50Z"}],
        }
        self.manifest = {
            "repository": "owner/repo", "required_jobs": ["unit"], "gitlab_job_mapping": {"test": ["unit"]},
            "baseline": {"wall_seconds": 80, "url": self.gitlab["pipeline"]["web_url"], "commit": "def",
                         "created_at": self.run["created_at"], "completed_at": "2026-10-13T00:01:20Z",
                         "excluded_post_merge_jobs": ["cleanup"]},
        }

    def test_counts_queueing_and_final_verification_completion(self):
        report = COMPARE.compare(self.run, self.jobs, self.manifest, self.gitlab)
        self.assertEqual(report["github_wall_seconds"], 70)
        self.assertEqual(report["verified_jobs"], 2)
        self.assertEqual(report["seconds_saved"], 10)
        self.assertTrue(report["speed_requirement_passed"])
        self.jobs[1]["completed_at"] = "2026-10-13T00:01:30Z"
        self.assertFalse(COMPARE.compare(self.run, self.jobs, self.manifest, self.gitlab)["speed_requirement_passed"])

    def test_gitlab_cleanup_cannot_inflate_the_baseline(self):
        self.assertEqual(COMPARE.gitlab_duration(self.gitlab, self.manifest), 80)
        self.manifest["baseline"].update(wall_seconds=180, completed_at="2026-10-13T00:03:00Z")
        with self.assertRaisesRegex(ValueError, "final required check"):
            COMPARE.gitlab_duration(self.gitlab, self.manifest)

    def test_gitlab_incomplete_retried_failed_or_mixed_evidence_is_rejected(self):
        invalid = [dict(self.gitlab, jobs=self.gitlab["jobs"][1:]),
                   dict(self.gitlab, jobs=self.gitlab["jobs"] + [self.gitlab["jobs"][0]])]
        for changes in ({"status": "failed"}, {"sha": "older"}, {"web_url": "another-pipeline"}):
            invalid.append(dict(self.gitlab, pipeline=dict(self.gitlab["pipeline"], **changes)))
        failed = copy.deepcopy(self.gitlab)
        failed["jobs"][0]["status"] = "failed"
        invalid.append(failed)
        for evidence in invalid:
            with self.subTest(evidence=evidence), self.assertRaises(ValueError):
                COMPARE.gitlab_duration(evidence, self.manifest)

    def test_incomplete_retried_failed_or_mixed_evidence_cannot_prove_speed(self):
        for changes in ({"status": "in_progress"}, {"conclusion": "failure"}, {"run_attempt": 2},
                        {"repository": {"full_name": "another/repo"}}):
            with self.subTest(changes=changes), self.assertRaises(ValueError):
                COMPARE.compare(dict(self.run, **changes), self.jobs, self.manifest, self.gitlab)
        for jobs in (self.jobs[:1], [dict(self.jobs[0], head_sha="older"), self.jobs[1]],
                     [dict(self.jobs[0], conclusion="skipped"), self.jobs[1]]):
            with self.subTest(jobs=jobs), self.assertRaises(ValueError):
                COMPARE.compare(self.run, jobs, self.manifest, self.gitlab)


if __name__ == "__main__":
    unittest.main()
