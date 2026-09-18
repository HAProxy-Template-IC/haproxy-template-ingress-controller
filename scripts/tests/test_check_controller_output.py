import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "check-controller-output.sh"


class ControllerOutputTests(unittest.TestCase):
    def test_capture_and_rejection(self):
        cases = [
            ("clean", "INFO controller ready\n", True, 0),
            ("mismatch", "ERROR Rendered output rejected: content differs from its plan file\n", True, 1),
            ("empty logs", "", True, 1),
            ("missing controller", "INFO unrelated pod\n", False, 1),
        ]
        for name, log, has_controller, expected in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as temp:
                directory = Path(temp)
                pods = {"items": [{"metadata": {"name": "controller"}}] if has_controller else []}
                for command, output in {
                    "kubectl": json.dumps(pods),
                    "kind": "test-control-plane",
                    "docker": log,
                }.items():
                    executable = directory / command
                    executable.write_text("#!/usr/bin/env python3\nimport sys\nsys.stdout.write(" + repr(output) + ")\n")
                    executable.chmod(0o755)
                artifacts = directory / "artifacts"
                result = subprocess.run(
                    ["bash", str(SCRIPT)], capture_output=True, text=True,
                    env={**os.environ, "PATH": str(directory) + os.pathsep + os.environ["PATH"],
                         "CONTROLLER_OUTPUT_DIR": str(artifacts)},
                    check=False,
                )
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                if has_controller:
                    self.assertEqual((artifacts / "controller.log").read_text(), log)


if __name__ == "__main__":
    unittest.main()
