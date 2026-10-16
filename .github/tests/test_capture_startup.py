import importlib.util
from pathlib import Path
import subprocess
import unittest
from unittest import mock


SPEC = importlib.util.spec_from_file_location("capture_startup", Path(__file__).parents[1] / "scripts/capture-startup.py")
CAPTURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CAPTURE)


class LogFetchTests(unittest.TestCase):
    def test_retries_a_stalled_log_stream(self):
        outcomes = [subprocess.TimeoutExpired("kubectl", 30), subprocess.TimeoutExpired("kubectl", 30), "log"]

        def check_output(*_args, **_kwargs):
            outcome = outcomes.pop(0)
            if isinstance(outcome, Exception):
                raise outcome
            return outcome

        with mock.patch.object(CAPTURE.subprocess, "check_output", check_output):
            self.assertEqual(CAPTURE.fetch_log("pod"), "log")

    def test_fails_after_the_last_attempt(self):
        with mock.patch.object(CAPTURE.subprocess, "check_output", side_effect=subprocess.TimeoutExpired("kubectl", 30)) as fetch:
            with self.assertRaises(subprocess.TimeoutExpired):
                CAPTURE.fetch_log("pod")
        self.assertEqual(fetch.call_count, 3)


if __name__ == "__main__":
    unittest.main()
