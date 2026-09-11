import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]


class ReleaseTests(unittest.TestCase):
    def test_chart_publication_waits_for_complete_signed_runtime(self):
        config = yaml.compose((ROOT / ".gitlab-ci.yml").read_text())
        jobs = {key.value: value for key, value in config.value}
        chart = {key.value: value for key, value in jobs["release-chart"].value}
        needs = set()
        for node in chart["needs"].value:
            if isinstance(node, yaml.ScalarNode):
                needs.add(node.value)
            else:
                fields = {key.value: value.value for key, value in node.value}
                self.assertNotEqual(fields.get("optional"), "true")
                needs.add(fields["job"])
        self.assertTrue({
            "release-controller", "sign-spoa-image", "attest-spoa-sbom",
            "smoke-spoa-amd64", "smoke-spoa-arm64",
        }.issubset(needs), needs)
        publisher = {key.value: value for key, value in jobs["prepare-spoa-release"].value}
        self.assertEqual(publisher["resource_group"].value, "spoa-release-publish")
        self.assertEqual(publisher["script"].value[0].value, "bash scripts/spoa-release-image.sh prepare")
        release = {key.value: value for key, value in jobs["build-spoa-image-release"].value}
        self.assertEqual(release["script"].value[0].value, "bash scripts/spoa-release-image.sh verify")

    def test_release_commits_every_rewritten_chart_file(self):
        with tempfile.TemporaryDirectory(prefix="haptic-release-test-") as temp:
            repo = Path(temp)
            files = {
                "go.mod": "module example.test/release\n",
                "VERSION": "0.1.0\n",
                "versions.env": 'DEFAULT_HAPROXY="3.4"\n',
                "CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- A fix.\n",
                "charts/haptic/Chart.yaml": 'version: 0.1.0\nappVersion: "0.1.0"\n',
                "charts/haptic/README.md": "https://haproxy-haptic.org/docs/dev/\n",
                "charts/haptic/values.yaml": "# https://gitlab.com/haproxy-haptic/haptic/-/blob/main/README.md\n",
                "charts/haptic/templates/NOTES.txt": "https://haproxy-haptic.org/docs/dev/\n",
                "README.md": "helm install haptic --version 0.1.0\n",
                "docs/site/docs/install.md": "helm upgrade haptic --version 0.1.0\n",
                "docs/landing/overrides/home.html": '<span id="helm-version" class="t-num">0.1.0</span>\n',
            }
            for name, content in files.items():
                target = repo / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content)
            (repo / "scripts").mkdir()
            shutil.copyfile(ROOT / "scripts/release.sh", repo / "scripts/release.sh")
            env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
            env.update({
                "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_AUTHOR_NAME": "Release test", "GIT_AUTHOR_EMAIL": "release@example.test",
                "GIT_COMMITTER_NAME": "Release test", "GIT_COMMITTER_EMAIL": "release@example.test",
            })

            def run(*args):
                return subprocess.run(args, cwd=repo, env=env, check=True, capture_output=True, text=True).stdout

            run("git", "init", "--initial-branch=main")
            run("git", "add", ".")
            run("git", "commit", "-m", "Initial fixture")
            run("bash", "scripts/release.sh", "0.2.0-alpha.2")
            self.assertEqual(run("git", "status", "--porcelain"), "")
            self.assertEqual(run("git", "show", "HEAD:VERSION").strip(), "0.2.0-alpha.2")
            for path in ("charts/haptic/README.md", "charts/haptic/values.yaml", "charts/haptic/templates/NOTES.txt"):
                with self.subTest(path=path):
                    committed = run("git", "show", "HEAD:" + path)
                    self.assertIn("0.2.0-alpha.2", committed)
                    self.assertNotIn("/docs/dev/", committed)
                    self.assertNotIn("/blob/main/", committed)


if __name__ == "__main__":
    unittest.main()
