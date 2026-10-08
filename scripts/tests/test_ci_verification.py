"""Exercise merge-gate failure modes independently of successful pipelines."""

import copy
import hashlib
import json
from pathlib import Path
import re
import unittest
from unittest.mock import Mock, patch

from scripts.ci.configuration import expand, job, load
from scripts.ci.verification import completed_train_pipeline, context, profile, required_jobs, validate_jobs, validate_receipt, verify_publication


SHA = "a" * 40


class ResultTests(unittest.TestCase):
    def setUp(self):
        self.jobs = [{"id": 1, "name": "test", "status": "success", "allow_failure": False,
                      "pipeline": {"id": 20}, "commit": {"id": SHA}}]

    def test_success_records_specific_job_ids(self):
        self.assertEqual(validate_jobs(self.jobs, ["test"], 20, SHA), [{"name": "test", "id": 1}])

    def test_missing_required_job_fails(self):
        with self.assertRaisesRegex(ValueError, "missing"):
            validate_jobs([], ["test"], 20, SHA)

    def test_every_non_success_status_fails(self):
        for status in ["failed", "skipped", "canceled", "manual", "pending", "running"]:
            with self.subTest(status=status):
                self.jobs[0]["status"] = status
                with self.assertRaises(ValueError):
                    validate_jobs(self.jobs, ["test"], 20, SHA)

    def test_optional_success_is_not_a_required_gate(self):
        self.jobs[0]["allow_failure"] = True
        with self.assertRaises(ValueError):
            validate_jobs(self.jobs, ["test"], 20, SHA)

    def test_wrong_source_or_pipeline_fails(self):
        for pipeline, sha in [(21, SHA), (20, "b" * 40)]:
            with self.assertRaises(ValueError):
                validate_jobs(self.jobs, ["test"], pipeline, sha)

    def test_newer_failed_retry_overrides_an_older_success(self):
        self.jobs.append({**self.jobs[0], "id": 2, "status": "failed"})
        with self.assertRaises(ValueError):
            validate_jobs(self.jobs, ["test"], 20, SHA)



