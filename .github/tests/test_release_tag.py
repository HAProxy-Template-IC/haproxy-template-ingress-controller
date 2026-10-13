import copy
import unittest
import urllib.error

from scripts.ci.github_release_tag import release_plan, tag_and_dispatch


class ReleaseTagTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.receipt = {"version": 2, "repository": "owner/repo", "sha": self.sha,
                        "jobs": [{"name": "full / unit", "id": 10}], "branch": "main", "run_id": 42}
        self.chart = {"version": "1.2.3", "appVersion": "1.2.3"}

    def plan(self):
        return release_plan("1.2.3", self.chart, self.receipt, repository="owner/repo", sha=self.sha)

    def test_requires_matching_release_files_and_verified_source(self):
        self.assertEqual(self.plan(), {"tag": "v1.2.3", "sha": self.sha, "run_id": 42})
        for field in ("version", "appVersion"):
            self.chart[field] = "1.2.4"
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.plan()
            self.chart[field] = "1.2.3"
        original = copy.deepcopy(self.receipt)
        for field, value in (("sha", "b" * 40), ("repository", "fork/repo"), ("branch", "feature"), ("jobs", [])):
            self.receipt = dict(original, **{field: value})
            with self.subTest(field=field), self.assertRaises(ValueError):
                self.plan()

    def test_dispatch_follows_verified_tag_creation(self):
        api = FakeAPI(self.sha)
        result = tag_and_dispatch(api, "owner/repo", self.plan())
        self.assertTrue(result["created"] and result["dispatched"])
        self.assertEqual([path for path, body in api.posts], [
            "repos/owner/repo/git/tags", "repos/owner/repo/git/refs",
            "repos/owner/repo/actions/workflows/release.yml/dispatches"])
        self.assertEqual(api.posts[-1][1], {"ref": "v1.2.3", "inputs": {"verification_run": "42"}})

    def test_documentation_only_evidence_cannot_tag_a_release(self):
        self.receipt["profile"] = "prose"
        with self.assertRaises(ValueError):
            self.plan()

    def test_existing_release_version_and_lookup_failure_never_create_a_tag(self):
        for status in (200, 403, 503):
            api = FakeAPI(self.sha, status)
            if status == 200:
                self.assertFalse(tag_and_dispatch(api, "owner/repo", self.plan())["created"])
            else:
                with self.subTest(status=status), self.assertRaises(urllib.error.HTTPError):
                    tag_and_dispatch(api, "owner/repo", self.plan())
            self.assertEqual(api.posts, [])

    def test_different_tag_target_cannot_dispatch_publication(self):
        api = FakeAPI("b" * 40)
        with self.assertRaisesRegex(ValueError, "different object"):
            tag_and_dispatch(api, "owner/repo", self.plan())
        self.assertEqual(len(api.posts), 1)


class FakeAPI:
    def __init__(self, sha, status=404):
        self.sha, self.status, self.posts = sha, status, []

    def get(self, path):
        if self.status != 200 and not self.posts:
            raise urllib.error.HTTPError("https://api.github.com/" + path, self.status, "lookup", {}, None)
        return {"object": {"sha": "annotation"}}

    def post(self, path, body):
        self.posts.append((path, body))
        return {"sha": "annotation", "object": {"type": "commit", "sha": self.sha,
                "url": "https://api.github.com/repos/owner/repo/git/commits/" + self.sha}}


if __name__ == "__main__":
    unittest.main()
