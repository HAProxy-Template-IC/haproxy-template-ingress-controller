import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).parents[2]
REGISTRY_FAILURE = (
    "ERROR: failed to build: failed to solve: unexpected status from HEAD request to "
    "https://registry-1.docker.io/v2/library/golang/manifests/1.27-bookworm: 502 Bad Gateway"
)


class RegistryBuildRetryTests(unittest.TestCase):
    def run_build(self, failures, message):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            command = root / "build.py"
            command.write_text("""import pathlib, sys
state = pathlib.Path(sys.argv[1])
attempt = int(state.read_text()) + 1 if state.exists() else 1
state.write_text(str(attempt))
if attempt <= int(sys.argv[2]):
    print(sys.argv[3], file=sys.stderr)
    sys.exit(7)
print('image built')
""")
            sleeper = root / "sleep"
            sleeper.write_text('#!/bin/sh\nprintf "%s\\n" "$1" >> "$RETRY_TEST_DELAYS"\n')
            sleeper.chmod(0o755)
            result = subprocess.run(
                ["bash", str(ROOT / "scripts/retry-registry-build.sh"), sys.executable,
                 str(command), str(root / "attempts"), str(failures), message],
                env={**os.environ, "PATH": str(root) + os.pathsep + os.environ["PATH"],
                     "RETRY_TEST_DELAYS": str(root / "delays")}, text=True, capture_output=True, check=False)
            delays = (root / "delays").read_text().splitlines() if (root / "delays").exists() else []
            return result, int((root / "attempts").read_text()), delays

    def test_retries_registry_gateway_error_then_returns_success(self):
        result, attempts, delays = self.run_build(2, REGISTRY_FAILURE)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(attempts, 3)
        self.assertEqual(delays, ["5", "10"])
        self.assertIn("image built", result.stdout)

    def test_exhaustion_preserves_the_build_exit_code(self):
        result, attempts, delays = self.run_build(4, REGISTRY_FAILURE)
        self.assertEqual(result.returncode, 7)
        self.assertEqual(attempts, 3)
        self.assertEqual(delays, ["5", "10"])

    def test_success_does_not_retry(self):
        result, attempts, delays = self.run_build(0, "")
        self.assertEqual((result.returncode, attempts, delays), (0, 1, []))

    def test_compile_assertion_authorization_and_unrelated_http_failures_do_not_retry(self):
        for message in ("ERROR: Go compiler: undefined: missing", "--- FAIL: TestExpectedBehavior",
                        REGISTRY_FAILURE.replace("502 Bad Gateway", "401 Unauthorized"),
                        "ERROR: integration request returned 502 Bad Gateway",
                        REGISTRY_FAILURE.replace("/v2/library/golang/", "/application/")):
            with self.subTest(message=message):
                result, attempts, delays = self.run_build(4, message)
                self.assertEqual((result.returncode, attempts, delays), (7, 1, []))

    def test_reads_output_written_after_the_build_exits(self):
        with tempfile.TemporaryDirectory() as temporary:
            delayed = f"(sleep 0.3; echo '{REGISTRY_FAILURE}' >&2) & exit 7"
            sleeper = Path(temporary) / "sleep"
            sleeper.write_text('#!/bin/sh\n[ "$1" != 0.3 ] || exec /bin/sleep "$1"\n')
            sleeper.chmod(0o755)
            result = subprocess.run(["bash", str(ROOT / "scripts/retry-registry-build.sh"), "sh", "-c", delayed],
                                    env={**os.environ, "PATH": temporary + os.pathsep + os.environ["PATH"]},
                                    text=True, capture_output=True, check=False)
        self.assertEqual(result.returncode, 7)
        self.assertEqual(result.stderr.count("retrying build"), 2, result.stderr)


if __name__ == "__main__":
    unittest.main()
