import importlib.util
from pathlib import Path
import unittest
from unittest import mock


SPEC = importlib.util.spec_from_file_location("capture_startup", Path(__file__).parents[1] / "scripts/capture-startup.py")
CAPTURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CAPTURE)

POD = {"metadata": {"name": "a", "uid": "1234", "labels": {"app.kubernetes.io/component": "controller"}},
       "spec": {"nodeName": "kind-worker"},
       "status": {"containerStatuses": [{"name": "controller", "ready": True, "restartCount": 0}]}}


def record(stream, tag, content):
    return f"2026-10-16T18:30:00.000000000Z {stream} {tag} {content}\n"


class LogFetchTests(unittest.TestCase):
    def test_reads_the_initial_container_log_on_the_pod_node(self):
        with mock.patch.object(CAPTURE.subprocess, "check_output", return_value="") as fetch:
            CAPTURE.fetch_log(POD)
        self.assertEqual(fetch.call_args.args[0],
                         ["docker", "exec", "kind-worker", "cat", "/var/log/pods/haptic_a_1234/controller/0.log"])

    def test_cri_records_become_prefixed_kubectl_lines(self):
        cri = (record("stdout", "F", 'level=INFO msg="HAProxy Template Ingress Controller starting"')
               + record("stdout", "P", "long ") + record("stdout", "F", "line")
               + record("stderr", "F", "")
               + record("stdout", "F", 'level=DEBUG msg="Test run completed" component=test-runner '
                                       'total=2 passed=2 failed=0 skipped=0 duration=1s')
               + record("stdout", "P", "unfinished") + "2026-10-16T18:30:00Z stdout F half")
        log = CAPTURE.kubectl_prefixed("a", cri)
        self.assertEqual(log.splitlines()[1:3], ["[pod/a/controller] long line", "[pod/a/controller] "])
        self.assertNotIn("unfinished", log)
        self.assertNotIn("half", log)
        other = {**POD, "metadata": {**POD["metadata"], "name": "b"}}
        verdict = CAPTURE.VERIFY.verify(log + CAPTURE.kubectl_prefixed("b", cri), {"items": [POD, other]})
        self.assertEqual(sorted(verdict), ["a", "b"])

    def test_malformed_record_fails(self):
        with self.assertRaises(ValueError):
            CAPTURE.kubectl_prefixed("a", "not a cri record\n")


if __name__ == "__main__":
    unittest.main()
