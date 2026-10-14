import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
from urllib.parse import parse_qs, urlsplit


SCRIPT = Path(__file__).parents[1] / "record-benchmark-config-identities.py"
SPEC = importlib.util.spec_from_file_location("record_config_identities", SCRIPT)
RECORDER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RECORDER)


def metadata(name="initial", revision="10"):
    return {"apiVersion": "meta.k8s.io/v1", "kind": "PartialObjectMetadata",
            "metadata": {"namespace": "haptic", "name": name, "uid": name + "-uid",
                         "resourceVersion": revision, "annotations": {"private": "never-record"}}}


def initial_list():
    return {"apiVersion": "meta.k8s.io/v1", "kind": "PartialObjectMetadataList",
            "metadata": {"resourceVersion": "10"}, "items": [metadata()]}


def response(document):
    return io.BytesIO(json.dumps(document).encode())


class ConfigIdentityTests(unittest.TestCase):
    def test_watch_retains_deleted_owners_and_resumes_from_bookmark(self):
        output = io.StringIO()
        events = [
            {"type": "ADDED", "object": metadata("later", "11")},
            {"type": "MODIFIED", "object": metadata("later", "12")},
            {"type": "DELETED", "object": metadata("initial", "13")},
            {"type": "BOOKMARK", "object": {"metadata": {"resourceVersion": "14"}}},
        ]
        fetch = mock.Mock(side_effect=[response(initial_list()),
                          io.BytesIO("".join(json.dumps(event) + "\n" for event in events).encode()),
                          RuntimeError("end fixture")])
        ready = mock.Mock()
        with self.assertRaisesRegex(RuntimeError, "end fixture"):
            RECORDER.observe("http://127.0.0.1:1234", output, fetch, ready)
        ready.assert_called_once_with()
        records = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual([item["metadata"]["name"] for item in records],
                         ["initial", "later", "later", "initial"])
        for item in records:
            self.assertEqual(item["apiVersion"], "haproxy-haptic.org/v1alpha1")
            self.assertEqual(item["kind"], "HAProxyCfg")
            self.assertEqual(set(item["metadata"]), {"namespace", "name", "uid"})
        self.assertNotIn("never-record", output.getvalue())
        requests = [call.args[0] for call in fetch.call_args_list]
        self.assertIn("as=PartialObjectMetadataList;", requests[0].get_header("Accept"))
        for request, revision in zip(requests[1:], ["10", "14"]):
            self.assertIn("as=PartialObjectMetadata;", request.get_header("Accept"))
            query = parse_qs(urlsplit(request.full_url).query)
            self.assertEqual(query["resourceVersion"], [revision])
            self.assertEqual(query["watch"], ["true"])

    def test_invalid_list_never_becomes_ready(self):
        for document in ({"kind": "PartialObjectMetadataList"},
                         {"apiVersion": "meta.k8s.io/v1", "kind": "List"}):
            with self.subTest(document=document):
                ready = mock.Mock()
                with self.assertRaisesRegex(ValueError, "metadata-only"):
                    RECORDER.observe("http://127.0.0.1:1234", io.StringIO(),
                                     mock.Mock(return_value=response(document)), ready)
                ready.assert_not_called()

    def test_watch_errors_do_not_silently_relist_and_lose_history(self):
        fetch = mock.Mock(side_effect=[response(initial_list()), response(
            {"type": "ERROR", "object": {"code": 410, "reason": "Expired"}})])
        with self.assertRaisesRegex(ValueError, "invalid event"):
            RECORDER.observe("http://127.0.0.1:1234", io.StringIO(), fetch)
        self.assertEqual(fetch.call_count, 2)

    def test_unverified_or_incomplete_objects_are_not_recorded(self):
        objects = [metadata(), metadata(), metadata()]
        objects[0]["kind"] = "HAProxyCfg"
        objects[1]["metadata"]["uid"] = ""
        del objects[2]["metadata"]["namespace"]
        for item in objects:
            with self.subTest(item=item):
                output = io.StringIO()
                with self.assertRaises((ValueError, KeyError)):
                    RECORDER.record_identity(item, output)
                self.assertEqual(output.getvalue(), "")

    def test_recorder_failure_stops_its_proxy(self):
        process = mock.Mock()
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.object(RECORDER.sys, "argv", [str(SCRIPT), directory]), \
                mock.patch.object(RECORDER.signal, "signal"), \
                mock.patch.object(RECORDER.subprocess, "Popen", return_value=process), \
                mock.patch.object(RECORDER, "proxy_address", return_value="http://127.0.0.1:1234"), \
                mock.patch.object(RECORDER, "observe", side_effect=RuntimeError("watch failed")):
            with self.assertRaisesRegex(RuntimeError, "watch failed"):
                RECORDER.main()
            self.assertEqual((Path(directory) / "runtime-config-history.jsonl").stat().st_mode & 0o777, 0o600)
        process.terminate.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=10)


if __name__ == "__main__":
    unittest.main()
