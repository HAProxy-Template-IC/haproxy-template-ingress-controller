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
from decimal import Decimal
from pathlib import Path


SCRIPT = Path(__file__).parents[1] / "analyze-gateway-api-resources.py"
SPEC = importlib.util.spec_from_file_location("analyze_gateway_api_resources", SCRIPT)
ANALYZER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ANALYZER)

CONTROLLER = ("haptic", "haptic-controller-0", "controller")
LOADBALANCER = ("haptic", "haptic-loadbalancer-0", "haproxy")
CONTROLLER_POD = ("haptic", "haptic-controller-0")
LOADBALANCER_POD = ("haptic", "haptic-loadbalancer-0")
TIMESTAMPS = [100, 105, 110]
SOURCE_TIMESTAMPS = [99.5, 104.5, 109.5]
WINDOW_START = Decimal("99")
WINDOW_END = Decimal("110")
VALUE_METRICS = (
    "cpu",
    "working_set",
    "rss",
    "pod_cgroup_cpu",
    "pod_cgroup_working_set",
)
SOURCE_METRICS = tuple(f"{metric}_source" for metric in VALUE_METRICS)
CLI_INPUTS = (
    ("cpu", "--cpu", "cpu.json"),
    ("working_set", "--working-set", "working-set.json"),
    ("rss", "--rss", "rss.json"),
    ("pod_cgroup_cpu", "--pod-cgroup-cpu", "pod-cgroup-cpu.json"),
    (
        "pod_cgroup_working_set",
        "--pod-cgroup-working-set",
        "pod-cgroup-working-set.json",
    ),
    (
        "cpu_source",
        "--cpu-source-timestamps",
        "cpu-source-timestamps.json",
    ),
    (
        "working_set_source",
        "--working-set-source-timestamps",
        "working-set-source-timestamps.json",
    ),
    (
        "rss_source",
        "--rss-source-timestamps",
        "rss-source-timestamps.json",
    ),
    (
        "pod_cgroup_cpu_source",
        "--pod-cgroup-cpu-source-timestamps",
        "pod-cgroup-cpu-source-timestamps.json",
    ),
    (
        "pod_cgroup_working_set_source",
        "--pod-cgroup-working-set-source-timestamps",
        "pod-cgroup-working-set-source-timestamps.json",
    ),
    ("before", "--identities-before", "identities-before.json"),
    ("after", "--identities-after", "identities-after.json"),
)


def identity_document():
    return [
        {
            "namespace": "haptic",
            "name": "haptic-loadbalancer-0",
            "uid": "loadbalancer-uid",
            "component": "loadbalancer",
            "containers": [
                {
                    "name": "haproxy",
                    "image": "haproxy:3.4",
                    "imageID": "sha256:haproxy",
                    "containerID": "containerd://haproxy",
                    "restartCount": 0,
                    "ready": True,
                }
            ],
        },
        {
            "namespace": "haptic",
            "name": "haptic-controller-0",
            "uid": "controller-uid",
            "component": "controller",
            "containers": [
                {
                    "name": "controller",
                    "image": "haptic:test",
                    "imageID": "sha256:controller",
                    "containerID": "containerd://controller",
                    "restartCount": 0,
                    "ready": True,
                }
            ],
        },
    ]


def metric_document(metric_name, values_by_identity, timestamps=None, pod_cgroup=False):
    timestamps = timestamps or TIMESTAMPS
    result = []
    for identity in reversed(sorted(values_by_identity)):
        namespace, pod = identity[:2]
        labels = {
            "__name__": metric_name,
            "namespace": namespace,
            "pod": pod,
            "instance": "kind-control-plane",
        }
        if not pod_cgroup:
            labels["container"] = identity[2]
        result.append(
            {
                "metric": labels,
                "values": [
                    [timestamp, str(value)]
                    for timestamp, value in zip(timestamps, values_by_identity[identity])
                ],
            }
        )
    return {"status": "success", "data": {"resultType": "matrix", "result": result}}


