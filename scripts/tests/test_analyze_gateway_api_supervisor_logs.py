# Copyright 2026 Philipp Hossner
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import copy
import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).parents[1] / "analyze-gateway-api-supervisor-logs.py"
SPEC = importlib.util.spec_from_file_location("analyze_gateway_api_supervisor_logs", SCRIPT)
ANALYZER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ANALYZER)
WINDOW = {
    "start": "2026-08-15T10:00:00.000000000Z",
    "end": "2026-08-15T10:02:00.000000000Z",
}
STARTED_AT = "2026-08-15T10:02:04.500000000Z"
CAPTURED_AT = "2026-08-15T10:02:05.000000000Z"
SPOA_WARNING = (
    "WARNING: SPOA hub exited with status 139; SPOA processing is unavailable; "
    "restarting in 1s"
)
SPOA_HEALTH_WARNING = (
    "WARNING: SPOA hub failed 3 consecutive health checks; "
    "SPOA processing is unavailable; restarting child"
)


def source(pod, pod_uid, container_id, child="spoa-hub", log_file=None):
    return {
        "namespace": "haptic",
        "pod": pod,
        "pod_uid": pod_uid,
        "container": child,
        "container_id": container_id,
        "child": child,
        "log_file": log_file or f"{pod}-{child}.log",
        "capture": {
            "rc": 0,
            "started_at": STARTED_AT,
            "captured_at": CAPTURED_AT,
            "since_time": WINDOW["start"],
            "tail": -1,
            "timestamps": True,
            "prefix": True,
        },
    }


def topology_document(sources):
    tasks = [
        {
            "key": f"{item['namespace']}/{item['pod']}/{item['child']}",
            "namespace": item["namespace"],
            "pod": item["pod"],
            "pod_uid": item["pod_uid"],
            "container": item["container"],
            "container_id": item["container_id"],
        }
        for item in sources
    ]
    return {
        "schema_version": 1,
        "evidence_valid": True,
        "topology": {
            "schema_version": 1,
            "supervised_container_names": sorted({item["child"] for item in sources}),
            "tasks": tasks,
        },
    }


def manifest(sources):
    return {
        "schema_version": 1,
        "window": WINDOW,
        "supervised_children": {
            "before": "supervised-children-before.json",
            "after": "supervised-children-after.json",
        },
        "sources": sources,
    }


def write_topologies(directory, before_sources, after_sources=None):
    directory = Path(directory)
    if after_sources is None:
        after_sources = before_sources
    (directory / "supervised-children-before.json").write_text(
        json.dumps(topology_document(before_sources)), encoding="utf-8"
    )
    (directory / "supervised-children-after.json").write_text(
        json.dumps(topology_document(after_sources)), encoding="utf-8"
    )


def log_line(source_item, timestamp, message):
    return (
        f"[pod/{source_item['pod']}/{source_item['container']}] "
        f"{timestamp} {message}\n"
    )


