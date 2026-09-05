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

import base64
import copy
import importlib.util
import json
import os
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).parents[1] / "analyze-gateway-api-children.py"
RUNNER = Path(__file__).parents[1] / "bench-gateway-api.sh"
CI_CONFIG = Path(__file__).parents[2] / ".gitlab-ci.yml"
SPEC = importlib.util.spec_from_file_location("analyze_gateway_api_children", SCRIPT)
ANALYZER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = ANALYZER
SPEC.loader.exec_module(ANALYZER)


SCRIPTS = {
    "spoa-hub": """
_child_pid=
_start_watchdog() { :; }
/usr/local/bin/haproxy-spoa-hub --config /etc/haproxy/general/spoa-hub-config.toml &
""",
    "vector": """
_child_pid=
_start_watchdog() { :; }
/usr/bin/vector --watch-config --watch-config-method poll --watch-config-poll-interval-seconds 2 --config /etc/haproxy/general/vector.yaml &
""",
}


def container(name, ports=()):
    result = {"name": name}
    if name in SCRIPTS:
        result.update({"command": ["/bin/sh", "-c"], "args": [SCRIPTS[name]]})
    if ports:
        result["ports"] = [
            {"name": port_name, "containerPort": port} for port_name, port in ports
        ]
    return result


def topology_inputs():
    containers = [
        container("haproxy", (("stats", 8404),)),
        container("agent", (("dataplane", 5555),)),
        container("spoa-hub"),
        container("vector", (("vector-metrics", 9598),)),
    ]
    labels = {
        "app.kubernetes.io/instance": "haptic",
        "app.kubernetes.io/component": "loadbalancer",
    }
    workloads = {
        "items": [
            {
                "kind": "Deployment",
                "metadata": {
                    "namespace": "haptic",
                    "name": "haptic-haproxy",
                    "uid": "deployment-uid",
                    "generation": 2,
                    "labels": labels,
                },
                "spec": {
                    "template": {
                        "spec": {"shareProcessNamespace": False, "containers": containers}
                    }
                },
            }
        ]
    }
    statuses = [
        {
            "name": item["name"],
            "containerID": f"containerd://{item['name']}",
            "image": f"example/{item['name']}:tag",
            "imageID": f"example/{item['name']}@sha256:digest",
            "restartCount": 0,
            "ready": True,
        }
        for item in containers
    ]
    pods = {
        "items": [
            {
                "metadata": {
                    "namespace": "haptic",
                    "name": "haptic-haproxy-0",
                    "uid": "pod-uid",
                    "labels": labels,
                },
                "spec": {
                    "shareProcessNamespace": False,
                    "nodeName": "kind-worker",
                    "containers": copy.deepcopy(containers),
                },
                "status": {"containerStatuses": statuses},
            }
        ]
    }
    return workloads, pods


def capture(topology):
    children = []
    for index, task in enumerate(topology["tasks"], start=1):
        identity = {
            "process_count": 1,
            "matching_pids": [str(index + 10)],
            "pid": str(index + 10),
            "argv0": task["expected_executable"],
            "executable_inode_matches": True,
            "state": "S",
            "ppid": "1",
            "starttime": str(index + 1000),
        }
        status_code = next(
            (
                status
                for status in task["health"]["accepted_status_codes"]
                if status != "any-http"
            ),
            "200",
        )
        children.append(
            {
                **task,
                "boot_id": "01234567-89ab-cdef-0123-456789abcdef",
                "identity_before_health": copy.deepcopy(identity),
                "identity_after_health": copy.deepcopy(identity),
                "capture_stable": True,
                "health": {
                    **task["health"],
                    "pass": True,
                    "required_config_verified": True,
                    "probe_exit_code": 0,
                    "http_status_code": status_code,
                    "http_status_line": f"HTTP/1.0 {status_code} OK",
                },
            }
        )
    return {
        "schema_version": 1,
        "evidence_valid": True,
        "topology": topology,
        "children": children,
    }