def source_timestamp_document(value_document, source_timestamps=None):
    document = copy.deepcopy(value_document)
    source_timestamps = source_timestamps or SOURCE_TIMESTAMPS
    for series in document["data"]["result"]:
        del series["metric"]["__name__"]
        for sample, source_timestamp in zip(series["values"], source_timestamps):
            sample[1] = str(source_timestamp)
    return document


def valid_documents():
    documents = {
        "cpu": metric_document(
            "container_cpu_usage_seconds_total",
            {
                CONTROLLER: [10, 11, 13],
                LOADBALANCER: [20, 20.5, 21.5],
            },
        ),
        "working_set": metric_document(
            "container_memory_working_set_bytes",
            {
                CONTROLLER: [100, 200, 300],
                LOADBALANCER: [400, 500, 600],
            },
        ),
        "rss": metric_document(
            "container_memory_rss",
            {
                CONTROLLER: [80, 160, 240],
                LOADBALANCER: [300, 350, 400],
            },
        ),
        "pod_cgroup_cpu": metric_document(
            "container_cpu_usage_seconds_total",
            {
                CONTROLLER_POD: [100, 102, 106],
                LOADBALANCER_POD: [200, 201, 204],
            },
            pod_cgroup=True,
        ),
        "pod_cgroup_working_set": metric_document(
            "container_memory_working_set_bytes",
            {
                CONTROLLER_POD: [1000, 1200, 1400],
                LOADBALANCER_POD: [3000, 3200, 3400],
            },
            pod_cgroup=True,
        ),
        "before": identity_document(),
        "after": identity_document(),
    }
    for metric in VALUE_METRICS:
        documents[f"{metric}_source"] = source_timestamp_document(documents[metric])
    return documents


def analyze(documents, window_start=WINDOW_START, window_end=WINDOW_END):
    return ANALYZER.analyze_resources(
        documents["cpu"],
        documents["working_set"],
        documents["rss"],
        documents["pod_cgroup_cpu"],
        documents["pod_cgroup_working_set"],
        documents["cpu_source"],
        documents["working_set_source"],
        documents["rss_source"],
        documents["pod_cgroup_cpu_source"],
        documents["pod_cgroup_working_set_source"],
        documents["before"],
        documents["after"],
        window_start,
        window_end,
    )


def write_cli_inputs(directory, documents):
    directory.mkdir(parents=True, exist_ok=True)
    paths = {}
    for name, _, filename in CLI_INPUTS:
        paths[name] = directory / filename
        paths[name].write_text(json.dumps(documents[name]), encoding="utf-8")
    return paths


def cli_command(paths, output, window_start=WINDOW_START, window_end=WINDOW_END):
    command = [sys.executable, str(SCRIPT)]
    for name, flag, _ in CLI_INPUTS:
        command.extend((flag, str(paths[name])))
    command.extend(
        (
            "--window-start",
            str(window_start),
            "--window-end",
            str(window_end),
            "--output",
            str(output),
        )
    )
    return command


