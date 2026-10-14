import base64
import copy
import hashlib
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch
import urllib.error

import yaml

from scripts.ci import github_site_state as storage
from scripts.ci.github_site_inputs import check_artifact, download_inputs
from scripts.ci.github_site_publication import publication_source, retain
from scripts.ci.github_pages import record


ROOT = Path(__file__).parents[2]
REPOSITORY = "owner/project"
BASELINE = {"url": "pinned-baseline", "sha256": "b" * 64, "size": 10}


def entry(version="dev", sha="a" * 40):
    digest = "c" * 64
    return {"url": f"https://github.com/{REPOSITORY}/releases/download/{storage.TAG}/website-{version}-{digest}.tar.gz",
            "size": 100, "sha256": digest,
            "provenance": {"version": version, "source": {"repository": REPOSITORY, "sha": sha}}}


class WebsiteStateTests(unittest.TestCase):
    def setUp(self):
        self.state = {"version": 1, "baseline": BASELINE, "releases": {}, "dev": None}
        self.api = Mock()

    def test_missing_branch_bootstraps_only_from_the_pinned_baseline(self):
        self.api.get.side_effect = urllib.error.HTTPError("url", 404, "missing", {}, io.BytesIO())
        self.assertEqual(storage.read(self.api, REPOSITORY, BASELINE), (None, self.state))
        self.api.get.side_effect = urllib.error.HTTPError("url", 403, "denied", {}, io.BytesIO())
        with self.assertRaises(urllib.error.HTTPError):
            storage.read(self.api, REPOSITORY, BASELINE)

    def test_catalog_read_is_pinned_to_one_git_commit(self):
        content = json.dumps(self.state).encode()
        self.api.get.side_effect = [
            {"object": {"sha": "head"}},
            {"truncated": False, "tree": [{"path": "catalog.json", "type": "blob", "mode": "100644", "sha": "blob"}]},
            {"encoding": "base64", "size": len(content), "content": base64.b64encode(content).decode()}]
        self.assertEqual(storage.read(self.api, REPOSITORY, BASELINE), ("head", self.state))
        self.assertEqual(self.api.get.call_args_list[1].args, (f"repos/{REPOSITORY}/git/trees/head",))

    def test_website_versions_are_immutable_and_development_must_advance(self):
        released = entry("1.0.0")
        initial = storage.advance(self.api, REPOSITORY, self.state, released)
        self.assertEqual(storage.advance(self.api, REPOSITORY, initial, released), initial)
        changed = copy.deepcopy(released)
        changed["size"] += 1
        with self.assertRaisesRegex(ValueError, "cannot be replaced"):
            storage.advance(self.api, REPOSITORY, initial, changed)
        current = storage.advance(self.api, REPOSITORY, initial, entry())
        for status in ("behind", "diverged"):
            self.api.get.return_value = {"status": status}
            with self.assertRaisesRegex(ValueError, "roll back or diverge"):
                storage.advance(self.api, REPOSITORY, current, entry(sha="d" * 40))
        self.api.get.return_value = {"status": "ahead"}
        updated = storage.advance(self.api, REPOSITORY, current, entry(sha="d" * 40))
        self.assertEqual(updated["releases"], current["releases"])
        self.assertNotEqual(updated["dev"], current["dev"])

    def test_catalog_rejects_other_repositories_urls_and_baselines(self):
        state = storage.advance(self.api, REPOSITORY, self.state, entry())
        for section, field, value in (("dev", "url", "https://other.example/archive"),
                                       ("dev", "sha256", "bad"), ("dev", "size", 0),
                                       ("baseline", "size", 20)):
            with self.subTest(field=field):
                altered = copy.deepcopy(state)
                altered[section][field] = value
                with self.assertRaises(ValueError):
                    storage.validate(altered, BASELINE, REPOSITORY)
        with self.assertRaises(ValueError):
            storage.validate(state, BASELINE, "another/repo")

    def test_concurrent_catalog_changes_never_force_an_update(self):
        with patch.object(storage, "read", return_value=("other", self.state)):
            with self.assertRaisesRegex(ValueError, "changed while"):
                storage.write(self.api, REPOSITORY, "expected", self.state)
        self.api.post.assert_not_called()
        self.api.request.assert_not_called()
        self.api.post.side_effect = [{"sha": "tree"}, {"sha": "next"}]
        with patch.object(storage, "read", side_effect=[("expected", self.state), ("next", self.state)]):
            self.assertEqual(storage.write(self.api, REPOSITORY, "expected", self.state), "next")
        self.api.request.assert_called_once_with("PATCH", f"repos/{REPOSITORY}/git/refs/heads/website-state",
                                                 {"sha": "next", "force": False})

    def test_retention_rechecks_source_before_any_upload(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            (directory / "request.json").write_text(json.dumps({"entry": entry()}))
            with self.assertRaisesRegex(ValueError, "verification changed"):
                retain(self.api, REPOSITORY, {"sha": "different"}, directory)
        self.api.assert_not_called()
        self.api.get.assert_not_called()

    def test_archive_upload_uses_content_addressed_name_without_replacement(self):
        with tempfile.TemporaryDirectory() as temporary:
            scratch = Path(temporary)
            bundle = scratch / "content.tar.gz"
            bundle.write_bytes(b"test archive")
            digest = record(bundle)
            name = f"website-dev-{digest['sha256']}.tar.gz"
            url = f"https://github.com/{REPOSITORY}/releases/download/{storage.TAG}/{name}"
            asset = {"name": name, "size": digest["size"], "digest": "sha256:" + digest["sha256"],
                     "state": "uploaded", "browser_download_url": url}
            provenance = entry()["provenance"]
            self.api.get.return_value = {"id": 7, "tag_name": storage.TAG, "draft": False, "prerelease": True}
            self.api.all.side_effect = [[], [asset]]
            with patch.object(storage.subprocess, "run") as run, patch.object(storage, "download") as download:
                download.return_value = {"provenance": provenance}
                result = storage.upload(self.api, REPOSITORY, bundle, provenance, scratch)
            run.assert_called_once_with(["gh", "release", "upload", storage.TAG, str(scratch / name),
                                         "--repo", REPOSITORY], check=True)
            self.assertEqual((scratch / name).read_bytes(), bundle.read_bytes())
            self.assertEqual(result, {**digest, "url": url, "provenance": provenance})
            self.assertEqual(download.call_args.args[0], result)
            self.api.all.side_effect = [[{**asset, "digest": "sha256:wrong"}]]
            with patch.object(storage.subprocess, "run") as run, patch.object(storage, "download") as download:
                with self.assertRaisesRegex(ValueError, "differs from its local source"):
                    storage.upload(self.api, REPOSITORY, bundle, provenance, scratch)
                run.assert_not_called()
                download.assert_not_called()


class WebsiteArtifactTests(unittest.TestCase):
    def setUp(self):
        self.content = b"artifact archive"
        self.run = {"id": 1, "head_sha": "a" * 40, "head_branch": "main", "run_attempt": 2}
        self.job = {"run_id": 1, "head_sha": "a" * 40, "run_attempt": 2, "conclusion": "success",
                    "status": "completed", "started_at": "2026-10-14T01:00:00Z", "completed_at": "2026-10-14T01:05:00Z"}
        self.artifact = {"name": "ci-evidence-docs", "expired": False,
                         "workflow_run": {"id": 1, "head_sha": "a" * 40, "head_branch": "main"},
                         "created_at": "2026-10-14T01:04:00Z",
                         "digest": "sha256:" + hashlib.sha256(self.content).hexdigest()}

    def test_exact_producing_attempt_and_digest_are_required(self):
        check_artifact(self.artifact, self.job, self.run, "docs", self.content)
        changes = (("expired", True), ("name", "ci-evidence-wasm"),
                   ("created_at", "2026-10-14T00:04:00Z"), ("digest", "sha256:wrong"))
        for key, value in changes:
            with self.subTest(key=key), self.assertRaises(ValueError):
                check_artifact({**self.artifact, key: value}, self.job, self.run, "docs", self.content)
        for key, value in (("run_attempt", 1), ("conclusion", "skipped"), ("head_sha", "b" * 40)):
            with self.subTest(key=key), self.assertRaises(ValueError):
                check_artifact(self.artifact, {**self.job, key: value}, self.run, "docs", self.content)

    def test_rerun_source_is_rejected_before_artifact_download(self):
        api = Mock()
        api.get.return_value = {**self.run, "run_attempt": 3}
        receipt = {"run_id": 1, "run_attempt": 2, "sha": "a" * 40}
        with self.assertRaisesRegex(ValueError, "source changed"):
            download_inputs(api, REPOSITORY, receipt, Path("unused"))
        api.artifact.assert_not_called()

    def test_development_and_release_sources_are_bound_to_checkout_and_tag(self):
        receipt = {"repository": REPOSITORY, "sha": "a" * 40, "tree": "b" * 40, "branch": "main", "profile": "full"}
        with patch("scripts.ci.github_site_publication.subprocess.check_output", side_effect=["a" * 40, "b" * 40]):
            publication_source(receipt, REPOSITORY, "dev")
        with patch("scripts.ci.github_site_publication.subprocess.check_output", side_effect=["a" * 40, "b" * 40]):
            with self.assertRaisesRegex(ValueError, "verified main"):
                publication_source({**receipt, "branch": "maint/1.0"}, REPOSITORY, "dev")
        with patch("scripts.ci.github_site_publication.subprocess.check_output", side_effect=["a" * 40, "b" * 40, "c" * 40]):
            with self.assertRaisesRegex(ValueError, "matching verified release"):
                publication_source(receipt, REPOSITORY, (ROOT / "VERSION").read_text().strip())


class WebsiteWorkflowTests(unittest.TestCase):
    def test_publication_is_inactive_until_cutover_and_archives_before_deploying(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/pages.yml").read_text())
        jobs = workflow["jobs"]
        self.assertIn("vars.MIGRATION_ACTIVE == 'true'", jobs["build"]["if"])
        self.assertEqual(jobs["retain"]["needs"], "build")
        self.assertEqual(jobs["deploy"]["needs"], "retain")
        self.assertEqual(jobs["verify"]["needs"], ["build", "deploy"])
        self.assertFalse(workflow["concurrency"]["cancel-in-progress"])
        self.assertEqual(jobs["deploy"]["environment"]["name"], "github-pages")
        release = yaml.safe_load((ROOT / ".github/workflows/release.yml").read_text())
        self.assertEqual(release["jobs"]["website"]["needs"], ["source", "chart", "conformance"])
