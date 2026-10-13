import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).parents[2] / ".github/scripts/profile-gateway-benchmark.py"
SPEC = importlib.util.spec_from_file_location("profile_gateway_benchmark", SCRIPT)
PROFILER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROFILER)


class ControllerProfileTests(unittest.TestCase):
    def test_profile_uses_loopback_and_stops_forwarder_on_success_or_error(self):
        for error in (None, RuntimeError("profile failed")):
            with self.subTest(error=error), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                process = mock.Mock()
                process.poll.return_value = None

                def forward(command, stdout, stderr):
                    self.assertEqual(command, ["kubectl", "--kubeconfig", "owned-config", "port-forward",
                                               "--address=127.0.0.1", "-n", "haptic", "pod/controller", ":8080"])
                    stdout.write(b"Forwarding from 127.0.0.1:12345 -> 8080\n")
                    stdout.flush()
                    return process

                with mock.patch.object(PROFILER.subprocess, "Popen", side_effect=forward), \
                        mock.patch.object(PROFILER, "request", side_effect=error) as request:
                    if error:
                        with self.assertRaisesRegex(RuntimeError, "profile failed"):
                            PROFILER.capture_controller(["kubectl", "--kubeconfig", "owned-config"],
                                                        "controller", root)
                    else:
                        PROFILER.capture_controller(["kubectl", "--kubeconfig", "owned-config"],
                                                    "controller", root)
                    expected = ["heap"] if error else ["heap", "goroutine", "profile?seconds=30"]
                    self.assertEqual([call.args[0][-1] for call in request.call_args_list],
                                     ["http://127.0.0.1:12345/debug/pprof/" + name for name in expected])
                    self.assertEqual(request.call_args.args[1],
                                     root / ("controller-heap.prof" if error else "controller-cpu.prof"))
                process.terminate.assert_called_once_with()
                process.wait.assert_called_once_with(timeout=10)

    def test_failed_request_preserves_failure_evidence(self):
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory) / "controller.prof"
            failure = subprocess.CompletedProcess(["curl"], 22, b"", b"HTTP 403")
            with mock.patch.object(PROFILER.subprocess, "run", return_value=failure), \
                    self.assertRaisesRegex(RuntimeError, "exit 22"):
                PROFILER.request(["curl"], destination)
            self.assertEqual(destination.with_suffix(".prof.exit").read_text(), "22\n")
            self.assertEqual(destination.with_suffix(".prof.stderr").read_bytes(), b"HTTP 403")


if __name__ == "__main__":
    unittest.main()
