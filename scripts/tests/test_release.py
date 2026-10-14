import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]


class ReleaseTests(unittest.TestCase):
    def test_latest_moves_only_to_the_highest_stable_release(self):
        tags = "v0.1.0-alpha.9\nv0.2.0\nv0.2.1\nv0.3.0-alpha.1\nv0.10.0-rc.1\n"
        ls_remote = "".join(f"{i:040x}\trefs/tags/{t}\n" for i, t in enumerate(tags.split()))
        cases = [
            ("highest stable", "v0.2.1", tags, 0),
            ("older stable", "v0.2.0", tags, 1),
            ("prerelease", "v0.3.0-alpha.1", tags, 1),
            ("prerelease above every stable", "v0.10.0-rc.1", tags, 1),
            ("maintenance release below a newer line", "v0.2.2", tags + "v0.2.2\nv0.3.0\n", 1),
            ("maintenance release of the newest line", "v0.2.2", tags + "v0.2.2\n", 0),
            ("numeric not lexical order", "v0.10.0", tags + "v0.9.0\nv0.10.0\n", 0),
            ("ls-remote input", "v0.2.1", ls_remote, 0),
            ("tag missing from input", "v0.2.2", tags, 2),
            ("empty input", "v0.2.1", "", 2),
        ]
        for name, tag, stdin, code in cases:
            with self.subTest(name=name):
                result = subprocess.run(["bash", str(ROOT / "scripts/release-is-latest.sh"), tag],
                                        input=stdin, capture_output=True, text=True)
                self.assertEqual(result.returncode, code, result.stderr)

    def test_release_commits_updated_pins_and_preserves_historical_upgrades(self):
        upgrade_notes = (ROOT / "docs/site/docs/upgrade-notes.md").read_text()
        upgrade_04, historical = upgrade_notes.split("## Upgrading to 0.3\n", 1)
        historical = (
            "## Upgrading to 0.3\n\n"
            "~~~markdown\n## Current installation\nhelm upgrade haptic --version 0.3.0\n~~~\n"
            + historical
        )
        with tempfile.TemporaryDirectory(prefix="haptic-release-test-") as temp:
            repo = Path(temp)
            files = {
                "go.mod": "module example.test/release\n",
                "VERSION": "0.1.0\n",
                "versions.env": 'DEFAULT_HAPROXY="3.4"\n',
                "CHANGELOG.md": "# Changelog\n\n## [Unreleased]\n\n### Fixed\n\n- A fix.\n",
                "charts/haptic/Chart.yaml": (
                    'version: 0.1.0\nappVersion: "0.1.0"\n'
                    'annotations:\n  artifacthub.io/images: |\n'
                    '    - name: varnish\n'
                    '      image: ghcr.io/haproxy-haptic/haptic/varnish:0.1.0\n'
                ),
                "charts/haptic/README.md": (
                    "https://haproxy-haptic.org/docs/dev/\n"
                    "https://github.com/haproxy-haptic/HAPTIC/blob/main/README.md\n"
                ),
                "charts/haptic/values.yaml": (
                    "# https://github.com/haproxy-haptic/HAPTIC/blob/main/README.md\n"
                    "# https://github.com/haproxy-haptic/HAPTIC/blob/v0.1.0/README.md\n"
                ),
                "charts/haptic/templates/NOTES.txt": "https://haproxy-haptic.org/docs/dev/\n",
                "README.md": "helm install haptic --version 0.1.0\n",
                "docs/site/docs/install.md": "helm upgrade haptic --version 0.1.0\n",
                "docs/site/docs/upgrade-notes.md": (
                    "helm pull chart --version 0.1.0\n"
                    "haptic preflight --chart chart --expect-chart-version 0.1.0\n"
                    "Upgrade from 0.1.0.\n"
                    + upgrade_04 + historical +
                    "\n## Current installation\n"
                    "````markdown\n```\n## Upgrading to 9.9\n```\n````\n"
                    "helm install haptic --version 0.1.0\n"
                    "docker run registry.test/haptic:0.1.0-haproxy3.0\n"
                ),
                "docs/landing/overrides/home.html": '<span id="helm-version" class="t-num">0.1.0</span>\n',
            }
            for name, content in files.items():
                target = repo / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content)
            (repo / "scripts").mkdir()
            shutil.copyfile(ROOT / "scripts/release.sh", repo / "scripts/release.sh")
            shutil.copyfile(ROOT / "scripts/update-release-docs.py", repo / "scripts/update-release-docs.py")
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
            for version in ("0.2.0-alpha.2", "0.2.0", "0.3.0", "0.4.0", "0.4.1", "0.5.0"):
                with self.subTest(version=version):
                    run("bash", "scripts/release.sh", version)
                    self.assertEqual(run("git", "status", "--porcelain"), "")
                    for path in ("charts/haptic/README.md", "charts/haptic/values.yaml"):
                        committed = run("git", "show", "HEAD:" + path)
                        self.assertIn(
                            "https://github.com/haproxy-haptic/HAPTIC/"
                            f"blob/v{version}/README.md", committed,
                        )
                        self.assertNotIn("/blob/main/", committed)
                    self.assertIn(f"haptic/varnish:{version}\n", run("git", "show", "HEAD:charts/haptic/Chart.yaml"))
                    committed = run("git", "show", "HEAD:docs/site/docs/upgrade-notes.md")
                    self.assertIn(f"--version {version}\n", committed)
                    self.assertIn(f"--expect-chart-version {version}\n", committed)
                    self.assertIn("Upgrade from 0.1.0.\n", committed)
                    self.assertIn(historical, committed)
                    expected_04 = upgrade_04
                    if version in ("0.4.1", "0.5.0"):
                        expected_04 = expected_04.replace("--version 0.4.0", "--version 0.4.1")
                    self.assertIn(expected_04, committed)
                    self.assertIn(f"helm install haptic --version {version}\n", committed)
                    self.assertIn(f"registry.test/haptic:{version}-haproxy3.4\n", committed)


if __name__ == "__main__":
    unittest.main()
