import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


SPEC = importlib.util.spec_from_file_location(
    "profile_scale", Path(__file__).parents[1] / "scripts/profile-scale.py")
PROFILE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROFILE)


class ProfileScaleTests(unittest.TestCase):
    def test_slow_node_and_api_calls_never_stop_the_profiler(self):
        timeout = subprocess.TimeoutExpired(["crictl"], 30)
        with tempfile.TemporaryDirectory() as directory, patch.object(PROFILE.subprocess, "run", side_effect=timeout):
            path = Path(directory) / "containers-start.json"
            PROFILE.container_stats(path)
            self.assertIn("timed out", path.with_suffix(".error.txt").read_text())
            self.assertEqual(PROFILE.controller_names(), [])
        with patch.object(PROFILE.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "kubectl")):
            self.assertEqual(PROFILE.controller_names(), [])
        with patch.object(PROFILE.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, b"not json", b"")):
            self.assertEqual(PROFILE.controller_names(), [])


if __name__ == "__main__":
    unittest.main()