class AnalyzeGatewayAPIResourcesTests(unittest.TestCase):
    def test_p95_uses_nearest_rank(self):
        values = [Decimal(value) for value in range(1, 21)]

        self.assertEqual(ANALYZER.value_statistics(values)["p95"], 19)

    def test_cli_preserves_exact_decimal_window_boundaries(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            documents = valid_documents()
            for metric in SOURCE_METRICS:
                for series in documents[metric]["data"]["result"]:
                    series["values"][0][1] = "99.0000000000000000002"
            paths = write_cli_inputs(temporary, documents)
            output = temporary / "resources.json"
            command = cli_command(
                paths,
                output,
                window_start=Decimal("99.0000000000000000001"),
            )

            completed = subprocess.run(command, capture_output=True, text=True, check=False)
            result = json.loads(output.read_bytes())

            self.assertEqual(completed.returncode, 0)
            self.assertEqual(result["window"]["sample_count"], 3)
            self.assertEqual(result["window"]["start_unix_seconds"], 100)

    def test_time_aligned_container_component_and_fleet_statistics(self):
        result = analyze(valid_documents())

        self.assertTrue(result["pass"])
        self.assertEqual(
            result["window"],
            {
                "requested_start_unix_seconds": 99,
                "requested_end_unix_seconds": 110,
                "source_sample_boundary": "(start,end]",
                "evaluation_step_seconds": 5,
                "max_source_age_seconds": 20,
                "start_unix_seconds": 100,
                "end_unix_seconds": 110,
                "window_seconds": 10,
                "sample_count": 3,
                "interval_count": 2,
            },
        )
        self.assertEqual(result["identity"]["pod_count"], 2)
        self.assertEqual(result["identity"]["container_count"], 2)
        self.assertEqual(
            [
                (item["pod"], item["container"])
                for item in result["haptic_container_diagnostics"]["containers"]
            ],
            [
                ("haptic-controller-0", "controller"),
                ("haptic-loadbalancer-0", "haproxy"),
            ],
        )

        diagnostics = result["haptic_container_diagnostics"]
        controller = diagnostics["containers"][0]
        self.assertEqual(
            controller["cpu_cores"],
            {
                "mean": 0.3,
                "p95": 0.4,
                "max": 0.4,
                "last": 0.4,
                "counter_delta_seconds": 3,
                "window_seconds": 10,
                "normalized_cores": 0.3,
            },
        )
        self.assertEqual(
            controller["working_set_bytes"],
            {"mean": 200, "p95": 300, "max": 300, "last": 300},
        )
        self.assertEqual(
            diagnostics["components"]["controller"]["cpu_cores"], controller["cpu_cores"]
        )

        fleet = diagnostics["fleet"]
        self.assertEqual(fleet["container_count"], 2)
        self.assertEqual(
            fleet["cpu_cores"],
            {
                "mean": 0.45,
                "p95": 0.6,
                "max": 0.6,
                "last": 0.6,
                "counter_delta_seconds": 4.5,
                "window_seconds": 10,
                "normalized_cores": 0.45,
            },
        )
        self.assertEqual(
            fleet["working_set_bytes"],
            {"mean": 700, "p95": 900, "max": 900, "last": 900},
        )
        self.assertEqual(
            fleet["rss_bytes"],
            {"mean": 510, "p95": 640, "max": 640, "last": 640},
        )

        upstream = result["upstream_compatible_pod_cgroups"]
        self.assertEqual(
            [(item["pod"], item["component"]) for item in upstream["pods"]],
            [
                ("haptic-controller-0", "controller"),
                ("haptic-loadbalancer-0", "loadbalancer"),
            ],
        )
        self.assertEqual(
            upstream["components"]["controller"]["cpu_cores"],
            {
                "mean": 0.6,
                "p95": 0.8,
                "max": 0.8,
                "last": 0.8,
                "counter_delta_seconds": 6,
                "window_seconds": 10,
                "normalized_cores": 0.6,
            },
        )
        self.assertEqual(
            upstream["fleet"]["cpu_cores"],
            {
                "mean": 1,
                "p95": 1.4,
                "max": 1.4,
                "last": 1.4,
                "counter_delta_seconds": 10,
                "window_seconds": 10,
                "normalized_cores": 1,
            },
        )
        self.assertEqual(
            upstream["fleet"]["working_set_bytes"],
            {"mean": 4400, "p95": 4800, "max": 4800, "last": 4800},
        )

    def test_rejects_missing_duplicate_and_unexpected_series(self):
        cases = []

        missing = valid_documents()
        missing["rss"]["data"]["result"].pop()
        cases.append(("missing", missing, "metric_identity_mismatch"))

        duplicate = valid_documents()
        duplicate["cpu"]["data"]["result"].append(
            copy.deepcopy(duplicate["cpu"]["data"]["result"][0])
        )
        cases.append(("duplicate", duplicate, "duplicate_series"))

        unexpected = valid_documents()
        extra = copy.deepcopy(unexpected["working_set"]["data"]["result"][0])
        extra["metric"]["pod"] = "unexpected-pod"
        unexpected["working_set"]["data"]["result"].append(extra)
        cases.append(("unexpected", unexpected, "metric_identity_mismatch"))

        missing_pod_cgroup = valid_documents()
        missing_pod_cgroup["pod_cgroup_working_set"]["data"]["result"].pop()
        cases.append(
            ("missing pod cgroup", missing_pod_cgroup, "metric_identity_mismatch")
        )

        duplicate_pod_cgroup = valid_documents()
        duplicate_pod_cgroup["pod_cgroup_cpu"]["data"]["result"].append(
            copy.deepcopy(duplicate_pod_cgroup["pod_cgroup_cpu"]["data"]["result"][0])
        )
        cases.append(("duplicate pod cgroup", duplicate_pod_cgroup, "duplicate_series"))

        missing_source = valid_documents()
        missing_source["rss_source"]["data"]["result"].pop()
        cases.append(("missing source", missing_source, "metric_identity_mismatch"))

        duplicate_source = valid_documents()
        duplicate_source["cpu_source"]["data"]["result"].append(
            copy.deepcopy(duplicate_source["cpu_source"]["data"]["result"][0])
        )
        cases.append(("duplicate source", duplicate_source, "duplicate_series"))

        for name, documents, expected_code in cases:
            with self.subTest(name=name):
                with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
                    analyze(documents)
                self.assertEqual(raised.exception.code, expected_code)

    def test_rejects_identity_change_and_restart(self):
        changed = valid_documents()
        changed["after"][0]["containers"][0]["containerID"] = "containerd://replacement"
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(changed)
        self.assertEqual(raised.exception.code, "identity_changed")

        restarted = valid_documents()
        restarted["after"][0]["containers"][0]["restartCount"] = 1
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(restarted)
        self.assertEqual(raised.exception.code, "container_restarted")

    def test_rejects_incomplete_timestamp_grid_and_cpu_reset(self):
        incomplete = valid_documents()
        incomplete["pod_cgroup_working_set"]["data"]["result"][0]["values"].pop(1)
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(incomplete)
        self.assertEqual(raised.exception.code, "source_evaluation_grid_mismatch")

        reset = valid_documents()
        reset["cpu"]["data"]["result"][0]["values"][2][1] = "1"
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(reset)
        self.assertEqual(raised.exception.code, "cpu_counter_reset")

        pod_reset = valid_documents()
        pod_reset["pod_cgroup_cpu"]["data"]["result"][0]["values"][2][1] = "1"
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(pod_reset)
        self.assertEqual(raised.exception.code, "cpu_counter_reset")

        nonuniform = valid_documents()
        for metric in (*VALUE_METRICS, *SOURCE_METRICS):
            for series in nonuniform[metric]["data"]["result"]:
                for sample, timestamp in zip(series["values"], (100, 104, 110)):
                    sample[0] = timestamp
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(nonuniform)
        self.assertEqual(raised.exception.code, "sample_step")

    def test_discards_leading_evaluations_until_every_source_is_after_start(self):
        leading = valid_documents()
        leading["rss_source"]["data"]["result"][0]["values"][0][1] = "99"

        result = analyze(leading)

        self.assertTrue(result["pass"])
        self.assertEqual(result["window"]["start_unix_seconds"], 105)
        self.assertEqual(result["window"]["sample_count"], 2)

    def test_discards_prometheus_evaluation_padding_before_window_start(self):
        result = analyze(valid_documents(), window_start=Decimal("101"))

        self.assertTrue(result["pass"])
        self.assertEqual(result["window"]["start_unix_seconds"], 105)
        self.assertEqual(result["window"]["sample_count"], 2)

    def test_rejects_evaluations_outside_window(self):
        after_window = valid_documents()
        after_window["rss"]["data"]["result"][0]["values"].append([111, "1"])
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(after_window)
        self.assertEqual(raised.exception.code, "sample_outside_window")

    def test_rejects_invalid_source_timestamps(self):
        cases = []

        after_window = valid_documents()
        after_window["rss_source"]["data"]["result"][0]["values"][2][1] = "111"
        cases.append(
            (
                "after window",
                after_window,
                WINDOW_START,
                "source_timestamp_outside_window",
            )
        )

        future = valid_documents()
        future["working_set_source"]["data"]["result"][0]["values"][1][1] = "106"
        cases.append(("future", future, WINDOW_START, "source_timestamp_future"))

        regression = valid_documents()
        regression["cpu_source"]["data"]["result"][0]["values"][2][1] = "103"
        cases.append(("regression", regression, WINDOW_START, "source_timestamp_regression"))

        stale = valid_documents()
        stale_values = stale["rss_source"]["data"]["result"][0]["values"]
        stale_values[0][1], stale_values[1][1], stale_values[2][1] = "79", "84", "89"
        cases.append(("stale", stale, Decimal("80"), "source_timestamp_stale"))

        for name, documents, window_start, expected_code in cases:
            with self.subTest(name=name):
                with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
                    analyze(documents, window_start=window_start)
                self.assertEqual(raised.exception.code, expected_code)

    def test_rejects_source_label_and_evaluation_grid_mismatches(self):
        wrong_labels = valid_documents()
        wrong_labels["cpu_source"]["data"]["result"][0]["metric"]["job"] = "other"
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(wrong_labels)
        self.assertEqual(raised.exception.code, "source_labels_mismatch")

        wrong_grid = valid_documents()
        wrong_grid["rss_source"]["data"]["result"][0]["values"][1][0] = 104
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(wrong_grid)
        self.assertEqual(raised.exception.code, "source_evaluation_grid_mismatch")

        incomplete_common_grid = valid_documents()
        incomplete_common_grid["rss"]["data"]["result"][0]["values"].pop(1)
        incomplete_common_grid["rss_source"]["data"]["result"][0]["values"].pop(1)
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(incomplete_common_grid)
        self.assertEqual(raised.exception.code, "sample_timestamps_mismatch")

        named_source = valid_documents()
        named_source["pod_cgroup_cpu_source"]["data"]["result"][0]["metric"][
            "__name__"
        ] = "container_cpu_usage_seconds_total"
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(named_source)
        self.assertEqual(raised.exception.code, "metric_name")

    def test_requires_two_distinct_fresh_source_timestamps_per_series(self):
        repeated_source = valid_documents()
        values = repeated_source["cpu_source"]["data"]["result"][0]["values"]
        for sample in values:
            sample[1] = "100"

        with self.assertRaises(ANALYZER.InsufficientSamples) as raised:
            analyze(repeated_source)

        source_counts = raised.exception.source_counts
        repeated_identity = repeated_source["cpu_source"]["data"]["result"][0]["metric"]
        count_key = (
            "container_cpu:"
            f"{repeated_identity['namespace']}/{repeated_identity['pod']}/"
            f"{repeated_identity['container']}"
        )
        self.assertEqual(source_counts[count_key], 1)

    def test_rejects_prometheus_annotations_and_unordered_samples(self):
        warned = valid_documents()
        warned["cpu"]["warnings"] = ["partial response"]
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(warned)
        self.assertEqual(raised.exception.code, "prometheus_annotations")

        source_info = valid_documents()
        source_info["rss_source"]["infos"] = ["partial response"]
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(source_info)
        self.assertEqual(raised.exception.code, "prometheus_annotations")

        unordered = valid_documents()
        values = unordered["working_set"]["data"]["result"][0]["values"]
        values[0], values[1] = values[1], values[0]
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(unordered)
        self.assertEqual(raised.exception.code, "sample_timestamp_order")

    def test_rejects_conflated_pod_cgroup_and_container_series(self):
        conflated = valid_documents()
        conflated["pod_cgroup_cpu"]["data"]["result"][0]["metric"]["container"] = "haproxy"

        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(conflated)

        self.assertEqual(raised.exception.code, "metric_identity_scope")

        derived_cpu = valid_documents()
        del derived_cpu["pod_cgroup_cpu"]["data"]["result"][0]["metric"]["__name__"]
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(derived_cpu)
        self.assertEqual(raised.exception.code, "metric_name")

    def test_accepts_total_cpu_series_and_rejects_per_cpu_series(self):
        total = valid_documents()
        for metric in ("cpu", "cpu_source", "pod_cgroup_cpu", "pod_cgroup_cpu_source"):
            for series in total[metric]["data"]["result"]:
                series["metric"]["cpu"] = "total"
        self.assertTrue(analyze(total)["pass"])

        per_cpu = valid_documents()
        per_cpu["cpu"]["data"]["result"][0]["metric"]["cpu"] = "cpu00"
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(per_cpu)
        self.assertEqual(raised.exception.code, "metric_cpu_scope")

        per_cpu_source = valid_documents()
        per_cpu_source["pod_cgroup_cpu_source"]["data"]["result"][0]["metric"][
            "cpu"
        ] = "cpu01"
        with self.assertRaises(ANALYZER.AnalysisFailure) as raised:
            analyze(per_cpu_source)
        self.assertEqual(raised.exception.code, "metric_cpu_scope")

    def test_cli_is_deterministic_and_writes_failure_result(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            documents = valid_documents()
            paths = write_cli_inputs(temporary, documents)
            output = temporary / "resources.json"
            command = cli_command(paths, output)

            first = subprocess.run(command, capture_output=True, text=True, check=False)
            first_bytes = output.read_bytes()
            second = subprocess.run(command, capture_output=True, text=True, check=False)

            self.assertEqual(first.returncode, 0)
            self.assertEqual(second.returncode, 0)
            self.assertEqual(output.read_bytes(), first_bytes)
            self.assertTrue(json.loads(first_bytes)["pass"])

            documents["rss"]["data"]["result"].pop()
            paths["rss"].write_text(json.dumps(documents["rss"]), encoding="utf-8")
            failed = subprocess.run(command, capture_output=True, text=True, check=False)

            self.assertEqual(failed.returncode, 1)
            failure_result = json.loads(output.read_bytes())
            self.assertFalse(failure_result["pass"])
            self.assertEqual(failure_result["failures"][0]["code"], "metric_identity_mismatch")

    def test_cli_marks_only_insufficient_fresh_samples_non_gating(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            documents = valid_documents()
            for metric in (*VALUE_METRICS, *SOURCE_METRICS):
                for series in documents[metric]["data"]["result"]:
                    series["values"] = series["values"][:1]
            paths = write_cli_inputs(temporary / "prometheus-range", documents)
            output = temporary / "resources.json"
            command = cli_command(paths, output)

            strict = subprocess.run(command, capture_output=True, text=True, check=False)
            strict_result = json.loads(output.read_bytes())
            allowed = subprocess.run(
                [*command, "--allow-insufficient-samples"],
                capture_output=True,
                text=True,
                check=False,
            )
            allowed_result = json.loads(output.read_bytes())

            self.assertEqual(strict.returncode, 1)
            self.assertFalse(strict_result["pass"])
            self.assertEqual(strict_result["failures"][0]["code"], "sample_window")
            self.assertEqual(allowed.returncode, 0)
            self.assertIsNone(allowed_result["pass"])
            self.assertFalse(allowed_result["gating"])
            self.assertEqual(allowed_result["analysis_status"], "not_gated")
            self.assertEqual(allowed_result["window"]["sample_count"], 1)
            self.assertEqual(len(allowed_result["source_sample_counts"]), 10)
            self.assertEqual(set(allowed_result["source_sample_counts"].values()), {1})
            self.assertEqual(
                allowed_result["raw_artifacts"]["haptic_container_cpu"],
                "prometheus-range/cpu.json",
            )
            self.assertEqual(
                allowed_result["raw_artifacts"][
                    "haptic_container_cpu_source_timestamps"
                ],
                "prometheus-range/cpu-source-timestamps.json",
            )

            documents["cpu_source"]["data"]["result"][0]["metric"]["job"] = "other"
            paths["cpu_source"].write_text(
                json.dumps(documents["cpu_source"]), encoding="utf-8"
            )
            malformed = subprocess.run(
                [*command, "--allow-insufficient-samples"],
                capture_output=True,
                text=True,
                check=False,
            )
            malformed_result = json.loads(output.read_bytes())

            self.assertEqual(malformed.returncode, 1)
            self.assertFalse(malformed_result["pass"])
            self.assertEqual(
                malformed_result["failures"][0]["code"], "source_labels_mismatch"
            )


if __name__ == "__main__":
    unittest.main()
