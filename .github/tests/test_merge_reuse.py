import copy
import hashlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
import urllib.error
import zipfile

import yaml

from scripts.ci import github_reuse as reuse


class MergeReuseTests(unittest.TestCase):
    def setUp(self):
        self.repository = "owner/repo"
        self.root = "repos/" + self.repository
        self.base, self.sha, self.tree, self.merged = (letter * 40 for letter in "abcd")
        self.branch = "gh-readonly-queue/main/pr-43-" + self.base
        self.run = {"id": 42, "head_sha": self.sha, "head_branch": self.branch, "run_attempt": 2,
                    "repository": {"full_name": self.repository}, "head_repository": {"full_name": self.repository},
                    "path": ".github/workflows/ci.yml", "event": "merge_group",
                    "status": "completed", "conclusion": "success", "created_at": "2026-10-13T01:00:00Z"}
        self.jobs = [{"id": number, "run_id": 42, "run_attempt": 2, "head_sha": self.sha, "name": name,
                      "status": "completed", "conclusion": "success", "started_at": "2026-10-13T01:00:00Z",
                      "completed_at": "2026-10-13T01:01:00Z"}
                     for number, name in enumerate(("full / unit", "full / verified", "verified"), 10)]
        self.manifest = json.dumps({"required_jobs": ["unit"]}).encode()
        self.receipt = {"version": 2, "repository": self.repository, "run_id": 42, "run_attempt": 2,
                        "sha": self.sha, "tree": self.tree, "jobs": [{"name": "full / unit", "id": 10}],
                        "job_prefix": "full / ", "event": "merge_group", "api_sha": self.sha,
                        "source_sha": self.sha, "source_repository": self.repository,
                        "manifest_sha256": hashlib.sha256(self.manifest).hexdigest(), "profile": "full",
                        "base_sha": self.base, "base_branch": "main", "merge_group_ref": "refs/heads/" + self.branch}
        self.artifact = {"id": 20, "name": "ci-verification", "expired": False,
                         "created_at": "2026-10-13T01:00:30Z", "workflow_run": {
                             "id": 42, "head_sha": self.sha, "head_branch": self.branch}}
        self.api = Mock()
        self.records = {
            self.root + "/actions/runs/42": self.run,
            self.root + "/git/commits/" + self.sha: {"sha": self.sha, "tree": {"sha": self.tree}},
            self.root + "/compare/" + self.base + "..." + self.sha: {
                "status": "ahead", "merge_base_commit": {"sha": self.base}},
            self.root + "/actions/workflows/ci.yml/runs": {"workflow_runs": [self.run]},
        }
        self.api.get.side_effect = lambda path, **params: copy.deepcopy(self.records[path])
        self.api.all.side_effect = lambda path, field: copy.deepcopy(
            self.jobs if path.endswith("/attempts/2/jobs") else [self.artifact])
        self.pack()

    def pack(self):
        self.content = self.archive(self.receipt)
        self.artifact["digest"] = "sha256:" + hashlib.sha256(self.content).hexdigest()
        self.api.artifact.return_value = self.content

    @staticmethod
    def archive(receipt):
        data = io.BytesIO()
        with zipfile.ZipFile(data, "w") as archive:
            archive.writestr("ci-verification.json", json.dumps(receipt))
        return data.getvalue()

    def source(self):
        return reuse.queue_source(self.api, self.repository, 42, branch="main", base_sha=self.base,
                                  tree=self.tree, manifest=self.manifest)

    def find(self):
        return reuse.find_source(self.api, self.repository, branch="main", base_sha=self.base,
                                 tree=self.tree, manifest=self.manifest, report=Mock())

    def test_accepts_only_original_full_merge_queue_evidence(self):
        result = self.source()
        self.assertEqual(result["sha"], self.sha)
        self.assertEqual(result["base_sha"], self.base)
        self.assertEqual(result["jobs"], self.receipt["jobs"])
        self.assertEqual(result["artifact_id"], 20)
        self.assertEqual(self.find(), result)

    def test_reuse_rejects_failed_or_missing_jobs(self):
        self.jobs[0]["conclusion"] = "failure"
        with self.assertRaises(ValueError):
            self.source()
        self.jobs.pop()
        with self.assertRaises(ValueError):
            self.source()

    def test_rejects_wrong_source_event_branch_repository_path_or_status(self):
        original = copy.deepcopy(self.run)
        for key, value in (("event", "push"), ("event", "pull_request"),
                           ("path", ".github/workflows/ci-speed-proof.yml"),
                           ("head_branch", "gh-readonly-queue/maint/0.1/pr-43"),
                           ("repository", {"full_name": "fork/repo"}),
                           ("head_repository", {"full_name": "fork/repo"}),
                           ("status", "in_progress"), ("conclusion", "failure"), ("id", 41)):
            self.run.update({key: value})
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                self.source()
            self.run.clear()
            self.run.update(copy.deepcopy(original))

    def test_rejects_changed_tree_parent_policy_or_recursive_receipt(self):
        original = copy.deepcopy(self.receipt)
        for key, value in (("tree", "f" * 40), ("base_sha", "f" * 40), ("base_branch", "maint/0.1"),
                           ("manifest_sha256", "wrong"), ("profile", "prose"), ("version", 3),
                           ("merge_group_ref", "refs/heads/main"), ("source_sha", self.merged),
                           ("jobs", []), ("run_attempt", 1)):
            self.receipt = dict(original, **{key: value})
            self.pack()
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.source()
        self.receipt = original
        self.pack()
        self.records[self.root + "/git/commits/" + self.sha]["tree"]["sha"] = "f" * 40
        with self.assertRaisesRegex(ValueError, "different tree"):
            self.source()

    def test_rejects_unrelated_merge_base_despite_equal_trees(self):
        comparison = self.records[self.root + "/compare/" + self.base + "..." + self.sha]
        for status in ("behind", "diverged", "identical"):
            comparison["status"] = status
            with self.subTest(status=status), self.assertRaises(ValueError):
                self.source()
        comparison.update(status="ahead", merge_base_commit={"sha": "f" * 40})
        with self.assertRaises(ValueError):
            self.source()

    def test_rejects_every_missing_skipped_failed_or_stale_job(self):
        original = copy.deepcopy(self.jobs)
        for index in range(len(original)):
            for conclusion in ("failure", "cancelled", "skipped", "neutral", "timed_out"):
                self.jobs = copy.deepcopy(original)
                self.jobs[index]["conclusion"] = conclusion
                with self.subTest(index=index, conclusion=conclusion), self.assertRaises(ValueError):
                    self.source()
            self.jobs = copy.deepcopy(original)
            self.jobs.pop(index)
            with self.subTest(missing=index), self.assertRaises(ValueError):
                self.source()
        self.jobs = copy.deepcopy(original)
        self.jobs[0]["run_attempt"] = 1
        with self.assertRaises(ValueError):
            self.source()

    def test_rejects_expired_modified_or_wrong_attempt_artifact(self):
        self.artifact["expired"] = True
        with self.assertRaises(ValueError):
            self.source()
        self.artifact["expired"] = False
        self.api.artifact.return_value = self.content + b"changed"
        with self.assertRaisesRegex(ValueError, "digest"):
            self.source()
        self.api.artifact.return_value = self.content
        self.artifact["created_at"] = "2026-10-12T01:00:30Z"
        with self.assertRaisesRegex(ValueError, "job attempt"):
            self.source()

    def test_source_rerun_during_lookup_invalidates_evidence(self):
        original = self.api.get.side_effect
        count = 0

        def changed(path, **params):
            nonlocal count
            result = original(path, **params)
            if path.endswith("/runs/42"):
                count += 1
                if count == 2:
                    result["run_attempt"] = 3
            return result

        self.api.get.side_effect = changed
        with self.assertRaisesRegex(ValueError, "changed"):
            self.source()

    def test_lookup_failure_or_missing_evidence_keeps_normal_ci(self):
        self.artifact["expired"] = True
        self.assertIsNone(self.find())
        self.api.get.side_effect = urllib.error.URLError("offline")
        self.assertIsNone(self.find())

    def test_discovery_does_not_hide_a_newer_failed_run_of_the_same_revision(self):
        self.records[self.root + "/actions/workflows/ci.yml/runs"]["workflow_runs"] = [
            dict(self.run, id=43, conclusion="failure"), self.run]
        self.assertIsNone(self.find())
        self.api.artifact.assert_not_called()

    def test_discovery_does_not_hide_failure_of_another_commit_with_the_same_tree(self):
        self.records[self.root + "/git/commits/" + self.merged] = {
            "sha": self.merged, "tree": {"sha": self.tree}}
        self.records[self.root + "/actions/workflows/ci.yml/runs"]["workflow_runs"] = [
            dict(self.run, id=43, head_sha=self.merged, conclusion="failure"), self.run]
        self.assertIsNone(self.find())
        self.api.artifact.assert_not_called()

    def test_discovery_ignores_newer_runs_for_other_trees(self):
        self.records[self.root + "/git/commits/" + self.merged] = {
            "sha": self.merged, "tree": {"sha": "f" * 40}}
        self.records[self.root + "/actions/workflows/ci.yml/runs"]["workflow_runs"] = [
            dict(self.run, id=43, head_sha=self.merged, conclusion="failure"), self.run]
        self.assertEqual(self.find()["run_id"], 42)

    def post_merge(self):
        run = dict(self.run, id=100, head_sha=self.merged, head_branch="main", event="push", run_attempt=1)
        jobs = [dict(self.jobs[index], id=50 + index, run_id=100, run_attempt=1, head_sha=self.merged, name=name)
                for index, name in enumerate(("selection", "verified"))]
        receipt = reuse.reuse_receipt(run, self.source(), repository=self.repository, sha=self.merged,
                                      tree=self.tree, branch="main", base_sha=self.base, manifest=self.manifest)
        artifact = dict(self.artifact, id=30, workflow_run={"id": 100, "head_sha": self.merged, "head_branch": "main"})
        return run, jobs, receipt, artifact

    def publication(self, run, jobs, receipt, artifact):
        content = self.archive(receipt)
        artifact["digest"] = "sha256:" + hashlib.sha256(content).hexdigest()
        return reuse.check_reused_evidence(self.api, run, jobs, artifact, content, repository=self.repository,
                                           sha=self.merged, tree=self.tree, base_sha=self.base, manifest=self.manifest)

    def test_publication_rechecks_original_jobs_and_artifact(self):
        values = self.post_merge()
        result = self.publication(*values)
        self.assertEqual(result["sha"], self.merged)
        self.assertEqual(result["reused_from"]["sha"], self.sha)
        self.jobs[0]["conclusion"] = "failure"
        with self.assertRaises(ValueError):
            self.publication(*values)
        self.jobs[0]["conclusion"] = "success"
        self.artifact["expired"] = True
        with self.assertRaises(ValueError):
            self.publication(*values)

    def test_publication_rejects_tampered_reuse_or_post_merge_gate(self):
        run, jobs, original, artifact = self.post_merge()
        for key, value in (("sha", self.sha), ("tree", "f" * 40), ("base_sha", "f" * 40),
                           ("run_attempt", 2), ("jobs", []), ("profile", "prose")):
            receipt = dict(original, **{key: value})
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.publication(run, jobs, receipt, artifact)
        for key, value in (("run_attempt", 1), ("artifact_id", 1), ("artifact_digest", "wrong")):
            receipt = copy.deepcopy(original)
            receipt["reused_from"][key] = value
            with self.subTest(source_key=key), self.assertRaises(ValueError):
                self.publication(run, jobs, receipt, artifact)
        for job in jobs:
            job["conclusion"] = "skipped"
            with self.subTest(job=job["name"]), self.assertRaises(ValueError):
                self.publication(run, jobs, original, artifact)
            job["conclusion"] = "success"

    def test_selection_restricts_reuse_to_ordinary_canonical_single_commit_pushes(self):
        event = {"ref": "refs/heads/main", "forced": False, "deleted": False,
                 "before": self.base, "after": self.merged, "repository": {"full_name": self.repository}}
        arguments = dict(repository=self.repository, sha=self.merged, parents=[self.base, self.sha])
        self.assertEqual(reuse.push_context("push", event, **arguments), {"branch": "main", "base_sha": self.base})
        for name in ("pull_request", "merge_group", "workflow_dispatch", "schedule"):
            with self.subTest(event=name):
                self.assertIsNone(reuse.push_context(name, event, **arguments))
        for key, value in (("ref", "refs/heads/feature"), ("ref", "refs/tags/main"),
                           ("forced", True), ("deleted", True), ("before", "e" * 40)):
            with self.subTest(key=key):
                self.assertIsNone(reuse.push_context("push", dict(event, **{key: value}), **arguments))
        event["ref"] = "refs/heads/maint/0.5"
        self.assertEqual(reuse.push_context("push", event, **arguments)["branch"], "maint/0.5")

    def test_workflow_keeps_default_full_path_and_rechecks_before_upload(self):
        workflow = yaml.safe_load((Path(__file__).parents[1] / "workflows/ci.yml").read_text())
        jobs = workflow["jobs"]
        self.assertEqual(jobs["full"]["if"], "needs.selection.outputs.source_run_id == ''")
        selection = next(step for step in jobs["selection"]["steps"] if step.get("id") == "reuse")
        self.assertEqual(selection["env"]["REUSE_MERGE_VERIFICATION"], "${{ vars.REUSE_MERGE_VERIFICATION }}")
        steps = jobs["verified"]["steps"]
        self.assertEqual(jobs["verified"]["if"], "always()")
        self.assertIn('github_reuse verify --run "$SOURCE_RUN"', steps[-2]["run"])
        self.assertEqual(steps[-1]["with"]["name"], "ci-verification")

    def test_disabled_selection_never_looks_up_or_reuses_evidence(self):
        event = {"ref": "refs/heads/main", "forced": False, "deleted": False,
                 "before": self.base, "after": self.merged, "repository": {"full_name": self.repository}}
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path / "event.json").write_text(json.dumps(event))
            environment = {"GITHUB_REPOSITORY": self.repository, "GITHUB_SHA": self.merged,
                           "GITHUB_EVENT_NAME": "push", "GITHUB_EVENT_PATH": str(path / "event.json"),
                           "GITHUB_OUTPUT": str(path / "output"), "GH_TOKEN": "test"}
            for enabled in (None, "false", "TRUE", "yes"):
                (path / "output").write_text("")
                with self.subTest(enabled=enabled), patch.dict(os.environ, environment, clear=True), \
                        patch("sys.argv", ["github_reuse", "select"]), \
                        patch.object(reuse, "checkout", return_value=(self.merged, self.tree, [self.base])), \
                        patch.object(reuse, "find_source") as lookup:
                    if enabled is not None:
                        os.environ["REUSE_MERGE_VERIFICATION"] = enabled
                    reuse.main()
                    lookup.assert_not_called()
                self.assertEqual((path / "output").read_text(), "source_run_id=\n")


if __name__ == "__main__":
    unittest.main()
