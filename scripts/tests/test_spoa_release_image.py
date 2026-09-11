import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
DIGEST = "sha256:" + "a" * 64
HUB_DIGEST = "sha256:" + "b" * 64
DOCKER_STUB = r'''
import json, os, pathlib, sys
args = sys.argv[1:]
mode = os.environ["TEST_MODE"]
state = pathlib.Path(os.environ["TEST_STATE"])
with (state / "calls").open("a") as stream:
    stream.write(" ".join(args) + "\n")
digest = "sha256:" + "a" * 64
if args[:3] == ["buildx", "imagetools", "inspect"]:
    image = args[3]
    if "/haproxy-spoa-hub:" in image:
        print(json.dumps("sha256:" + "b" * 64))
    elif "--raw" in args:
        platforms = [{"os": "linux", "architecture": "amd64"},
                     {"os": "linux", "architecture": "arm64"},
                     {"os": "linux", "architecture": "arm", "variant": "v7"}]
        if mode == "missing-platform":
            platforms.pop()
        print(json.dumps({"manifests": [dict(platform=p, digest="sha256:" + key * 64)
                                       for key, p in zip("cde", platforms)]}))
    elif any("@sha256:" + key * 64 in image for key in "cde"):
        wrong = mode == "wrong-inputs" or (mode == "wrong-arm-inputs" and image.endswith("e" * 64))
        value = "wrong" if wrong else os.environ["TEST_INPUTS_HASH"]
        print(json.dumps({"config": {"Labels": {
            "org.haproxy-haptic.spoa.inputs-sha256": value,
            "org.opencontainers.image.version": "0.2.0-alpha.2"}}}))
    elif mode == "auth-error":
        print("unauthorized: authentication required", file=sys.stderr)
        sys.exit(1)
    elif mode == "command-not-found":
        print("helper-command: not found", file=sys.stderr)
        sys.exit(1)
    elif mode == "missing" and ":ci-spoa-prepare-" not in image and not (state / "published").exists():
        print("ERROR: " + image + ": not found", file=sys.stderr)
        sys.exit(1)
    else:
        print(json.dumps(digest))
elif args[:3] == ["buildx", "imagetools", "create"]:
    (state / "published").touch()
elif args[:2] != ["buildx", "build"]:
    sys.exit("unexpected Docker command")
'''


class SPOAReleaseImageTests(unittest.TestCase):
    def run_image_script(self, action, mode, branch="release/v0.2.0-alpha.2"):
        with tempfile.TemporaryDirectory(prefix="haptic-spoa-release-test-") as temp:
            repo = Path(temp)
            files = {
                "VERSION": "0.2.0-alpha.2\n",
                "Dockerfile.spoa-hub": "FROM example.test/hub\n",
                ".dockerignore": "ignored\n",
                "versions-spoa.env": 'SPOA_HUB_VERSION="v1.2.3"\n',
                "charts/haptic/Chart.yaml": 'version: 0.2.0-alpha.2\nappVersion: "0.2.0-alpha.2"\n',
                "charts/haptic/values.yaml": 'spoaHub:\n  image:\n    repository: example.test/haptic/spoa-hub\n    tag: ""\n',
                **{f"plugins/{arch}/test.so": arch for arch in ("amd64", "arm64", "armv7")},
            }
            for name, content in files.items():
                target = repo / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content)
            (repo / "scripts").mkdir()
            shutil.copyfile(ROOT / "scripts/spoa-release-image.sh", repo / "scripts/spoa-release-image.sh")
            (repo / "bin").mkdir()
            docker = repo / "bin/docker"
            docker.write_text(f"#!{sys.executable}\n" + DOCKER_STUB)
            docker.chmod(0o755)
            names = ["Dockerfile.spoa-hub", ".dockerignore", "versions-spoa.env"]
            names += sorted(name for name in files if name.startswith("plugins/"))
            manifest = HUB_DIGEST + "\n" + "".join(
                hashlib.sha256(files[name].encode()).hexdigest() + "  " + name + "\n" for name in names
            )
            if mode == "changed-plugin":
                (repo / "plugins/amd64/test.so").write_text("changed plugin bytes")
            env = os.environ | {
                "PATH": str(repo / "bin") + os.pathsep + os.environ["PATH"],
                "CI_REGISTRY_IMAGE": "example.test/haptic", "CI_JOB_ID": "123",
                "CI_MERGE_REQUEST_SOURCE_BRANCH_NAME": branch,
                "CI_PROJECT_URL": "https://example.test/haptic", "CI_COMMIT_SHA": "source-sha",
                "TEST_MODE": mode, "TEST_STATE": str(repo),
                "TEST_INPUTS_HASH": hashlib.sha256(manifest.encode()).hexdigest(),
            }
            result = subprocess.run(["bash", "scripts/spoa-release-image.sh", action], cwd=repo,
                                    env=env, text=True, capture_output=True, check=False)
            calls = (repo / "calls").read_text() if (repo / "calls").exists() else ""
            metadata = json.loads((repo / "metadata.json").read_text()) if (repo / "metadata.json").exists() else None
            return result, calls, metadata

    def test_verified_release_is_never_rebuilt(self):
        for action in ("prepare", "verify"):
            with self.subTest(action=action):
                result, calls, metadata = self.run_image_script(action, "valid")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(metadata, {"containerimage.digest": DIGEST})
                self.assertNotIn("buildx build", calls)
                self.assertNotIn("imagetools create", calls)

    def test_prepare_builds_once_then_publishes_verified_digest(self):
        result, calls, metadata = self.run_image_script("prepare", "missing")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(calls.count("buildx build"), 1)
        self.assertEqual(calls.count("imagetools create"), 1)
        self.assertIn(":ci-spoa-prepare-0.2.0-alpha.2-123", calls)
        self.assertIn("--build-arg SPOA_HUB_VERSION=1.2.3@" + HUB_DIGEST, calls)
        self.assertEqual(metadata, {"containerimage.digest": DIGEST})

    def test_verify_never_builds_missing_release(self):
        result, calls, metadata = self.run_image_script("verify", "missing")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("prepare-spoa-release CI job", result.stderr)
        self.assertNotIn("buildx build", calls)
        self.assertIsNone(metadata)

    def test_unverifiable_release_is_never_overwritten(self):
        for mode in ("auth-error", "command-not-found", "wrong-inputs", "wrong-arm-inputs", "missing-platform", "changed-plugin"):
            with self.subTest(mode=mode):
                result, calls, metadata = self.run_image_script("prepare", mode)
                self.assertNotEqual(result.returncode, 0)
                self.assertNotIn("buildx build", calls)
                self.assertNotIn("imagetools create", calls)
                self.assertIsNone(metadata)

    def test_preparation_rejects_another_branch(self):
        result, calls, _ = self.run_image_script("prepare", "missing", branch="feature/not-a-release")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, "")


if __name__ == "__main__":
    unittest.main()
