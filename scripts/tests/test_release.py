import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

import yaml


ROOT = Path(__file__).resolve().parents[2]


class ReleaseTests(unittest.TestCase):
    def test_release_install_jobs_wait_for_runtime_image_preparation(self):
        config = yaml.safe_load((ROOT / ".gitlab-ci.yml").read_text().replace("!reference", ""))
        preparation = config["prepare-spoa-release"]
        manual_rules = [rule for rule in preparation["rules"]
                        if isinstance(rule, dict) and rule.get("when") == "manual"]
        self.assertTrue(manual_rules)
        for rule in manual_rules:
            self.assertIs(rule["allow_failure"], False)
        for name, job in config.items():
            if not isinstance(job, dict):
                continue
            needs = {need if isinstance(need, str) else need["job"]: need
                     for need in job.get("needs", [])}
            if "build-snapshot" not in needs:
                continue
            with self.subTest(job=name):
                self.assertEqual(needs.get("prepare-spoa-release"), {
                    "job": "prepare-spoa-release", "optional": True, "artifacts": False,
                })

    def test_release_tag_lookup_distinguishes_present_absent_and_failure(self):
        config = yaml.compose((ROOT / ".gitlab-ci.yml").read_text())
        jobs = {key.value: value for key, value in config.value}
        job = {key.value: value for key, value in jobs["create-release-tag"].value}
        lookup = next(node.value for node in job["script"].value if node.value.startswith("if git ls-remote"))
        with tempfile.TemporaryDirectory(prefix="haptic-release-tags-") as temp:
            repo = Path(temp)
            env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
            env.update({
                "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull,
                "GIT_AUTHOR_NAME": "Release test", "GIT_AUTHOR_EMAIL": "release@example.test",
                "GIT_COMMITTER_NAME": "Release test", "GIT_COMMITTER_EMAIL": "release@example.test",
                "TAG": "v0.2.0-alpha.2",
            })

            def git(*args, **kwargs):
                return subprocess.run(["git", *args], cwd=repo, env=env, check=True,
                                      capture_output=True, text=True, **kwargs).stdout

            git("init", "--initial-branch=main")
            git("commit", "--allow-empty", "-m", "Fixture")
            git("remote", "add", "origin", str(repo))
            commit = git("rev-parse", "HEAD").strip()
            refs = ["refs/tags/v0.2.0-alpha.2", *[f"refs/tags/v9.fixture-{i:05d}" for i in range(4096)]]
            git("update-ref", "--stdin", input="".join(f"update {ref} {commit}\n" for ref in refs))
            cases = [
                ("present", "v0.2.0-alpha.2", str(repo), 0, "Tag v0.2.0-alpha.2 already exists, skipping\n"),
                ("absent", "v0.2.0-alpha.3", str(repo), 0, "tag absent\n"),
                ("lookup failure", "v0.2.0-alpha.2", str(repo / "missing.git"), 128, ""),
            ]
            for name, tag, remote, code, output in cases:
                with self.subTest(name=name):
                    git("remote", "set-url", "origin", remote)
                    result = subprocess.run(["bash", "-euo", "pipefail", "-c", lookup + '\necho "tag absent"'],
                                            cwd=repo, env={**env, "TAG": tag}, capture_output=True, text=True)
                    self.assertEqual(result.returncode, code, result.stderr)
                    self.assertEqual(result.stdout, output, result.stderr)

    def test_release_attests_each_images_own_repository(self):
        config = yaml.compose((ROOT / ".gitlab-ci.yml").read_text())
        jobs = {key.value: value for key, value in config.value}
        job = {key.value: value for key, value in jobs["release-controller"].value}
        script = next(node.value for node in job["script"].value
                      if node.value.startswith("for IMAGE_VARIANT"))
        with tempfile.TemporaryDirectory(prefix="haptic-image-sboms-") as temp:
            root = Path(temp)
            for name, body in {
                "docker": "printf 'sha256:fixture\\n'",
                "syft": 'printf "syft %s\\n" "$1" >> "$TEST_CALLS"; printf "{}\\n"',
                "cosign": 'printf "cosign %s\\n" "$*" >> "$TEST_CALLS"',
            }.items():
                command = root / name
                command.write_text("#!/bin/sh\n" + body + "\n")
                command.chmod(0o755)
            env = {**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"],
                   "TEST_CALLS": str(root / "calls"), "CI_REGISTRY_IMAGE": "registry.test/haptic",
                   "VERSION": "v1.2.3", "HAPROXY_VERSIONS": "3.4"}
            subprocess.run(["bash", "-euo", "pipefail", "-c", script], cwd=root, env=env,
                           capture_output=True, text=True, check=True)
            calls = (root / "calls").read_text().splitlines()
            self.assertEqual([line for line in calls if line.startswith("syft ")], [
                "syft registry.test/haptic@sha256:fixture",
                "syft registry.test/haptic/varnish@sha256:fixture",
            ])
            self.assertTrue(any(line.endswith("registry.test/haptic/varnish@sha256:fixture")
                                for line in calls if line.startswith("cosign ")))

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

    def test_maint_branches_tag_releases_and_latest_tags_check_the_order(self):
        config = yaml.safe_load((ROOT / ".gitlab-ci.yml").read_text().replace("!reference", ""))
        maint_rule = "$CI_COMMIT_BRANCH =~ /^maint\\//"
        self.assertIn({"if": maint_rule}, config["workflow"]["rules"])
        tag_rules = [rule.get("if", "") for rule in config["create-release-tag"]["rules"] if isinstance(rule, dict)]
        self.assertTrue(any(maint_rule in rule for rule in tag_rules), tag_rules)
        self.assertNotIn("needs", config["create-release-tag"])
        for job in ("trigger-pages", ".rules-main-snapshot-publish", "build-playground-wasm"):
            with self.subTest(job=job):
                self.assertNotIn("maint", yaml.safe_dump(config[job]["rules"]))
        for job in ("release-controller", "build-spoa-image-release"):
            with self.subTest(job=job):
                script = "\n".join(config[job]["script"])
                self.assertIn("scripts/release-is-latest.sh", script)
                self.assertNotIn("alpha|beta|rc", script)
                self.assertNotIn("latest", "\n".join(config[job].get("after_script", [])))

    def test_chart_publication_waits_for_complete_signed_runtime(self):
        config = yaml.compose((ROOT / ".gitlab-ci.yml").read_text())
        jobs = {key.value: value for key, value in config.value}
        chart = {key.value: value for key, value in jobs["release-chart"].value}
        builder = {key.value: value for key, value in jobs[".goreleaser-base"].value}
        variables = {key.value: value.value for key, value in builder["variables"].value}
        self.assertEqual(variables["BUILDX_NO_DEFAULT_OCI_ARTIFACT"], "true")
        self.assertNotIn("BUILDX_NO_DEFAULT_ATTESTATIONS", variables)
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
                    '      image: registry.gitlab.com/haproxy-haptic/haptic/varnish:0.1.0\n'
                ),
                "charts/haptic/README.md": "https://haproxy-haptic.org/docs/dev/\n",
                "charts/haptic/values.yaml": "# https://gitlab.com/haproxy-haptic/haptic/-/blob/main/README.md\n",
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