class AnalyzeGatewayAPIChildrenTest(unittest.TestCase):
    def test_secret_patterns_exclude_only_owned_haptic_ssl_path_metadata(self):
        def encoded(value):
            return base64.b64encode(value.encode()).decode()

        owner = {
            "apiVersion": "haproxy-haptic.org/v1alpha1",
            "kind": "HAProxyCfg",
            "name": "haptic-config-haproxycfg",
            "uid": "runtime-config-uid",
            "controller": True,
        }
        labels = {
            "haproxy-haptic.org/type": "ssl-certificate",
            "haproxy-haptic.org/runtime-config": "haptic-config-haproxycfg",
        }
        items = [
            {
                "type": "Opaque",
                "metadata": {
                    "namespace": "haptic",
                    "name": "haproxy-cert-default",
                    "labels": labels,
                    "ownerReferences": [owner],
                },
                "data": {
                    "certificate": encoded("fixture-private-key-material"),
                    "path": encoded("default.pem"),
                },
            },
            {
                "type": "Opaque",
                "metadata": {"namespace": "default", "name": "ordinary"},
                "data": {"path": encoded("ordinary-secret-path")},
            },
            {
                "type": "Opaque",
                "metadata": {
                    "namespace": "haptic",
                    "name": "lookalike-wrong-owner",
                    "labels": labels,
                    "ownerReferences": [{**owner, "name": "other-haproxycfg"}],
                },
                "data": {"path": encoded("lookalike-wrong-owner-path")},
            },
            {
                "type": "Opaque",
                "metadata": {
                    "namespace": "haptic",
                    "name": "lookalike-wrong-owner-uid",
                    "labels": labels,
                    "ownerReferences": [{**owner, "uid": "other-runtime-config-uid"}],
                },
                "data": {"path": encoded("lookalike-wrong-owner-uid-path")},
            },
            {
                "type": "Opaque",
                "metadata": {
                    "namespace": "haptic",
                    "name": "lookalike-no-runtime-label",
                    "labels": {"haproxy-haptic.org/type": "ssl-certificate"},
                    "ownerReferences": [owner],
                },
                "data": {"path": encoded("lookalike-no-label-path")},
            },
        ]
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            secrets = directory / "secrets.json"
            runtime_configs = directory / "runtime-configs.json"
            patterns = directory / "patterns.json"
            secrets.write_text(json.dumps({"items": items}), encoding="utf-8")
            runtime_configs.write_text(
                json.dumps(
                    {
                        "items": [
                            {
                                "apiVersion": "haproxy-haptic.org/v1alpha1",
                                "kind": "HAProxyCfg",
                                "metadata": {
                                    "namespace": "haptic",
                                    "name": "haptic-config-haproxycfg",
                                    "uid": "runtime-config-uid",
                                },
                            }
                        ]
                    }
                ),
                encoding="utf-8",
            )
            command = r'''
source "$1"
trap - EXIT INT TERM
build_live_secret_patterns "$2" "$3" "$4"
'''
            result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "secret-pattern-test",
                    str(RUNNER),
                    str(secrets),
                    str(runtime_configs),
                    str(patterns),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            entries = json.loads(patterns.read_text(encoding="utf-8"))["patterns"]
            sources = [entry["source"] for entry in entries]
            self.assertNotIn("haptic/haproxy-cert-default/path", sources)
            for source in (
                "haptic/haproxy-cert-default/certificate",
                "default/ordinary/path",
                "haptic/lookalike-wrong-owner/path",
                "haptic/lookalike-wrong-owner-uid/path",
                "haptic/lookalike-no-runtime-label/path",
            ):
                self.assertEqual(sources.count(source), 2)
                self.assertEqual(
                    {
                        entry["representation"]
                        for entry in entries
                        if entry["source"] == source
                    },
                    {"decoded", "base64"},
                )

    def test_secret_scanner_preserves_haptic_ssl_path_and_rejects_other_values(self):
        def encoded(value):
            return base64.b64encode(value.encode()).decode()

        certificate = "fixture-private-key-material"
        ordinary_path = "ordinary-secret-path"
        owner = {
            "apiVersion": "haproxy-haptic.org/v1alpha1",
            "kind": "HAProxyCfg",
            "name": "haptic-config-haproxycfg",
            "uid": "runtime-config-uid",
            "controller": True,
        }
        secrets_document = {
            "items": [
                {
                    "type": "Opaque",
                    "metadata": {
                        "namespace": "haptic",
                        "name": "haproxy-cert-default",
                        "labels": {
                            "haproxy-haptic.org/type": "ssl-certificate",
                            "haproxy-haptic.org/runtime-config": "haptic-config-haproxycfg",
                        },
                        "ownerReferences": [owner],
                    },
                    "data": {
                        "certificate": encoded(certificate),
                        "path": encoded("default.pem"),
                    },
                },
                {
                    "type": "Opaque",
                    "metadata": {"namespace": "default", "name": "ordinary"},
                    "data": {"path": encoded(ordinary_path)},
                },
            ]
        }
        command = r'''
source "$1"
trap - EXIT INT TERM
build_live_secret_patterns "$2" "$3" "$4"
redact_secret_matches "$4" "$5" "$6"
'''
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            secrets = directory / "secrets.json"
            runtime_configs = directory / "runtime-configs.json"
            patterns = directory / "patterns.json"
            secrets.write_text(json.dumps(secrets_document), encoding="utf-8")
            runtime_configs.write_text(
                json.dumps(
                    {
                        "items": [
                            {
                                "apiVersion": "haproxy-haptic.org/v1alpha1",
                                "kind": "HAProxyCfg",
                                "metadata": {
                                    "namespace": "haptic",
                                    "name": "haptic-config-haproxycfg",
                                    "uid": "runtime-config-uid",
                                },
                            }
                        ]
                    }
                ),
                encoding="utf-8",
            )

            path_artifacts = directory / "path-artifacts"
            path_artifacts.mkdir()
            path_file = path_artifacts / "manifest.yaml"
            path_file.write_text("sslCertificates:\n  default.pem: {}\n", encoding="utf-8")
            path_report = directory / "path-report.json"
            path_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "secret-path-scan-test",
                    str(RUNNER),
                    str(secrets),
                    str(runtime_configs),
                    str(patterns),
                    str(path_artifacts),
                    str(path_report),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(path_result.returncode, 0, path_result.stderr)
            self.assertIn("default.pem", path_file.read_text(encoding="utf-8"))
            path_scan = json.loads(path_report.read_text(encoding="utf-8"))
            self.assertTrue(path_scan["pass"])
            self.assertEqual(path_scan["redacted"], [])
            self.assertEqual(
                path_scan["method"],
                "bytewise raw-base64 and decoded captured sensitive Secret value scan; HAPTIC SSL path metadata excluded",
            )

            sensitive_artifacts = directory / "sensitive-artifacts"
            sensitive_artifacts.mkdir()
            decoded_file = sensitive_artifacts / "decoded.txt"
            base64_file = sensitive_artifacts / "base64.txt"
            ordinary_file = sensitive_artifacts / "ordinary.txt"
            decoded_file.write_text(certificate, encoding="utf-8")
            base64_file.write_text(encoded(certificate), encoding="utf-8")
            ordinary_file.write_text(ordinary_path, encoding="utf-8")
            sensitive_report = directory / "sensitive-report.json"
            sensitive_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "sensitive-secret-scan-test",
                    str(RUNNER),
                    str(secrets),
                    str(runtime_configs),
                    str(patterns),
                    str(sensitive_artifacts),
                    str(sensitive_report),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(sensitive_result.returncode, 1, sensitive_result.stderr)
            sensitive_scan = json.loads(sensitive_report.read_text(encoding="utf-8"))
            self.assertFalse(sensitive_scan["pass"])
            self.assertEqual(len(sensitive_scan["redacted"]), 3)
            for artifact in (decoded_file, base64_file, ordinary_file):
                self.assertEqual(artifact.read_text(encoding="utf-8"), "<redacted>")

            residual_secrets = directory / "residual-secrets.json"
            residual_patterns = directory / "residual-patterns.json"
            residual_artifacts = directory / "residual-artifacts"
            residual_artifacts.mkdir()
            residual_file = residual_artifacts / "residual.txt"
            residual_report = directory / "residual-report.json"
            residual_secrets.write_text(
                json.dumps(
                    {
                        "items": [
                            {
                                "type": "Opaque",
                                "metadata": {
                                    "namespace": "default",
                                    "name": "residual",
                                },
                                "data": {"password": encoded("<redacted>")},
                            }
                        ]
                    }
                ),
                encoding="utf-8",
            )
            residual_file.write_text("<redacted>", encoding="utf-8")
            residual_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "residual-secret-scan-test",
                    str(RUNNER),
                    str(residual_secrets),
                    str(runtime_configs),
                    str(residual_patterns),
                    str(residual_artifacts),
                    str(residual_report),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(residual_result.returncode, 2, residual_result.stderr)
            self.assertFalse(residual_report.exists())
            self.assertIn(
                "artifact redaction left a selected Secret value",
                residual_result.stderr,
            )

    def test_extracts_exact_supervised_topology(self):
        workloads, pods = topology_inputs()
        result = ANALYZER.extract_topology(workloads, pods)
        self.assertEqual(result["supervised_container_names"], ["spoa-hub", "vector"])
        self.assertEqual(
            [item["expected_executable"] for item in result["tasks"]],
            [
                "/usr/local/bin/haproxy-spoa-hub",
                "/usr/bin/vector",
            ],
        )
        self.assertEqual(
            [item["health"]["port"] for item in result["tasks"]],
            [8404, 9598],
        )
        vector = next(item for item in result["tasks"] if item["container"] == "vector")
        self.assertEqual(vector["health"]["method"], "GET")
        self.assertEqual(
            vector["health"]["required_config_file"],
            "/etc/haproxy/general/vector.yaml",
        )

    def test_rejects_shared_process_namespace(self):
        workloads, pods = topology_inputs()
        workloads["items"][0]["spec"]["template"]["spec"]["shareProcessNamespace"] = True
        with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "shareProcessNamespace=false"):
            ANALYZER.extract_topology(workloads, pods)

    def test_rejects_unknown_supervisor(self):
        workloads, pods = topology_inputs()
        unknown = {
            "name": "other",
            "command": ["/bin/sh", "-c"],
            "args": ["_child_pid=\n_start_watchdog() { :; }\n/usr/bin/other &\n"],
        }
        workloads["items"][0]["spec"]["template"]["spec"]["containers"].append(unknown)
        with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "unknown supervised container"):
            ANALYZER.extract_topology(workloads, pods)

    def test_rejects_known_child_injected_only_into_pod(self):
        workloads, pods = topology_inputs()
        workloads["items"][0]["spec"]["template"]["spec"]["containers"] = [
            item
            for item in workloads["items"][0]["spec"]["template"]["spec"]["containers"]
            if item["name"] != "vector"
        ]
        with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "unexpected supervised container vector"):
            ANALYZER.extract_topology(workloads, pods)

    def test_rejects_unrecognized_child_executable(self):
        workloads, pods = topology_inputs()
        unrecognized = SCRIPTS["spoa-hub"].replace(
            "/usr/local/bin/haproxy-spoa-hub", "/tmp/haproxy-spoa-hub"
        )
        for document in (workloads["items"][0]["spec"]["template"], pods["items"][0]):
            target = next(
                item for item in document["spec"]["containers"] if item["name"] == "spoa-hub"
            )
            target["args"] = [unrecognized]
        with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "derive one child executable"):
            ANALYZER.extract_topology(workloads, pods)

    def test_rejects_missing_or_duplicate_named_port(self):
        for replacement in ([], [{"name": "stats", "containerPort": 8404}] * 2):
            with self.subTest(replacement=replacement):
                workloads, pods = topology_inputs()
                deployment_container = next(
                    item
                    for item in workloads["items"][0]["spec"]["template"]["spec"]["containers"]
                    if item["name"] == "haproxy"
                )
                deployment_container["ports"] = copy.deepcopy(replacement)
                with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "one numeric stats port"):
                    ANALYZER.extract_topology(workloads, pods)

    def test_baseline_and_continuity_pass(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        before = capture(topology)
        self.assertTrue(ANALYZER.baseline_result(before)["pass"])
        self.assertTrue(ANALYZER.continuity_result(before, copy.deepcopy(before))["pass"])

    def test_baseline_recomputes_capture_stability(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        before = capture(topology)
        before["children"][0]["identity_after_health"]["pid"] = "99"
        before["children"][0]["identity_after_health"]["matching_pids"] = ["99"]
        before["children"][0]["identity_after_health"]["starttime"] = "9000"
        self.assertTrue(before["children"][0]["capture_stable"])
        with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "baseline is missing"):
            ANALYZER.baseline_result(before)

    def test_restart_is_a_product_negative(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        before = capture(topology)
        after = copy.deepcopy(before)
        after["children"][0]["identity_before_health"]["pid"] = "99"
        after["children"][0]["identity_before_health"]["matching_pids"] = ["99"]
        after["children"][0]["identity_before_health"]["starttime"] = "9000"
        after["children"][0]["identity_after_health"] = copy.deepcopy(
            after["children"][0]["identity_before_health"]
        )
        result = ANALYZER.continuity_result(before, after)
        self.assertTrue(result["evidence_valid"])
        self.assertFalse(result["pass"])
        self.assertIn(
            "child boot ID, PID, starttime, or argv0 changed",
            result["children"][0]["negative_reasons"],
        )

    def test_unhealthy_final_child_is_a_product_negative(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        before = capture(topology)
        after = copy.deepcopy(before)
        after["children"][1]["health"].update(
            {
                "pass": False,
                "probe_exit_code": 124,
                "http_status_code": None,
                "http_status_line": None,
            }
        )
        result = ANALYZER.continuity_result(before, after)
        self.assertTrue(result["evidence_valid"])
        self.assertFalse(result["pass"])
        self.assertIn(
            "final health check failed", result["children"][1]["negative_reasons"]
        )

    def test_missing_final_process_is_a_product_negative(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        before = capture(topology)
        after = copy.deepcopy(before)
        missing = {
            "process_count": 0,
            "matching_pids": [],
            "pid": None,
            "argv0": None,
            "executable_inode_matches": False,
            "state": None,
            "ppid": None,
            "starttime": None,
        }
        after["children"][1]["identity_before_health"] = copy.deepcopy(missing)
        after["children"][1]["identity_after_health"] = copy.deepcopy(missing)
        after["children"][1]["health"].update(
            {
                "pass": False,
                "probe_exit_code": 10,
                "http_status_code": None,
                "http_status_line": None,
            }
        )
        result = ANALYZER.continuity_result(before, after)
        self.assertTrue(result["evidence_valid"])
        self.assertFalse(result["pass"])
        self.assertIn(
            "final process is missing, ambiguous, invalid, or changed during capture",
            result["children"][1]["negative_reasons"],
        )

    def test_missing_final_capture_is_evidence_invalid(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        before = capture(topology)
        after = copy.deepcopy(before)
        after["children"].pop()
        with self.assertRaisesRegex(ANALYZER.AnalysisFailure, "topology tasks"):
            ANALYZER.continuity_result(before, after)

    def test_cli_keeps_product_negative_as_valid_result(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        before = capture(topology)
        after = copy.deepcopy(before)
        after["children"][0]["health"].update(
            {
                "pass": False,
                "probe_exit_code": 124,
                "http_status_code": None,
                "http_status_line": None,
            }
        )
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            before_path = directory / "before.json"
            after_path = directory / "after.json"
            output_path = directory / "output.json"
            before_path.write_text(json.dumps(before), encoding="utf-8")
            after_path.write_text(json.dumps(after), encoding="utf-8")
            result = subprocess.run(
                [
                    sys.executable,
                    str(SCRIPT),
                    "continuity",
                    "--before",
                    str(before_path),
                    "--after",
                    str(after_path),
                    "--output",
                    str(output_path),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                json.loads(output_path.read_text(encoding="utf-8"))["pass"], False
            )

    def test_cli_rejects_malformed_capture(self):
        with tempfile.TemporaryDirectory() as directory:
            output_path = Path(directory) / "output.json"
            malformed = Path(directory) / "malformed.json"
            malformed.write_text("not JSON\n", encoding="utf-8")
            result = subprocess.run(
                [
                    sys.executable,
                    str(SCRIPT),
                    "baseline",
                    "--input",
                    str(malformed),
                    "--output",
                    str(output_path),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 1)
            self.assertFalse(
                json.loads(output_path.read_text(encoding="utf-8"))["evidence_valid"]
            )

    def test_proc_stat_parser_uses_last_closing_parenthesis(self):
        runner = RUNNER.read_text(encoding="utf-8")
        match = re.search(
            r"<<'CHILD_CAPTURE' \|\| true\n(.*?)\nCHILD_CAPTURE", runner, re.DOTALL
        )
        self.assertIsNotNone(match)
        fields = ["S", "1"] + ["0"] * 17 + ["987654"]
        stat = "42 (worker name) with ) inside) " + " ".join(fields)
        result = subprocess.run(
            ["/usr/bin/bash", "-c", match.group(1), "child-capture", "--parse-stat", stat],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.splitlines(), ["S", "1", "987654"])

    def test_runner_summary_retains_product_negative(self):
        topology = ANALYZER.extract_topology(*topology_inputs())
        continuity = ANALYZER.continuity_result(capture(topology), capture(topology))
        continuity["pass"] = False
        continuity["negative_children"] = ["haptic/pod/vector"]
        analysis = {
            "scenario": "probe",
            "measurement_valid": True,
            "pass": False,
            "upstream_program": {"pass": True},
            "haptic_non_vacuity": {"pass": None},
            "haptic_scenario_quality": {"pass": False},
            "resource_analysis": {"pass": True},
            "supervised_child_continuity": continuity,
        }
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            (output / "probe").mkdir()
            (output / "probe" / "analysis.json").write_text(
                json.dumps(analysis), encoding="utf-8"
            )
            command = (
                f"source {RUNNER!s}; trap - EXIT; "
                f"BENCH_OUTPUT_DIR={output!s}; SCENARIOS=(probe); write_runner_summary"
            )
            result = subprocess.run(
                ["/usr/bin/bash", "-c", command],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            summary = json.loads((output / "runner-summary.json").read_text(encoding="utf-8"))
            self.assertFalse(summary["scenarios"][0]["supervised_child_continuity"]["pass"])
            self.assertFalse(summary["measured_result"]["pass"])

    def test_attach_resource_analysis_preserves_gating_presence(self):
        cases = (
            (
                "explicit non-gating result",
                {"analysis_status": "not_gated", "gating": False, "pass": None},
                {
                    "artifact": "resources.json",
                    "status": "not_gated",
                    "gating": False,
                    "pass": None,
                },
            ),
            (
                "gating omitted",
                {"pass": True},
                {
                    "artifact": "resources.json",
                    "status": "passed",
                    "gating": True,
                    "pass": True,
                },
            ),
        )
        for name, resources, expected in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                scenario = Path(directory)
                (scenario / "analysis.json").write_text(
                    json.dumps({"scenario": "probe"}), encoding="utf-8"
                )
                (scenario / "resources.json").write_text(
                    json.dumps(resources), encoding="utf-8"
                )
                result = subprocess.run(
                    [
                        "/usr/bin/bash",
                        "-c",
                        'source "$1"; trap - EXIT INT TERM; '
                        'attach_resource_analysis "$2"',
                        "attach-resource-analysis-test",
                        str(RUNNER),
                        str(scenario),
                    ],
                    check=False,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                analysis = json.loads(
                    (scenario / "analysis.json").read_text(encoding="utf-8")
                )
                self.assertEqual(analysis["resource_analysis"], expected)

    def test_effective_profile_normalizes_helm_null_defaults(self):
        cases = (
            (
                "nullable default omitted by Helm",
                {"nullable": None},
                {},
                0,
                [],
            ),
            (
                "non-null default removed",
                {"required": "default"},
                {},
                1,
                [("required", True, False)],
            ),
            (
                "unexpected effective null",
                {},
                {"unexpectedNull": None},
                1,
                [("unexpectedNull", False, True)],
            ),
            (
                "allowed memory limit removed",
                {"controller": {"resources": {"limits": {"memory": "512Mi"}}}},
                {"controller": {"resources": {"limits": {}}}},
                0,
                [("controller.resources.limits.memory", True, False)],
            ),
        )
        for name, defaults, effective, returncode, expected_differences in cases:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                directory = Path(directory)
                defaults_path = directory / "defaults.json"
                effective_path = directory / "effective.json"
                output_path = directory / "output.json"
                defaults_path.write_text(json.dumps(defaults), encoding="utf-8")
                effective_path.write_text(json.dumps(effective), encoding="utf-8")
                result = subprocess.run(
                    [
                        "/usr/bin/bash",
                        "-c",
                        'source "$1"; trap - EXIT INT TERM; '
                        'assert_effective_profile "$2" "$3" "$4"',
                        "effective-profile-test",
                        str(RUNNER),
                        str(defaults_path),
                        str(effective_path),
                        str(output_path),
                    ],
                    check=False,
                    capture_output=True,
                    text=True,
                )
                self.assertEqual(result.returncode, returncode, result.stderr)
                analysis = json.loads(output_path.read_text(encoding="utf-8"))
                self.assertEqual(analysis["schema_version"], 2)
                self.assertEqual(
                    [
                        (
                            item["path"],
                            item["chart_default_present"],
                            item["effective_present"],
                        )
                        for item in analysis["differences"]
                    ],
                    expected_differences,
                )

    def test_haproxycfg_convergence_uses_deployed_checksums(self):
        cfg = {
            "metadata": {
                "name": "config",
                "namespace": "haptic",
                "uid": "config-uid",
                "generation": 9,
                "resourceVersion": "90",
                "annotations": {"haproxy-haptic.org/auxiliary-set-id": "set-id"},
            },
            "spec": {"checksum": "config-checksum"},
            "status": {
                "auxiliaryFiles": {
                    "setID": "set-id",
                    "mapFiles": [
                        {"kind": "HAProxyMapFile", "name": "host-map"}
                    ],
                },
                "deployedToPods": [
                    {
                        "podName": "haproxy-0",
                        "podUID": "uid-0",
                        "checksum": "config-checksum",
                    },
                    {
                        "podName": "haproxy-1",
                        "podUID": "uid-1",
                        "checksum": "config-checksum",
                    },
                ],
            },
        }
        pods = {
            "items": [
                {
                    "metadata": {"name": f"haproxy-{index}", "uid": f"uid-{index}"},
                    "status": {
                        "conditions": [{"type": "Ready", "status": "True"}],
                        "containerStatuses": [
                            {"name": "haproxy", "ready": True, "restartCount": 0}
                        ],
                    },
                }
                for index in range(2)
            ]
        }
        maps = {
            "items": [
                {
                    "metadata": {
                        "name": "host-map",
                        "namespace": "haptic",
                        "uid": "map-uid",
                        "generation": 1,
                        "resourceVersion": "91",
                        "annotations": {
                            "haproxy-haptic.org/auxiliary-set-id": "set-id"
                        },
                    },
                    "spec": {
                        "mapName": "host.map",
                        "path": "host.map",
                        "checksum": "map-checksum",
                    },
                    "status": {
                        "deployedToPods": [
                            {
                                "podName": "haproxy-0",
                                "podUID": "uid-0",
                                "podRuntimeID": "runtime-0",
                                "checksum": "map-checksum",
                            },
                            {
                                "podName": "haproxy-1",
                                "podUID": "uid-1",
                                "podRuntimeID": "runtime-1",
                                "checksum": "map-checksum",
                            },
                        ]
                    },
                }
            ]
        }
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            fixtures = {}
            for name, value in (("cfg", cfg), ("pods", pods), ("maps", maps)):
                fixtures[name] = directory / f"fixture-{name}.json"
                fixtures[name].write_text(json.dumps(value), encoding="utf-8")
            scenario = directory / "scenario"
            scenario.mkdir()
            output = scenario / "haproxycfg.json"
            map_output = scenario / "map-inventory.json"
            command = r'''
source "$1"
trap - EXIT INT TERM
WORK_DIR="$2"
CFG_FIXTURE="$3"
PODS_FIXTURE="$4"
MAPS_FIXTURE="$5"
kubectl() {
    case "$2" in
        haproxycfg) cat "$CFG_FIXTURE" ;;
        pods) cat "$PODS_FIXTURE" ;;
        haproxymapfiles) cat "$MAPS_FIXTURE" ;;
        *) return 1 ;;
    esac
}
wait_for_haproxycfg_converged "" "$6"
capture_referenced_map_inventory "$6" "${6%.json}-pods.json" "$7"
'''
            result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "haproxycfg-convergence-test",
                    str(RUNNER),
                    str(directory),
                    str(fixtures["cfg"]),
                    str(fixtures["pods"]),
                    str(fixtures["maps"]),
                    str(output),
                    str(map_output),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                json.loads(output.read_text(encoding="utf-8"))["spec"]["checksum"],
                "config-checksum",
            )
            self.assertEqual(
                json.loads(map_output.read_text(encoding="utf-8"))["maps"][0]["checksum"],
                "map-checksum",
            )
            self.assertEqual(
                json.loads(map_output.read_text(encoding="utf-8"))["maps"][0][
                    "deployed_to_pods"
                ][0]["pod_runtime_id"],
                "runtime-0",
            )

            malformed_cases = []
            malformed_pod_uid = copy.deepcopy(maps)
            malformed_pod_uid["items"][0]["status"]["deployedToPods"][0][
                "podUID"
            ] = 7
            malformed_cases.append(("pod-uid-number", malformed_pod_uid))
            for field in ("mapName", "path", "checksum"):
                for variant, value in (("null", None), ("empty", "")):
                    malformed = copy.deepcopy(maps)
                    malformed["items"][0]["spec"][field] = value
                    malformed_cases.append((f"{field}-{variant}", malformed))
                malformed = copy.deepcopy(maps)
                del malformed["items"][0]["spec"][field]
                malformed_cases.append((f"{field}-missing", malformed))
            for case_name, malformed_maps in malformed_cases:
                with self.subTest(case_name=case_name):
                    malformed_maps_path = directory / f"fixture-{case_name}.json"
                    malformed_maps_path.write_text(
                        json.dumps(malformed_maps), encoding="utf-8"
                    )
                    malformed_result = subprocess.run(
                        [
                            "/usr/bin/bash",
                            "-c",
                            command,
                            "haproxycfg-malformed-map-test",
                            str(RUNNER),
                            str(directory),
                            str(fixtures["cfg"]),
                            str(fixtures["pods"]),
                            str(malformed_maps_path),
                            str(scenario / f"{case_name}-haproxycfg.json"),
                            str(scenario / f"{case_name}-map-inventory.json"),
                        ],
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                    self.assertEqual(
                        malformed_result.returncode, 2, malformed_result.stderr
                    )

    def test_haproxycfg_poll_distinguishes_deadline_from_evidence_failure(self):
        cfg = {
            "metadata": {
                "name": "config",
                "uid": "config-uid",
                "generation": 17,
                "resourceVersion": "200",
            },
            "spec": {"checksum": "current-checksum"},
            "status": {
                "deployedToPods": [
                    {
                        "podName": "haproxy-0",
                        "podUID": "uid-0",
                        "checksum": "previous-checksum",
                    }
                ]
            },
        }
        pods = {
            "items": [
                {
                    "metadata": {"name": "haproxy-0", "uid": "uid-0"},
                    "status": {
                        "conditions": [{"type": "Ready", "status": "True"}],
                        "containerStatuses": [
                            {"name": "haproxy", "ready": True, "restartCount": 0}
                        ],
                    },
                }
            ]
        }
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            cfg_path = directory / "cfg.json"
            pods_path = directory / "pods.json"
            cfg_path.write_text(json.dumps(cfg), encoding="utf-8")
            pods_path.write_text(json.dumps(pods), encoding="utf-8")
            command = r'''
source "$1"
trap - EXIT INT TERM
WORK_DIR="$2"
CFG_FIXTURE="$3"
PODS_FIXTURE="$4"
kubectl() {
    [[ "${READ_FAILURE:-false}" != "true" ]] || return 1
    case "$2" in
        haproxycfg) cat "$CFG_FIXTURE" ;;
        pods) cat "$PODS_FIXTURE" ;;
        *) return 1 ;;
    esac
}
set +e
poll_for_haproxycfg_converged "" "$5" "" "$((SECONDS + ${DEADLINE_OFFSET:-3}))" 0.01 "$6"
rc=$?
set -e
printf '%d\n' "$rc" > "$7"
'''
            timeout_output = directory / "timeout-cfg.json"
            timeout_report = directory / "timeout-report.json"
            timeout_code = directory / "timeout-code.txt"
            timeout_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "haproxycfg-timeout-test",
                    str(RUNNER),
                    str(directory),
                    str(cfg_path),
                    str(pods_path),
                    str(timeout_output),
                    str(timeout_report),
                    str(timeout_code),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(timeout_result.returncode, 0, timeout_result.stderr)
            self.assertEqual(timeout_code.read_text(encoding="utf-8").strip(), "1")
            timeout = json.loads(timeout_report.read_text(encoding="utf-8"))
            self.assertEqual(timeout["outcome"], "deadline")
            self.assertEqual(timeout["reason_code"], "exact-current-timeout")
            self.assertTrue(timeout["evidence_valid"])
            self.assertFalse(timeout["pass"])
            self.assertTrue(timeout["deadline_reached"])
            # Needs a deadline of several whole seconds, not one: SECONDS counts
            # in whole seconds, so `SECONDS + 1` evaluated late in a second
            # leaves almost no real time and the poll can time out having
            # attempted nothing. Zero attempts is the deliberate contract for an
            # already-expired deadline, asserted below, so this scenario has to
            # buy a window wide enough to be sure it is not that case.
            self.assertGreaterEqual(timeout["attempts"], 1)
            self.assertEqual(timeout["poll_interval_seconds"], 0.01)
            self.assertFalse(timeout["last_observation"]["pass"])
            self.assertFalse(
                timeout["last_observation"]["checks"]["exact_current_deployment"]
            )
            self.assertEqual(
                json.loads(timeout_output.read_text(encoding="utf-8"))["spec"][
                    "checksum"
                ],
                "current-checksum",
            )

            invalid_output = directory / "invalid-cfg.json"
            invalid_report = directory / "invalid-report.json"
            invalid_code = directory / "invalid-code.txt"
            invalid_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "haproxycfg-evidence-test",
                    str(RUNNER),
                    str(directory),
                    str(cfg_path),
                    str(pods_path),
                    str(invalid_output),
                    str(invalid_report),
                    str(invalid_code),
                ],
                check=False,
                capture_output=True,
                text=True,
                env={**os.environ, "READ_FAILURE": "true"},
            )
            self.assertEqual(invalid_result.returncode, 0, invalid_result.stderr)
            self.assertEqual(invalid_code.read_text(encoding="utf-8").strip(), "2")
            invalid = json.loads(invalid_report.read_text(encoding="utf-8"))
            self.assertEqual(invalid["outcome"], "evidence-invalid")
            self.assertEqual(invalid["reason_code"], "haproxycfg-read-failed")
            self.assertFalse(invalid["evidence_valid"])
            self.assertFalse(invalid["pass"])
            self.assertFalse(invalid["deadline_reached"])
            self.assertEqual(invalid["attempts"], 1)
            self.assertFalse(invalid_output.exists())

            malformed_cfg = copy.deepcopy(cfg)
            del malformed_cfg["status"]["deployedToPods"][0]["podName"]
            malformed_cfg_path = directory / "malformed-cfg.json"
            malformed_cfg_path.write_text(json.dumps(malformed_cfg), encoding="utf-8")
            malformed_output = directory / "malformed-output.json"
            malformed_report = directory / "malformed-report.json"
            malformed_code = directory / "malformed-code.txt"
            malformed_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "haproxycfg-malformed-test",
                    str(RUNNER),
                    str(directory),
                    str(malformed_cfg_path),
                    str(pods_path),
                    str(malformed_output),
                    str(malformed_report),
                    str(malformed_code),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(malformed_result.returncode, 0, malformed_result.stderr)
            self.assertEqual(malformed_code.read_text(encoding="utf-8").strip(), "2")
            malformed = json.loads(malformed_report.read_text(encoding="utf-8"))
            self.assertEqual(malformed["outcome"], "evidence-invalid")
            self.assertEqual(
                malformed["reason_code"], "malformed-convergence-snapshot"
            )
            self.assertFalse(malformed["evidence_valid"])
            self.assertEqual(malformed["attempts"], 1)
            self.assertFalse(malformed_output.exists())

            expired_report = directory / "expired-report.json"
            expired_code = directory / "expired-code.txt"
            expired_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "haproxycfg-expired-deadline-test",
                    str(RUNNER),
                    str(directory),
                    str(cfg_path),
                    str(pods_path),
                    str(directory / "expired-output.json"),
                    str(expired_report),
                    str(expired_code),
                ],
                check=False,
                capture_output=True,
                text=True,
                env={**os.environ, "DEADLINE_OFFSET": "0"},
            )
            self.assertEqual(expired_result.returncode, 0, expired_result.stderr)
            self.assertEqual(expired_code.read_text(encoding="utf-8").strip(), "1")
            expired = json.loads(expired_report.read_text(encoding="utf-8"))
            self.assertEqual(expired["reason_code"], "exact-current-timeout")
            self.assertEqual(expired["attempts"], 0)
            self.assertTrue(expired["evidence_valid"])
            self.assertIsNone(expired["last_observation"])
            self.assertFalse((directory / "expired-output.json").exists())

    def test_haproxycfg_pending_status_is_valid_not_ready_evidence(self):
        cfg = {
            "metadata": {
                "uid": "config-uid",
                "generation": 17,
                "resourceVersion": "200",
            },
            "spec": {"checksum": "current-checksum"},
        }
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            cfg_path = directory / "cfg.json"
            pods_path = directory / "pods.json"
            observation = directory / "observation.json"
            cfg_path.write_text(json.dumps(cfg), encoding="utf-8")
            pods_path.write_text(
                '{"items":[{"metadata":{"name":"haproxy-0","uid":"pod-uid"},"status":{}}]}\n',
                encoding="utf-8",
            )
            result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    'source "$1"; trap - EXIT INT TERM; '
                    'validate_haproxycfg_convergence_inputs "$2" "$3" && '
                    'capture_haproxycfg_convergence_observation "$2" "$3" "" 1 "$4" && '
                    "jq -e '.pass == false and .checks.fleet_nonempty == true and "
                    ".checks.fleet_ready_and_restart_free == false' \"$4\" >/dev/null",
                    "haproxycfg-pending-test",
                    str(RUNNER),
                    str(cfg_path),
                    str(pods_path),
                    str(observation),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)

    def test_readiness_poll_rejects_workload_exit_during_observation(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            report = directory / "report.json"
            observation = directory / "observation.json"
            command = r'''
source "$1"
trap - EXIT INT TERM
running=true
workload_container_running() { [[ "$running" == "true" ]]; }
observe_then_exit() {
    jq -n --argjson attempt "$1" '{attempt: $attempt, pass: false}' > "$2"
    running=false
    return 1
}
set +e
poll_for_readiness observe_then_exit ready timeout workload "$((SECONDS + 10))" 10 0.01 "$2" "$3"
rc=$?
set -e
printf '%d\n' "$rc"
'''
            result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "readiness-workload-exit-test",
                    str(RUNNER),
                    str(report),
                    str(observation),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.strip(), "2")
            evidence = json.loads(report.read_text(encoding="utf-8"))
            self.assertEqual(evidence["reason_code"], "workload-exited")
            self.assertFalse(evidence["evidence_valid"])
            self.assertEqual(evidence["attempts"], 1)
            self.assertFalse(evidence["last_observation"]["evidence_valid"])

    def test_readiness_poll_internal_failures_are_evidence_invalid(self):
        command = r'''
source "$1"
trap - EXIT INT TERM
MODE="$2"
DATE_MARKER="$3/date-called"
date() {
    if [[ "$MODE" == "date-start" ]]; then
        return 1
    fi
    if [[ "$MODE" == "date-finish" ]]; then
        if [[ -e "$DATE_MARKER" ]]; then
            return 1
        fi
        : > "$DATE_MARKER"
    fi
    command date "$@"
}
sleep() {
    [[ "$MODE" != "sleep" ]] || return 1
    command sleep "$@"
}
observe_failure() {
    local pass=false
    [[ "$MODE" == "date-finish" ]] && pass=true
    if [[ "$MODE" == "malformed-unexpected" ]]; then
        printf '{' > "$2"
        return 7
    fi
    jq -n --argjson attempt "$1" --argjson pass "$pass" \
        '{attempt: $attempt, pass: $pass}' > "$2"
    [[ "$MODE" != "unexpected" ]] || return 7
    [[ "$pass" == "true" ]] && return 0
    return 1
}
set +e
poll_for_readiness observe_failure ready timeout "" "$((SECONDS + 10))" 10 0.01 "$4" "$5"
rc=$?
set -e
printf '%d\n' "$rc" > "$6"
'''
        cases = (
            ("date-start", "readiness-start-timestamp-failed", 0, False),
            ("date-finish", "readiness-finished-timestamp-failed", 1, True),
            ("initial-observation", "readiness-observation-initialization-failed", 0, False),
            ("sleep", "readiness-sleep-failed", 1, True),
            ("unexpected", "unexpected-readiness-callback-exit-7", 1, True),
            ("malformed-unexpected", "unexpected-readiness-callback-exit-7", 1, False),
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for mode, reason, attempts, has_available in cases:
                with self.subTest(mode=mode):
                    case_dir = root / mode
                    case_dir.mkdir()
                    report = case_dir / "report.json"
                    observation = case_dir / "observation.json"
                    if mode == "initial-observation":
                        observation = case_dir / "missing" / "observation.json"
                    code = case_dir / "code.txt"
                    result = subprocess.run(
                        [
                            "/usr/bin/bash",
                            "-c",
                            command,
                            "readiness-internal-failure-test",
                            str(RUNNER),
                            mode,
                            str(case_dir),
                            str(report),
                            str(observation),
                            str(code),
                        ],
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(code.read_text(encoding="utf-8").strip(), "2")
                    evidence = json.loads(report.read_text(encoding="utf-8"))
                    self.assertEqual(evidence["outcome"], "evidence-invalid")
                    self.assertEqual(evidence["reason_code"], reason)
                    self.assertFalse(evidence["evidence_valid"])
                    self.assertEqual(evidence["attempts"], attempts)
                    current = evidence["last_observation"]
                    self.assertEqual(current["attempt"], attempts)
                    self.assertEqual(current["reason_code"], reason)
                    self.assertFalse(current["evidence_valid"])
                    self.assertEqual(
                        current["last_available_observation"] is not None,
                        has_available,
                    )

    def test_haproxycfg_baseline_deadline_remains_fatal(self):
        runner = RUNNER.read_text(encoding="utf-8")
        wrapper = re.search(
            r"\nwait_for_haproxycfg_converged\(\) \{(.*?)\n\}", runner, re.DOTALL
        )
        self.assertIsNotNone(wrapper)
        self.assertIn("DEFAULT_HAPROXYCFG_POLL_INTERVAL_SECONDS", wrapper.group(1))
        self.assertIn('die "HAProxyCfg did not converge', wrapper.group(1))
        self.assertIn(
            'wait_for_haproxycfg_converged "" "$scenario_dir/haproxycfg-baseline.json"',
            runner,
        )
        self.assertIn(
            'wait_for_haproxycfg_converged "$checksum" "$scenario_dir/haproxycfg-final.json"',
            runner,
        )

    def test_scale_readiness_timeout_is_a_valid_negative_without_steady_metrics(self):
        readiness = {
            "reason_code": "exact-current-timeout",
            "failure_stage": "initial-exact-current",
            "steady_window_started": False,
            "pass": False,
            "convergence": {
                "outcome": "deadline",
                "evidence_valid": True,
                "pass": False,
                "deadline_reached": True,
                "attempts": 4,
            },
        }
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)
            scenario = output / "scale"
            scenario.mkdir()
            (scenario / "scale-readiness.json").write_text(
                json.dumps(readiness), encoding="utf-8"
            )
            command = r'''
source "$1"
trap - EXIT INT TERM
BENCH_OUTPUT_DIR="$2"
write_scale_readiness_timeout_analysis "$2/scale" 10 10
jq '.supervised_child_continuity = {evidence_valid: true, pass: true}' \
    "$2/scale/analysis.json" > "$2/scale/analysis.json.tmp"
mv "$2/scale/analysis.json.tmp" "$2/scale/analysis.json"
SCENARIOS=(scale)
write_runner_summary
finalize_runner_summary 0
'''
            result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "scale-readiness-timeout-test",
                    str(RUNNER),
                    str(output),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            analysis = json.loads((scenario / "analysis.json").read_text(encoding="utf-8"))
            self.assertTrue(analysis["measurement_valid"])
            self.assertFalse(analysis["pass"])
            self.assertTrue(analysis["haptic_scenario_quality"]["measurement_complete"])
            self.assertFalse(
                analysis["haptic_scenario_quality"]["steady_measurement_complete"]
            )
            self.assertFalse(analysis["steady_window"]["applicable"])
            self.assertIsNone(analysis["resource_analysis"]["artifact"])
            self.assertFalse(analysis["resource_analysis"]["gating"])
            self.assertIsNone(analysis["resource_analysis"]["pass"])
            self.assertFalse((scenario / "steady-prometheus-range").exists())
            summary = json.loads(
                (output / "runner-summary.json").read_text(encoding="utf-8")
            )
            self.assertTrue(summary["harness"]["pass"])
            self.assertEqual(summary["harness"]["final_exit_code"], 0)
            self.assertFalse(summary["measured_result"]["pass"])
            self.assertEqual(summary["measured_result"]["negative_scenarios"], ["scale"])

        runner = RUNNER.read_text(encoding="utf-8")
        self.assertIn('readonly SCALE_READINESS_POLL_INTERVAL_SECONDS="0.1"', runner)
        match = re.search(r"\nrun_scale\(\) \{(.*?)\n\}\n\nwrite_runner_summary", runner, re.DOTALL)
        self.assertIsNotNone(match)
        body = match.group(1)
        self.assertIn('record_event scale-readiness-timeout scale', body)
        self.assertIn('signal_running_workload_container "$active_workload_container"', body)
        signal = re.search(
            r"\nsignal_running_workload_container\(\) \{(.*?)\n\}", runner, re.DOTALL
        )
        self.assertIsNotNone(signal)
        self.assertNotIn("docker kill", signal.group(1))
        self.assertEqual(signal.group(1).count("signal_workload_container"), 1)
        self.assertLess(
            body.index('if (( SECONDS >= startup_deadline ))'),
            body.index('if [[ "$count" -eq "$expected_routes"'),
        )
        readiness_entry = body[
            body.index("record_event scale-route-snapshot scale") : body.index(
                "wait_for_scale_dataplane"
            )
        ]
        self.assertNotIn("capture_state", readiness_entry)
        self.assertLess(
            body.index('write_scale_readiness_timeout_analysis'),
            body.index('wait_for_haproxycfg_baseline'),
        )
        self.assertIn('if [[ "$readiness_timed_out" != "true" ]]; then', body)

    def test_every_scale_readiness_deadline_has_stage_specific_valid_evidence(self):
        cases = (
            ("route-status-timeout", "route-status", False, 3),
            ("exact-current-timeout", "initial-exact-current", False, 0),
            ("referenced-map-timeout", "initial-referenced-map", True, 0),
            ("runtime-map-timeout", "runtime-host-map", True, 3),
            ("exact-current-timeout", "post-live-exact-current", False, 0),
            ("referenced-map-timeout", "post-live-referenced-map", True, 3),
            ("semantic-token-timeout", "semantic-token", True, 3),
        )
        cfg = {
            "metadata": {"generation": 18},
            "spec": {"checksum": "scale-checksum"},
            "status": {"deployedToPods": []},
        }
        command = r'''
source "$1"
trap - EXIT INT TERM
write_scale_readiness_timeout "$2" 10 "$3" "$4" "$5"
'''
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cfg_path = root / "cfg.json"
            null_cfg_path = root / "null-cfg.json"
            cfg_path.write_text(json.dumps(cfg), encoding="utf-8")
            null_cfg_path.write_text("null\n", encoding="utf-8")
            for reason_code, stage, has_cfg, attempts in cases:
                with self.subTest(reason_code=reason_code):
                    scenario = root / stage
                    scenario.mkdir()
                    (scenario / "haproxycfg-baseline.json").write_text(
                        json.dumps({"metadata": {"generation": 17}}),
                        encoding="utf-8",
                    )
                    (scenario / "haproxycfg-baseline-checksum.txt").write_text(
                        "baseline-checksum\n", encoding="utf-8"
                    )
                    report = scenario / "deadline.json"
                    report.write_text(
                        json.dumps(
                            {
                                "outcome": "deadline",
                                "reason_code": reason_code,
                                "evidence_valid": True,
                                "pass": False,
                                "deadline_reached": True,
                                "attempts": attempts,
                            }
                        ),
                        encoding="utf-8",
                    )
                    cfg_evidence = cfg_path if has_cfg else null_cfg_path
                    if stage == "initial-exact-current":
                        cfg_evidence = root / f"{stage}-missing.json"
                    elif stage == "post-live-exact-current":
                        cfg_evidence = cfg_path
                    result = subprocess.run(
                        [
                            "/usr/bin/bash",
                            "-c",
                            command,
                            "scale-readiness-stage-test",
                            str(RUNNER),
                            str(scenario),
                            stage,
                            str(cfg_evidence),
                            str(report),
                        ],
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
                    readiness = json.loads(
                        (scenario / "scale-readiness.json").read_text(
                            encoding="utf-8"
                        )
                    )
                    self.assertEqual(readiness["reason_code"], reason_code)
                    self.assertEqual(readiness["failure_stage"], stage)
                    self.assertTrue(readiness["deadline_evidence"]["evidence_valid"])
                    self.assertEqual(readiness["deadline_evidence"]["attempts"], attempts)
                    self.assertFalse(readiness["pass"])
                    self.assertEqual(has_cfg, readiness["at_scale"] is not None)
                    self.assertEqual(
                        readiness["stage_observation"]["attempted"], attempts > 0
                    )
                    if attempts == 0 and not has_cfg:
                        self.assertIsNone(
                            readiness["stage_observation"]["haproxycfg_snapshot"]
                        )
                    stage_names = [case[1] for case in cases]
                    self.assertEqual(
                        readiness["completed_gates"],
                        {
                            name.replace("-", "_"): index < stage_names.index(stage)
                            for index, name in enumerate(stage_names)
                        },
                    )

        runner = RUNNER.read_text(encoding="utf-8")
        match = re.search(
            r"\nwait_for_scale_dataplane\(\) \{(.*?)\n\}\n\nvalidate_probe_evidence",
            runner,
            re.DOTALL,
        )
        self.assertIsNotNone(match)
        body = match.group(1)
        for reason_code, stage, _, _ in cases:
            self.assertIn(reason_code, runner)
            self.assertIn(stage, body)
        self.assertIn("poll_for_haptic_scale_routes", body)
        self.assertIn("poll_for_referenced_map_inventory", body)
        self.assertIn("poll_for_scale_live_host_map", body)
        self.assertNotIn("wait_for_referenced_map_inventory", body)
        self.assertNotIn("host.map semantic token kept moving", body)

    def test_scale_map_identifiers_follow_effective_maps_directory(self):
        config = {
            "spec": {
                "dataplane": {
                    "configFile": "/var/lib/haproxy/haproxy.cfg",
                    "mapsDir": "/etc/haproxy/maps",
                }
            }
        }
        cases = (
            ("host.map", 0, "maps/host.map"),
            ("maps/host.map", 0, "maps/host.map"),
            ("/etc/haproxy/maps/host.map", 0, "/etc/haproxy/maps/host.map"),
            ("../host.map", 1, ""),
            ("/tmp/host.map", 1, ""),
        )
        with tempfile.TemporaryDirectory() as directory:
            config_path = Path(directory) / "config.json"
            config_path.write_text(json.dumps(config), encoding="utf-8")
            for map_path, returncode, stdout in cases:
                with self.subTest(map_path=map_path):
                    result = subprocess.run(
                        [
                            "/usr/bin/bash",
                            "-c",
                            'source "$1"; trap - EXIT INT TERM; '
                            'resolve_map_runtime_identifier "$2" "$3"',
                            "map-path-test",
                            str(RUNNER),
                            map_path,
                            str(config_path),
                        ],
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                    self.assertEqual(result.returncode, returncode, result.stderr)
                    self.assertEqual(result.stdout.strip(), stdout)

    def test_runtime_map_capture_reads_worker_entries(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            raw = directory / "runtime.txt"
            keys = directory / "keys.txt"
            command = r'''
source "$1"
trap - EXIT INT TERM
kubectl() {
    printf '%s\n' \
        '0x00000002 b.example:2000 backend-b' \
        'diagnostic text' \
        '0x00000001 a.example:2000 backend-a'
}
capture_haproxy_runtime_map_entries haproxy-0 /etc/haproxy/maps/host.map "$2" "$3"
'''
            result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "runtime-map-test",
                    str(RUNNER),
                    str(raw),
                    str(keys),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(
                keys.read_text(encoding="utf-8"),
                "a.example:2000 backend-a\nb.example:2000 backend-b\n",
            )
            self.assertIn("0x00000002", raw.read_text(encoding="utf-8"))

    def test_live_host_map_proof_reads_current_worker_in_every_pod_twice(self):
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            scenario = directory / "scale"
            scenario.mkdir()
            cluster = directory / "cluster"
            cluster.mkdir()
            (cluster / "effective-template-config.json").write_text(
                json.dumps(
                    {
                        "spec": {
                            "dataplane": {
                                "configFile": "/etc/haproxy/haproxy.cfg",
                                "mapsDir": "/etc/haproxy/maps",
                            }
                        }
                    }
                ),
                encoding="utf-8",
            )
            (scenario / "routes-at-scale-snapshot.json").write_text(
                json.dumps(
                    {"items": [{"spec": {"hostnames": ["a.example.com"]}}]}
                ),
                encoding="utf-8",
            )
            (scenario / "map-inventory-at-scale.json").write_text(
                json.dumps(
                    {
                        "pods": [
                            {"name": "haproxy-0", "uid": "uid-0"},
                            {"name": "haproxy-1", "uid": "uid-1"},
                        ],
                        "maps": [
                            {
                                "map_name": "host.map",
                                "path": "host.map",
                                "checksum": "map-checksum",
                            }
                        ],
                    }
                ),
                encoding="utf-8",
            )
            command = r'''
source "$1"
trap - EXIT INT TERM
BENCH_OUTPUT_DIR="$2"
GATEWAYS=("bench/gateway")
CALLS="$3"
MISMATCH="$4"
capture_gateway_service_targets() {
    printf '%s\n' '[{"namespace":"bench","gateway_name":"gateway","target_port":2000}]' > "$1"
}
kubectl() {
    printf 'call\n' >> "$CALLS"
    count=$(wc -l < "$CALLS")
    if [[ "$MISMATCH" == true && "$count" -eq 4 ]]; then
        printf '%s\n' '0x00000001 a.example.com:2000 wrong-value'
    else
        printf '%s\n' '0x00000001 a.example.com:2000 a.example.com:2000'
    fi
}
prove_scale_live_host_map "$5" 1
'''
            for mismatch, returncode in ((False, 0), (True, 1)):
                with self.subTest(mismatch=mismatch):
                    calls = directory / f"calls-{mismatch}.txt"
                    result = subprocess.run(
                        [
                            "/usr/bin/bash",
                            "-c",
                            command,
                            "live-host-map-test",
                            str(RUNNER),
                            str(directory),
                            str(calls),
                            str(mismatch).lower(),
                            str(scenario),
                        ],
                        check=False,
                        capture_output=True,
                        text=True,
                    )
                    self.assertEqual(result.returncode, returncode, result.stderr)
                    self.assertEqual(
                        calls.read_text(encoding="utf-8").splitlines(),
                        ["call", "call", "call", "call"],
                    )
            proof = json.loads(
                (scenario / "live-host-map.json").read_text(encoding="utf-8")
            )
            self.assertTrue(proof["exact_runtime_entries_on_every_pod"])
            self.assertEqual(proof["runtime_reads_per_pod"], 2)

    def test_runner_pod_cgroup_queries_exclude_pause_sandboxes(self):
        runner = RUNNER.read_text(encoding="utf-8")
        self.assertIn(
            r'container_cpu_usage_seconds_total{namespace=\"haptic\",container=\"\",image=\"\",name=\"\",pod=~',
            runner,
        )
        self.assertIn(
            r'container_memory_working_set_bytes{namespace=\"haptic\",container=\"\",image=\"\",name=\"\",pod=~',
            runner,
        )

    def test_scale_route_validation_is_silent_on_success(self):
        controller = "haproxy-haptic.org/controller"
        route = {
            "metadata": {
                "namespace": "bench",
                "name": "route",
                "uid": "route-uid",
                "generation": 4,
            },
            "spec": {"parentRefs": [{"name": "gateway"}]},
            "status": {
                "parents": [
                    {
                        "controllerName": controller,
                        "parentRef": {"name": "gateway"},
                        "conditions": [
                            {
                                "type": "Accepted",
                                "status": "True",
                                "reason": "Accepted",
                                "observedGeneration": 4,
                            },
                            {
                                "type": "ResolvedRefs",
                                "status": "True",
                                "reason": "ResolvedRefs",
                                "observedGeneration": 4,
                            },
                        ],
                    }
                ]
            },
        }
        with tempfile.TemporaryDirectory() as directory:
            directory = Path(directory)
            routes = directory / "routes.json"
            snapshot = directory / "snapshot.json"
            output = directory / "output.json"
            routes.write_text(json.dumps({"items": [route]}), encoding="utf-8")
            snapshot.write_text(json.dumps({"items": [route]}), encoding="utf-8")
            command = r'''
source "$1"
trap - EXIT INT TERM
ROUTES_FIXTURE="$2"
GATEWAYS=("bench/gateway")
kubectl() {
    case "$2" in
        httproutes.gateway.networking.k8s.io) cat "$ROUTES_FIXTURE" ;;
        gatewayclass) printf '%s\n' '{"spec":{"controllerName":"haproxy-haptic.org/controller"}}' ;;
        *) return 1 ;;
    esac
}
validate_haptic_scale_routes 1 "$3" "$4"
'''
            result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "scale-route-validation-test",
                    str(RUNNER),
                    str(routes),
                    str(output),
                    str(snapshot),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, "")

            lagging = copy.deepcopy(route)
            lagging["status"]["parents"] = []
            routes.write_text(json.dumps({"items": [lagging]}), encoding="utf-8")
            lagging_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "scale-route-lagging-test",
                    str(RUNNER),
                    str(routes),
                    str(output),
                    str(snapshot),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(lagging_result.returncode, 1, lagging_result.stderr)

            malformed = copy.deepcopy(route)
            malformed["status"]["parents"] = "not-an-array"
            routes.write_text(json.dumps({"items": [malformed]}), encoding="utf-8")
            malformed_result = subprocess.run(
                [
                    "/usr/bin/bash",
                    "-c",
                    command,
                    "scale-route-malformed-test",
                    str(RUNNER),
                    str(routes),
                    str(output),
                    str(snapshot),
                ],
                check=False,
                capture_output=True,
                text=True,
            )
            self.assertEqual(malformed_result.returncode, 2, malformed_result.stderr)

    def test_scale_stop_failure_is_immediately_fatal(self):
        runner = RUNNER.read_text(encoding="utf-8")
        match = re.search(r"\nrun_scale\(\) \{(.*?)\n\}\n\nwrite_runner_summary", runner, re.DOTALL)
        self.assertIsNotNone(match)
        self.assertIn(
            'if ! signal_workload_container "$active_workload_container"; then',
            match.group(1),
        )
        self.assertIn(
            'die "pilot-load could not be stopped after the steady-churn interval"',
            match.group(1),
        )

    def test_hosted_ci_job_is_an_explicit_bounded_smoke(self):
        ci_config = CI_CONFIG.read_text(encoding="utf-8")
        match = re.search(
            r"(?m)^gateway-api-benchmark-smoke:\n(.*?)(?=^# Upstream Kubernetes Ingress)",
            ci_config,
            re.DOTALL,
        )
        self.assertIsNotNone(match)
        job = match.group(1)
        for setting in (
            "image: ${CI_IMAGE_BASE}:${CI_IMAGE_TAG}-hp3.4",
            'timeout: 2 hours 45 minutes',
            'BENCH_REF: "e81292ed876472804e0a2245876a7c445ab80881"',
            'BENCH_GATEWAY_API_VERSION: "v1.4.0"',
            'BENCH_GATEWAYS: "haptic-bench/haptic"',
            'BENCH_SCENARIOS: "probe,scale,routechange"',
            'BENCH_PROBE_ROUTES: "300"',
            'BENCH_PROBE_TIMEOUT: "45m"',
            'BENCH_DEPLOY_INTERVAL: "100ms"',
            'BENCH_WATCH_DEBOUNCE: "100ms"',
            'BENCH_SCALE_NAMESPACES: "50"',
            'BENCH_SCALE_ROUTES_PER_NAMESPACE: "100"',
            'BENCH_SCALE_DURATION: "10m"',
            'BENCH_SCALE_STARTUP_TIMEOUT: "20m"',
            'BENCH_ROUTECHANGE_ITERATIONS: "20"',
            'BENCH_ROUTECHANGE_GRACE_PERIOD: "200ms"',
            'BENCH_ROUTECHANGE_TIMEOUT: "10m"',
            'HAPROXY_VERSION: "3.4"',
            'BENCH_KEEP_CLUSTER: "false"',
            'BENCH_ALLOW_DIRTY: "false"',
            'BENCH_ALLOW_COSCHEDULED_CLUSTERS: "false"',
            'REUSE_CLUSTER: "false"',
            'BUILD_ONLY: "false"',
            'timeout --preserve-status --kill-after=20m 135m ./scripts/bench-gateway-api.sh',
            '.comparison.published_workload_inputs_match == false',
            '.comparison.controlled_default_profile == false',
            'cluster/artifact-secret-scan.json',
            '.schema_version == 1 and .pass == true and .redacted == []',
            '(.scan_count | type) == "number"',
            '(.scan_count | floor) == .scan_count',
            '.scan_count >= 2',
            'HAPTIC SSL path metadata excluded',
        ):
            self.assertIn(setting, job)
        for assignment in (
            "HAPROXY_VERSION=3.4",
            "BENCH_REF=e81292ed876472804e0a2245876a7c445ab80881",
            "BENCH_GATEWAY_API_VERSION=v1.4.0",
            "BENCH_GATEWAYS=haptic-bench/haptic",
            "BENCH_SCENARIOS=probe,scale,routechange",
            "BENCH_PROBE_ROUTES=300",
            "BENCH_PROBE_TIMEOUT=45m",
            "BENCH_DEPLOY_INTERVAL=100ms",
            "BENCH_WATCH_DEBOUNCE=100ms",
            "BENCH_SCALE_NAMESPACES=50",
            "BENCH_SCALE_ROUTES_PER_NAMESPACE=100",
            "BENCH_SCALE_DURATION=10m",
            "BENCH_SCALE_STARTUP_TIMEOUT=20m",
            "BENCH_ROUTECHANGE_ITERATIONS=20",
            "BENCH_ROUTECHANGE_GRACE_PERIOD=200ms",
            "BENCH_ROUTECHANGE_TIMEOUT=10m",
            "BENCH_KEEP_CLUSTER=false",
            "BENCH_ALLOW_DIRTY=false",
            "BENCH_ALLOW_COSCHEDULED_CLUSTERS=false",
            "REUSE_CLUSTER=false",
            "BUILD_ONLY=false",
        ):
            self.assertIn(assignment, job)
        self.assertNotRegex(ci_config, r"(?m)^gateway-api-benchmark:")
        runner = RUNNER.read_text(encoding="utf-8")
        self.assertIn("readonly DEFAULT_PROBE_ROUTES=3000", runner)
        self.assertIn('BENCH_DEPLOY_INTERVAL="${BENCH_DEPLOY_INTERVAL:-}"', runner)
        self.assertIn('BENCH_WATCH_DEBOUNCE="${BENCH_WATCH_DEBOUNCE:-}"', runner)


if __name__ == "__main__":
    unittest.main()
