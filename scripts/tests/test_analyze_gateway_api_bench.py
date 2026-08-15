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

import importlib.util
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).parents[1] / "analyze-gateway-api-bench.py"
SPEC = importlib.util.spec_from_file_location("analyze_gateway_api_bench", SCRIPT)
ANALYZER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ANALYZER)


def success_line(gateway: str, iteration: int, latency: str) -> str:
    return (
        "2026-08-14T10:00:00Z\tinfo\tprobe completed: 200\t"
        f"gateway={gateway} iter={iteration} latency={latency}"
    )


class AnalyzeGatewayAPIBenchTests(unittest.TestCase):
    def test_parses_go_durations_exactly(self):
        cases = {
            "0s": 0,
            "291.58915ms": 291_589_150,
            "1.109182476s": 1_109_182_476,
            "1m2.003004005s": 62_003_004_005,
            "7\N{MICRO SIGN}s": 7_000,
            "8\N{GREEK SMALL LETTER MU}s": 8_000,
        }
        for value, expected in cases.items():
            with self.subTest(value=value):
                self.assertEqual(ANALYZER.parse_go_duration_ns(value), expected)

        for value in ("", "1", "1fortnight", "0.1ns", "-1ms"):
            with self.subTest(value=value):
                with self.assertRaises(ValueError):
                    ANALYZER.parse_go_duration_ns(value)

    def test_summarizes_each_gateway_with_nearest_rank_percentiles(self):
        lines = []
        for gateway in ("bench/first", "bench/second"):
            lines.extend(
                success_line(gateway, iteration, f"{iteration + 1}ms") for iteration in range(10)
            )
        samples, errors, malformed = ANALYZER.parse_probe_log("\n".join(reversed(lines)))
        result = ANALYZER.build_result(
            samples,
            errors,
            malformed,
            20,
            10,
            2,
            {},
            expected_gateway_names=["bench/second", "bench/first"],
        )

        self.assertTrue(result["pass"])
        self.assertEqual(result["scenario"], "probe")
        self.assertEqual(result["observed"]["samples"], 20)
        self.assertEqual(
            [(sample["gateway"], sample["iter"]) for sample in result["samples"]],
            [
                (gateway, iteration)
                for gateway in ("bench/first", "bench/second")
                for iteration in range(10)
            ],
        )
        for summary in result["gateways"].values():
            self.assertEqual(summary["count"], 10)
            self.assertEqual(summary["error_count"], 0)
            self.assertEqual(
                summary["latency_ns"],
                {
                    "max": 10_000_000,
                    "mean": 5_500_000.0,
                    "median": 5_500_000.0,
                    "p90": 9_000_000,
                    "p95": 10_000_000,
                    "p99": 10_000_000,
                },
            )

    def test_duplicate_incomplete_and_error_samples_fail(self):
        text = "\n".join(
            [
                success_line("bench/haptic", 0, "1ms"),
                success_line("bench/haptic", 0, "2ms"),
                "2026-08-14T10:00:00Z\terror\tunexpected status code: 503\t"
                "gateway=bench/haptic iter=1",
            ]
        )
        samples, errors, malformed = ANALYZER.parse_probe_log(text)
        result = ANALYZER.build_result(samples, errors, malformed, 2, 2, 1, {})

        self.assertFalse(result["pass"])
        self.assertEqual(result["observed"]["duplicate_samples"], 1)
        self.assertEqual(result["observed"]["error_count"], 1)
        self.assertEqual(result["errors"][0]["status"], 503)
        self.assertEqual(
            {failure["code"] for failure in result["failures"]},
            {
                "duplicate_samples",
                "gateway_route_ids",
                "gateway_route_count",
                "route_count",
                "route_ids",
                "sample_count",
                "unexpected_statuses",
            },
        )

    def test_rejects_shifted_route_ids_and_wrong_gateway_names(self):
        shifted_lines = [
            success_line("bench/haptic", iteration, "1ms") for iteration in range(1, 4)
        ]
        samples, errors, malformed = ANALYZER.parse_probe_log("\n".join(shifted_lines))
        shifted = ANALYZER.build_result(
            samples,
            errors,
            malformed,
            3,
            3,
            1,
            {},
            expected_gateway_names=["bench/haptic"],
        )

        self.assertFalse(shifted["pass"])
        failures = {failure["code"]: failure for failure in shifted["failures"]}
        self.assertEqual(failures["route_ids"]["missing"], [0])
        self.assertEqual(failures["route_ids"]["unexpected"], [3])
        self.assertEqual(failures["gateway_route_ids"]["gateways"][0]["missing"], [0])
        self.assertEqual(failures["gateway_route_ids"]["gateways"][0]["unexpected"], [3])

        correct_lines = [
            success_line("bench/impostor", iteration, "1ms") for iteration in range(3)
        ]
        samples, errors, malformed = ANALYZER.parse_probe_log("\n".join(correct_lines))
        wrong_gateway = ANALYZER.build_result(
            samples,
            errors,
            malformed,
            3,
            3,
            1,
            {},
            expected_gateway_names=["bench/haptic"],
        )

        self.assertFalse(wrong_gateway["pass"])
        gateway_failure = next(
            failure for failure in wrong_gateway["failures"] if failure["code"] == "gateway_names"
        )
        self.assertEqual(gateway_failure["missing"], ["bench/haptic"])
        self.assertEqual(gateway_failure["unexpected"], ["bench/impostor"])

    def test_rejects_duplicate_labels_on_success_and_error_lines(self):
        text = "\n".join(
            [
                success_line("bench/haptic", 0, "1ms"),
                "gateway=bench/impostor gateway=bench/haptic iter=0 latency=2ms "
                "probe completed: 200",
                "gateway=bench/haptic iter=0 iter=1 latency=1ms "
                "unexpected status code: 503",
                "gateway=bench/haptic iter=0 latency=1ms latency=2ms "
                "probe completed: 200",
            ]
        )

        samples, errors, malformed = ANALYZER.parse_probe_log(text)
        result = ANALYZER.build_result(
            samples,
            errors,
            malformed,
            1,
            1,
            1,
            {},
            expected_gateway_names=["bench/haptic"],
        )

        self.assertEqual(len(samples), 1)
        self.assertEqual(errors, [])
        self.assertEqual(
            [line["reason"] for line in malformed],
            [
                "duplicate labels: gateway",
                "duplicate labels: iter",
                "duplicate labels: latency",
            ],
        )
        self.assertFalse(result["pass"])
        self.assertIn(
            "malformed_probe_lines",
            {failure["code"] for failure in result["failures"]},
        )

    def test_expected_gateway_names_must_match_expected_count(self):
        line = success_line("bench/haptic", 0, "1ms")
        samples, errors, malformed = ANALYZER.parse_probe_log(line)
        result = ANALYZER.build_result(
            samples,
            errors,
            malformed,
            1,
            1,
            1,
            {},
            expected_gateway_names=["bench/haptic", "bench/haptic"],
        )

        self.assertFalse(result["pass"])
        self.assertIn(
            "expected_gateway_names_count",
            {failure["code"] for failure in result["failures"]},
        )
        self.assertIn(
            "duplicate_expected_gateway_names",
            {failure["code"] for failure in result["failures"]},
        )

    def test_cli_merges_metadata_and_always_writes_failed_result(self):
        with tempfile.TemporaryDirectory() as temporary_directory:
            temporary = Path(temporary_directory)
            log = temporary / "probe.log"
            output = temporary / "result.json"
            metadata = temporary / "metadata.json"
            log.write_text(success_line("bench/haptic", 0, "1ms") + "\n", encoding="utf-8")
            metadata.write_text(
                json.dumps(
                    {
                        "provenance": {"upstream_commit": "e81292e"},
                        "resources": {"cpu": 1.5},
                    }
                ),
                encoding="utf-8",
            )
            command = [
                sys.executable,
                str(SCRIPT),
                "--log",
                str(log),
                "--output",
                str(output),
                "--expected-samples",
                "2",
                "--expected-routes",
                "2",
                "--expected-gateways",
                "1",
                "--expected-gateway",
                "bench/haptic",
                "--metadata",
                str(metadata),
            ]

            first = subprocess.run(command, capture_output=True, text=True, check=False)
            first_bytes = output.read_bytes()
            second = subprocess.run(command, capture_output=True, text=True, check=False)

            self.assertEqual(first.returncode, 1)
            self.assertEqual(second.returncode, 1)
            self.assertEqual(output.read_bytes(), first_bytes)
            result = json.loads(first_bytes)
            self.assertFalse(result["pass"])
            self.assertEqual(result["scenario"], "probe")
            self.assertEqual(result["provenance"]["upstream_commit"], "e81292e")
            self.assertEqual(result["resources"]["cpu"], 1.5)
            self.assertEqual(result["observed"]["samples"], 1)


if __name__ == "__main__":
    unittest.main()
