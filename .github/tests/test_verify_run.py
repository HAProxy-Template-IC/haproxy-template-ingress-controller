import copy
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import Mock


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

    def test_reusable_workflow_requires_its_own_jobs(self):
        nested = dict(self.job, name="full / unit", id=124)
        arguments = {"repository": "owner/repo", "sha": "abc", "attempt": 2, "prefix": "full / "}
        self.assertEqual(VERIFY.verify(self.run, [self.job, nested], ["unit"], **arguments),
                         [{"name": "full / unit", "id": 124}])
        with self.assertRaises(ValueError):
            VERIFY.verify(self.run, [self.job], ["unit"], **arguments)
        with self.assertRaises(ValueError):
            VERIFY.verify(self.run, [self.job, dict(nested, conclusion="failure")], ["unit"], **arguments)
        arguments["prefix"] = "full"
        with self.assertRaises(ValueError):
            VERIFY.verify(self.run, [nested], ["unit"], **arguments)


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

    def test_merge_queue_binds_requested_head_target_and_real_ancestry(self):
        event = {"repository": {"full_name": "owner/repo"}, "action": "checks_requested", "merge_group": {
            "head_sha": "a" * 40, "base_sha": "b" * 40, "base_ref": "refs/heads/main",
            "head_ref": "refs/heads/gh-readonly-queue/main/pr-43-" + "c" * 40,
        }}
        arguments = {"repository": "owner/repo", "tested_sha": "a" * 40, "parents": []}
        visited = []

        def ancestry(base, head):
            visited.append((base, head))
            return True

        result = VERIFY.revision_identity("merge_group", event, **arguments, is_ancestor=ancestry)
        self.assertEqual(result["base_branch"], "main")
        self.assertEqual(result["base_sha"], "b" * 40)
        self.assertEqual(visited, [("b" * 40, "a" * 40)])
        for predicate in (None, lambda base, head: False):
            with self.subTest(predicate=predicate), self.assertRaises(ValueError):
                VERIFY.revision_identity("merge_group", event, **arguments, is_ancestor=predicate)
        for key, value in (("head_sha", "c" * 40), ("base_sha", "a" * 40), ("base_sha", "--all"),
                           ("base_ref", "refs/heads/feature"), ("base_ref", "main"),
                           ("head_ref", "refs/heads/main"), ("head_ref", "refs/heads/gh-readonly-queue/other/pr-43")):
            altered = copy.deepcopy(event)
            altered["merge_group"][key] = value
            with self.subTest(key=key, value=value), self.assertRaises(ValueError):
                VERIFY.revision_identity("merge_group", altered, **arguments, is_ancestor=ancestry)
        event["action"] = "destroyed"
        with self.assertRaises(ValueError):
            VERIFY.revision_identity("merge_group", event, **arguments, is_ancestor=ancestry)


class VerificationProfileTests(unittest.TestCase):
    def setUp(self):
        self.manifest = json.loads((Path(__file__).parents[1] / "ci-verification.json").read_text())
        self.base, self.sha = "a" * 40, "b" * 40
        self.paths = Mock(return_value=["docs/site/docs/getting-started.md"])
        self.ancestor = Mock(return_value=True)

    def select(self, event_name="push", before=None, identity=None):
        return VERIFY.selection(self.manifest, event_name,
                                {"before": self.base if before is None else before},
                                {} if identity is None else identity, sha=self.sha,
                                is_ancestor=self.ancestor, changed_paths=self.paths)

    def test_only_the_existing_prose_allowlist_gets_focused_verification(self):
        for paths in (["README.md"], ["charts/haptic/README.md", "CHANGELOG.md"],
                      ["docs/site/docs/development/linting.md"]):
            with self.subTest(paths=paths):
                self.assertEqual(VERIFY.paths_profile(self.manifest, paths), "prose")
        for paths in ([], ["README.md", "pkg/controller/controller.go"], ["docs/site/mkdocs.yml"],
                      [".github/workflows/ci.yml"], ["docs/site/docs/example.yaml"],
                      ["scripts/generate.md"], ["charts/haptic/values.yaml"], ["VERSION"],
                      ["docs/site/docs/line\nbreak.md"], ["pkg/old.go", "docs/site/docs/new.md"]):
            with self.subTest(paths=paths):
                self.assertEqual(VERIFY.paths_profile(self.manifest, paths), "full")

    def test_push_pr_and_merge_group_use_their_real_comparison_base(self):
        for event_name in ("push", "pull_request", "merge_group"):
            result = self.select(event_name, identity={"base_sha": self.base})
            self.assertEqual(result, {"profile": "prose", "comparison_base": self.base,
                                      "changed_paths_sha256": VERIFY.paths_digest(self.paths.return_value)})
            self.paths.assert_called_with(self.base, self.sha)
        self.paths.return_value.append(".github/ci-verification.json")
        self.assertEqual(self.select()["profile"], "full")

    def test_new_branches_force_pushes_and_dispatches_require_full_verification(self):
        for before in ("0" * 40, "", "--all", "short"):
            with self.subTest(before=before):
                self.assertEqual(self.select(before=before), {"profile": "full"})
        self.ancestor.return_value = False
        self.assertEqual(self.select(), {"profile": "full"})
        for event_name in ("schedule", "workflow_dispatch"):
            self.assertEqual(self.select(event_name), {"profile": "full"})
        self.paths.assert_not_called()
        for event_name in ("pull_request", "merge_group"):
            with self.subTest(event_name=event_name), self.assertRaises(ValueError):
                self.select(event_name, identity={"base_sha": self.base})

    def test_profile_inventory_rejects_unknown_or_incomplete_policy(self):
        self.assertEqual(VERIFY.required_jobs(self.manifest, "full"), self.manifest["required_jobs"])
        for selected in ([], ["lint", "lint"], ["missing-job"]):
            self.manifest["prose"]["required_jobs"] = selected
            with self.subTest(selected=selected), self.assertRaises(ValueError):
                VERIFY.required_jobs(self.manifest, "prose")
        for profile in ("", "skip", "docs"):
            with self.subTest(profile=profile), self.assertRaises(ValueError):
                VERIFY.required_jobs(self.manifest, profile)


if __name__ == "__main__":
    unittest.main()