class AnalyzeGatewayAPISupervisorLogsTest(unittest.TestCase):
    def run_cli(self, directory, document):
        directory = Path(directory)
        manifest_path = directory / "manifest.json"
        output_path = directory / "analysis.json"
        manifest_path.write_text(json.dumps(document), encoding="utf-8")
        completed = subprocess.run(
            [
                sys.executable,
                str(SCRIPT),
                "--manifest",
                str(manifest_path),
                "--output",
                str(output_path),
            ],
            check=False,
            capture_output=True,
            text=True,
        )
        return completed, json.loads(output_path.read_text(encoding="utf-8"))

    def analyze(self, directory, sources, before_sources=None, after_sources=None):
        directory = Path(directory)
        authoritative = before_sources if before_sources is not None else sources
        write_topologies(directory, authoritative, after_sources)
        return ANALYZER.analyze_manifest(manifest(sources), directory / "manifest.json")

    def test_fc06718a_spoa_status_139_is_a_valid_product_negative(self):
        sources = [
            source(
                "haptic-haproxy-7f6d54984-xlt5f",
                "229c32ab-2565-4d02-9bb2-4baec6b47ce9",
                "containerd://2316c3da4c0a47cc88e457a980d01733bd90070ee12f51af93c4faaaf7b816c0",
            ),
            source(
                "haptic-haproxy-7f6d54984-4q5pj",
                "90fbd38a-5171-4669-89d3-d60d1b7c3a92",
                "containerd://0deeb1dffd40163b886ce48e7ef4e693e358ef8356e5af5c065354792ab3c347",
            ),
        ]
        timestamps = (
            "2026-08-15T10:01:04.357709000Z",
            "2026-08-15T10:01:04.358885000Z",
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            write_topologies(directory, sources)
            for item, timestamp in zip(sources, timestamps, strict=True):
                (directory / item["log_file"]).write_text(
                    log_line(item, timestamp, SPOA_WARNING), encoding="utf-8"
                )
            completed, result = self.run_cli(directory, manifest(sources))

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertTrue(result["evidence_valid"])
        self.assertFalse(result["pass"])
        self.assertEqual(result["supervised_children"]["task_count"], 2)
        self.assertEqual([event["status"] for event in result["events"]], [139, 139])
        self.assertEqual([event["signal"] for event in result["events"]], [11, 11])
        self.assertTrue(all(event["exit_observed"] for event in result["events"]))
        self.assertTrue(all(event["restart_reason"] == "child-exit" for event in result["events"]))

    def test_empty_completed_logs_for_every_authoritative_task_pass(self):
        sources = [
            source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a"),
            source("haptic-haproxy-a", "pod-uid-a", "containerd://vector-a", "vector"),
            source("haptic-haproxy-b", "pod-uid-b", "containerd://spoa-b"),
        ]
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            for item in sources:
                (directory / item["log_file"]).write_text("", encoding="utf-8")
            result = self.analyze(directory, sources)

        self.assertTrue(result["evidence_valid"])
        self.assertTrue(result["pass"])
        self.assertEqual(result["source_count"], 3)
        self.assertEqual(result["event_count"], 0)

    def test_rejects_manifest_source_omitted_from_authoritative_tasks(self):
        sources = [source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")]
        vector = source(
            "haptic-haproxy-a",
            "pod-uid-a",
            "containerd://vector-a",
            "vector",
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            (directory / sources[0]["log_file"]).write_text("", encoding="utf-8")
            with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "authoritative") as raised:
                self.analyze(directory, sources, before_sources=[*sources, vector])
        self.assertEqual(raised.exception.code, "source_inventory")
        self.assertEqual(raised.exception.details["missing"], ["haptic/haptic-haproxy-a/vector"])

    def test_rejects_manifest_source_absent_from_authoritative_tasks(self):
        spoa = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
        vector = source(
            "haptic-haproxy-a",
            "pod-uid-a",
            "containerd://vector-a",
            "vector",
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            for item in (spoa, vector):
                (directory / item["log_file"]).write_text("", encoding="utf-8")
            with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "authoritative") as raised:
                self.analyze(directory, [spoa, vector], before_sources=[spoa])
        self.assertEqual(raised.exception.code, "source_inventory")
        self.assertEqual(raised.exception.details["unexpected"], ["haptic/haptic-haproxy-a/vector"])

    def test_rejects_source_identity_that_differs_from_authoritative_task(self):
        cases = (
            ("pod UID", "pod_uid", "wrong-pod-uid"),
            ("container ID", "container_id", "containerd://wrong"),
        )
        for name, field, value in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                directory = Path(directory)
                authoritative = source(
                    "haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a"
                )
                observed = copy.deepcopy(authoritative)
                observed[field] = value
                (directory / observed["log_file"]).write_text("", encoding="utf-8")
                with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "differs") as raised:
                    self.analyze(directory, [observed], before_sources=[authoritative])
                self.assertEqual(raised.exception.code, "source_identity_mismatch")

    def test_rejects_before_after_authoritative_topology_change(self):
        before = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
        after = copy.deepcopy(before)
        after["container_id"] = "containerd://spoa-replaced"
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            (directory / before["log_file"]).write_text("", encoding="utf-8")
            write_topologies(directory, [before], [after])
            with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "topology changed") as raised:
                ANALYZER.analyze_manifest(manifest([before]), directory / "manifest.json")
        self.assertEqual(raised.exception.code, "topology_changed")

    def test_requires_complete_successful_capture_metadata(self):
        cases = (
            ("missing metadata", None),
            ("nonzero rc", {"rc": 1}),
        )
        for name, capture in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                directory = Path(directory)
                item = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
                if capture is None:
                    del item["capture"]
                else:
                    item["capture"] = capture
                (directory / item["log_file"]).write_text("", encoding="utf-8")
                write_topologies(directory, [item])
                with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
                    ANALYZER.analyze_manifest(manifest([item]), directory / "manifest.json")
                self.assertEqual(raised.exception.code, "capture_incomplete")

    def test_rejects_incomplete_capture_window_request(self):
        cases = (
            ("early start", "started_at", "2026-08-15T10:01:59.999999999Z"),
            ("wrong since", "since_time", "2026-08-15T10:00:00.000000001Z"),
            ("truncated tail", "tail", 10),
            ("timestamps disabled", "timestamps", False),
            ("prefix disabled", "prefix", False),
            ("completion before start", "captured_at", "2026-08-15T10:02:04.499999999Z"),
        )
        for name, field, value in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                directory = Path(directory)
                item = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
                item["capture"][field] = value
                (directory / item["log_file"]).write_text("", encoding="utf-8")
                write_topologies(directory, [item])
                with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
                    ANALYZER.analyze_manifest(manifest([item]), directory / "manifest.json")
                self.assertEqual(raised.exception.code, "capture_incomplete")

    def test_unknown_authoritative_child_is_invalid_instead_of_omitted(self):
        item = source(
            "haptic-haproxy-a",
            "pod-uid-a",
            "containerd://future-a",
            "future-child",
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            write_topologies(directory, [item])
            with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "unsupported") as raised:
                ANALYZER.analyze_manifest(manifest([item]), directory / "manifest.json")
        self.assertEqual(raised.exception.code, "topology_child")

    def test_post_end_lines_are_recorded_without_changing_the_window_verdict(self):
        item = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
        lines = log_line(
            item,
            "2026-08-15T10:02:00.000000001Z",
            '{"level":"INFO","message":"after cutoff"}',
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            (directory / item["log_file"]).write_text(lines, encoding="utf-8")
            result = self.analyze(directory, [item])

        self.assertTrue(result["pass"])
        self.assertEqual(result["post_end_lines"]["count"], 1)

    def test_health_restart_associates_the_next_exit_without_double_counting(self):
        item = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
        exit_message = SPOA_WARNING.replace("status 139", "status 143")
        lines = (
            log_line(item, "2026-08-15T10:01:59.000000000Z", SPOA_HEALTH_WARNING)
            + log_line(item, "2026-08-15T10:02:04.000000000Z", exit_message)
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            (directory / item["log_file"]).write_text(lines, encoding="utf-8")
            result = self.analyze(directory, [item])

        event = result["events"][0]
        self.assertFalse(result["pass"])
        self.assertEqual(result["event_count"], 1)
        self.assertEqual(event["timestamp"], "2026-08-15T10:01:59.000000000Z")
        self.assertEqual(event["exit_timestamp"], "2026-08-15T10:02:04.000000000Z")
        self.assertEqual(event["status"], 143)
        self.assertEqual(event["signal"], 15)
        self.assertTrue(event["exit_observed"])
        self.assertEqual(event["line"], 1)
        self.assertEqual(event["exit_line"], 2)
        self.assertEqual(event["restart_reason"], "health-check-failure")
        self.assertEqual(event["health_check_failure"]["consecutive_failures"], 3)

    def test_health_warning_without_exit_is_a_valid_product_negative(self):
        item = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
        lines = log_line(item, "2026-08-15T10:01:59.000000000Z", SPOA_HEALTH_WARNING)
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            (directory / item["log_file"]).write_text(lines, encoding="utf-8")
            write_topologies(directory, [item])
            completed, result = self.run_cli(directory, manifest([item]))

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertTrue(result["evidence_valid"])
        self.assertFalse(result["pass"])
        self.assertEqual(result["event_count"], 1)
        event = result["events"][0]
        self.assertFalse(event["exit_observed"])
        self.assertIsNone(event["exit_timestamp"])
        self.assertIsNone(event["status"])
        self.assertIsNone(event["signal"])
        self.assertIsNone(event["restart_backoff_seconds"])

    def test_rejects_second_health_warning_post_end_before_exit(self):
        item = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
        lines = (
            log_line(item, "2026-08-15T10:01:59.000000000Z", SPOA_HEALTH_WARNING)
            + log_line(item, "2026-08-15T10:02:00.500000000Z", SPOA_HEALTH_WARNING)
            + log_line(item, "2026-08-15T10:02:01.000000000Z", SPOA_WARNING)
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            (directory / item["log_file"]).write_text(lines, encoding="utf-8")
            with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "unpaired") as raised:
                self.analyze(directory, [item])
        self.assertEqual(raised.exception.code, "unpaired_health_restart")

    def test_rejects_malformed_or_mismatched_warning(self):
        cases = (
            ("malformed", "spoa-hub", SPOA_WARNING.replace("status 139", "status signal-11"),
             "malformed_supervisor_warning"),
            ("mismatched", "vector", SPOA_WARNING, "warning_source_mismatch"),
        )
        for name, child, message, code in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                directory = Path(directory)
                item = source(
                    "haptic-haproxy-a",
                    "pod-uid-a",
                    f"containerd://{child}-a",
                    child,
                )
                (directory / item["log_file"]).write_text(
                    log_line(item, "2026-08-15T10:01:04Z", message), encoding="utf-8"
                )
                with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
                    self.analyze(directory, [item])
                self.assertEqual(raised.exception.code, code)

    def test_rejects_missing_and_duplicate_log_sources(self):
        item = source("haptic-haproxy-a", "pod-uid-a", "containerd://spoa-a")
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            write_topologies(directory, [item])
            completed, result = self.run_cli(directory, manifest([item]))
            self.assertEqual(completed.returncode, 1)
            self.assertEqual(result["failure"]["code"], "missing_source")

            (directory / item["log_file"]).write_text("", encoding="utf-8")
            with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "duplicate source") as raised:
                ANALYZER.analyze_manifest(
                    manifest([item, copy.deepcopy(item)]), directory / "manifest.json"
                )
        self.assertEqual(raised.exception.code, "duplicate_source")


if __name__ == "__main__":
    unittest.main()