class ContextTests(unittest.TestCase):
    def setUp(self):
        self.env = {"CI_MERGE_REQUEST_EVENT_TYPE": "merge_train", "CI_PIPELINE_ID": "20",
                    "CI_MERGE_REQUEST_SOURCE_BRANCH_SHA": SHA, "CI_COMMIT_SHA": "b" * 40}
        self.mr = {"state": "opened", "sha": SHA, "target_branch": "main"}
        self.train = {"pipeline": {"id": 20, "sha": "b" * 40}}

    def verify(self, checkout="b" * 40):
        api = Mock()
        api.get.side_effect = [self.mr, self.train]
        with patch("scripts.ci.verification.git", return_value=checkout):
            return context(api, "30", "12", self.env)

    def test_native_train_context_matches_current_source_and_pipeline(self):
        self.assertEqual(self.verify(), self.mr)

    def test_ordinary_pipeline_cannot_satisfy_train_gate(self):
        for event in ["merged_result", "detached", ""]:
            self.env["CI_MERGE_REQUEST_EVENT_TYPE"] = event
            with self.assertRaises(ValueError):
                self.verify()

    def test_changed_source_closed_mr_or_wrong_target_is_rejected(self):
        for field, value in [("sha", "c" * 40), ("state", "merged"), ("target_branch", "feature/other")]:
            original = self.mr[field]
            self.mr[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.verify()
            self.mr[field] = original

    def test_wrong_checkout_or_different_train_pipeline_is_rejected(self):
        with self.assertRaises(ValueError):
            self.verify(checkout="c" * 40)
        self.train["pipeline"]["id"] = 21
        with self.assertRaises(ValueError):
            self.verify()


class SelectionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = load()
        cls.policy = json.loads(Path(".gitlab/ci/verification-policy.json").read_text())

    def train_selected(self, name, paths):
        for rule in expand(job(self.config, name)["rules"], self.config):
            condition = rule.get("if", "")
            if condition == '$MIRROR_IMAGES == "true" && $CI_MERGE_REQUEST_EVENT_TYPE != "merge_train"':
                continue
            if condition == '$CI_PIPELINE_SOURCE == "schedule" && $SCHEDULE_KIND == "nightly"':
                continue
            if condition != '$CI_MERGE_REQUEST_EVENT_TYPE == "merge_train"':
                raise AssertionError(f"{name} reached non-train rule {rule}")
            if "changes" in rule:
                pattern = rule["changes"]["regexp"].replace(r"\z", r"\Z")
                if not any(re.search(pattern, path) for path in paths):
                    continue
            return rule.get("when") != "never"
        return False

    def test_train_runs_every_required_job_for_code_policy_and_unknown_inputs(self):
        for path in ["pkg/controller/input_isolation.go", ".gitlab-ci.yml", "AGENTS.md",
                     ".gitar/review/ci-verification.md", "charts/haptic/files/config.md", "new.unknown"]:
            for name in self.policy["common_jobs"] + self.policy["full_jobs"]:
                with self.subTest(path=path, job=name):
                    self.assertTrue(self.train_selected(name, [path]))

    def test_prose_still_gets_common_checks_without_full_matrix(self):
        for paths in [["README.md"], ["docs/site/docs/operations/input-isolation.md"], ["charts/haptic/README.md"]]:
            self.assertEqual(profile(paths, self.policy), "prose")
            for name in self.policy["common_jobs"]:
                self.assertTrue(self.train_selected(name, paths), name)
            for name in self.policy["full_jobs"]:
                self.assertFalse(self.train_selected(name, paths), name)

    def test_mixed_and_unknown_paths_are_full(self):
        for paths in [[], ["README.md", "pkg/change.go"], ["charts/haptic/templates/readme.md"], ["docs/site/docs/a.md\ncode"]]:
            self.assertEqual(profile(paths, self.policy), "full")

    def test_full_manifest_contains_all_haproxy_versions_and_conformance_shards(self):
        required = required_jobs(self.config, self.policy, "full", "feature/example")
        for version in ["3.0", "3.1", "3.2", "3.3", "3.4"]:
            base = "test-e2e" if version in {"3.0", "3.4"} else "test-e2e-extra-versions"
            for shard in [1, 2, 3]:
                self.assertIn(f"{base}: [{version}, {shard}]", required)
        for shard in range(1, 5):
            self.assertIn(f"test-gateway-conformance {shard}/4", required)
            self.assertIn(f"test-ingress-conformance {shard}/4", required)

    def test_deleting_a_required_definition_fails(self):
        config = copy.deepcopy(self.config)
        del config["test"]
        with self.assertRaises(KeyError):
            required_jobs(config, self.policy, "full", "feature/example")

    def test_release_preparation_is_required_only_for_release_candidates(self):
        self.assertIn("prepare-spoa-release", required_jobs(self.config, self.policy, "full", "release/v0.5.0"))
        self.assertNotIn("prepare-spoa-release", required_jobs(self.config, self.policy, "full", "fix/bug"))


class PipelineCompletionTests(unittest.TestCase):
    def setUp(self):
        self.train = {"status": "merged", "pipeline": {"id": 20}}
        self.api = Mock()

    def test_merged_train_can_finish_automatic_environment_cleanup(self):
        self.api.get.side_effect = [{"status": "running"}, {"status": "success"}]
        with patch("scripts.ci.verification.time.sleep") as sleep:
            self.assertEqual(completed_train_pipeline(self.api, "30", self.train), {"status": "success"})
        sleep.assert_called_once_with(5)
        self.assertEqual(self.api.get.call_count, 2)

    def test_terminal_failure_or_unmerged_train_is_not_retried(self):
        for status in ["failed", "canceled", "skipped", "manual"]:
            self.api.get.return_value = {"status": status}
            with self.subTest(status=status), self.assertRaises(ValueError), patch("scripts.ci.verification.time.sleep") as sleep:
                completed_train_pipeline(self.api, "30", self.train)
            sleep.assert_not_called()
        self.train["status"] = "fresh"
        self.api.get.return_value = {"status": "running"}
        with self.assertRaises(ValueError):
            completed_train_pipeline(self.api, "30", self.train)

    def test_cleanup_that_never_finishes_blocks_publication(self):
        self.api.get.return_value = {"status": "running"}
        with patch("scripts.ci.verification.time.monotonic", side_effect=[0, 181]), self.assertRaises(ValueError):
            completed_train_pipeline(self.api, "30", self.train)


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.mr = {"iid": 12, "sha": SHA, "target_branch": "main", "source_branch": "fix/bug"}
        self.train = {"status": "merged", "target_branch": "main", "pipeline": {
            "id": 20, "sha": "b" * 40, "status": "success", "ref": "refs/merge-requests/12/train"}}
        self.jobs = [{"id": 1, "name": "test", "status": "success", "allow_failure": False,
                      "pipeline": {"id": 20}, "commit": {"id": "b" * 40}}]
        self.policy = {"common_jobs": ["test"], "full_jobs": []}
        self.config = {"test": {"script": "test"}}
        self.receipt = {"version": 1, "project_id": 30, "mr_iid": 12, "pipeline_id": 20,
                        "pipeline_sha": "b" * 40, "source_sha": SHA, "target_branch": "main",
                        "tree": "c" * 40, "profile": "full", "required_jobs": [{"name": "test", "id": 1}],
                        "policy_sha256": hashlib.sha256(Path(".gitlab/ci/verification-policy.json").read_bytes()).hexdigest(),
                        "build_pins_sha256": hashlib.sha256(Path(".gitlab/ci/build-pins.yml").read_bytes()).hexdigest()}

    def verify(self):
        validate_receipt(self.receipt, self.mr, self.train, self.jobs, 30, "c" * 40, self.policy, self.config)

    def test_unchanged_tree_with_successful_required_jobs(self):
        self.verify()

    def test_receipt_cannot_be_reused_for_different_code_or_policy(self):
        for key in ["version", "project_id", "mr_iid", "pipeline_id", "pipeline_sha", "source_sha",
                    "target_branch", "tree", "policy_sha256", "build_pins_sha256", "profile", "required_jobs"]:
            with self.subTest(key=key):
                original = self.receipt[key]
                self.receipt[key] = "different"
                with self.assertRaises(ValueError):
                    self.verify()
                self.receipt[key] = original

    def test_skipped_train_or_later_failed_job_blocks_publication(self):
        for status in ["skip_merged", "fresh", "stale"]:
            self.train["status"] = status
            with self.assertRaises(ValueError):
                self.verify()
        self.train["status"] = "merged"
        self.jobs[0]["status"] = "failed"
        with self.assertRaises(ValueError):
            self.verify()

    def test_successful_ordinary_pipeline_is_not_a_train(self):
        self.train["pipeline"]["ref"] = "refs/merge-requests/12/merge"
        with self.assertRaises(ValueError):
            self.verify()

    def test_maintenance_branch_can_start_from_a_verified_main_commit(self):
        policy = json.loads(Path(".gitlab/ci/verification-policy.json").read_text())
        required = required_jobs(load(), policy, "full", "fix/bug")
        jobs = [{**self.jobs[0], "name": name, "id": index}
                for index, name in enumerate(required, start=1)]
        jobs.append({**self.jobs[0], "name": "verify-merge-train", "id": 999})
        mr = {**self.mr, "state": "merged", "merge_commit_sha": "e" * 40}
        receipt = {**self.receipt, "target_sha": "d" * 40, "ci_image_tag": "pinned",
                   "required_jobs": [{"name": j["name"], "id": j["id"]} for j in jobs[:-1]]}
        api = Mock()
        records = {"projects/30/merge_requests/12": mr,
                   "projects/30/merge_trains/merge_requests/12": self.train,
                   "projects/30/pipelines/20": self.train["pipeline"],
                   "projects/30/jobs/999/artifacts/ci-verification.json": receipt}
        api.get.side_effect = records.__getitem__
        api.all.side_effect = lambda path: [mr] if path.endswith("/merge_requests") else jobs
        revisions = {"HEAD": "e" * 40, "HEAD^{tree}": "c" * 40, "HEAD^1": "d" * 40}
        with patch("scripts.ci.verification.git", side_effect=lambda _, rev: revisions[rev]), \
                patch("scripts.ci.verification.subprocess.run") as ancestry, \
                patch("scripts.ci.verification.subprocess.check_output", side_effect=[b"pkg/change.go\0", "pinned\n"]):
            verify_publication(api, {"CI_PROJECT_ID": "30", "CI_COMMIT_SHA": "e" * 40,
                                     "CI_COMMIT_BRANCH": "maint/0.5"})
        ancestry.assert_called_once_with(["git", "merge-base", "--is-ancestor", "d" * 40, "HEAD"], check=True)


if __name__ == "__main__":
    unittest.main()
