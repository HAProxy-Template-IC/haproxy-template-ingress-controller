#!/usr/bin/env python3
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
"""Validate supervised load-balancer child continuity for gateway-api-bench."""

import argparse
import json
import re
import sys
from dataclasses import dataclass
from pathlib import Path


SCHEMA_VERSION = 1
BOOT_ID = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
DECIMAL = re.compile(r"^[1-9][0-9]*$")
SUPERVISOR_MARKERS = ("_start_watchdog", "_child_pid")


class AnalysisFailure(Exception):
    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code
        self.message = message

    def result(self) -> dict:
        return {
            "schema_version": SCHEMA_VERSION,
            "evidence_valid": False,
            "pass": False,
            "failure": {"code": self.code, "message": self.message},
        }


@dataclass(frozen=True)
class ChildDefinition:
    executable_pattern: re.Pattern
    port_container: str
    port_name: str
    method: str
    path: str
    accepted_status_codes: tuple[str, ...]
    health_scope: str
    required_config_file: str | None = None
    required_config_pattern: str | None = None


CHILDREN = {
    "dataplane": ChildDefinition(
        re.compile(
            r"(?m)^\s*((?:/usr/local/bin/dataplaneapi|"
            r"/opt/hapee-extras/sbin/hapee-dataplaneapi)) "
            r"-f /etc/haproxy/dataplaneapi\.yaml &$"
        ),
        "dataplane",
        "dataplane",
        "GET",
        "/v2/info",
        ("any-http",),
        "unauthenticated listener response; not authenticated API functionality",
    ),
    "spoa-hub": ChildDefinition(
        re.compile(
            r"(?m)^\s*(/usr/local/bin/haproxy-spoa-hub) "
            r"--config /etc/haproxy/general/spoa-hub-config\.toml &$"
        ),
        "haproxy",
        "stats",
        "GET",
        "/spoa-hub-child-health",
        ("204",),
        "HAProxy reports the SPOA backend healthy",
    ),
    "vector": ChildDefinition(
        re.compile(
            r"(?m)^\s*(/usr/bin/vector) --watch-config --watch-config-method poll "
            r"--watch-config-poll-interval-seconds 2 --config "
            r"/etc/haproxy/general/vector\.yaml &$"
        ),
        "vector",
        "vector-metrics",
        "GET",
        "/metrics",
        ("200",),
        "Vector Prometheus exporter response",
        "/etc/haproxy/general/vector.yaml",
        r"^[[:space:]]*type:[[:space:]]*prometheus_exporter",
    ),
}


def fail(code: str, message: str):
    raise AnalysisFailure(code, message)


def load_document(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as handle:
            document = json.load(handle)
    except (OSError, json.JSONDecodeError) as error:
        fail("input", f"could not read {path}: {error}")
    if not isinstance(document, dict):
        fail("input", f"{path} must contain a JSON object")
    return document


def write_document(path: str, document: dict):
    temporary = f"{path}.tmp"
    with open(temporary, "w", encoding="utf-8") as handle:
        json.dump(document, handle, indent=2, sort_keys=True)
        handle.write("\n")
    Path(temporary).replace(path)


def unique_by_name(containers: list, owner: str) -> dict[str, dict]:
    if not isinstance(containers, list):
        fail("topology", f"{owner} containers must be a list")
    result = {}
    for container in containers:
        if not isinstance(container, dict) or not isinstance(container.get("name"), str):
            fail("topology", f"{owner} has a container without a name")
        name = container["name"]
        if name in result:
            fail("topology", f"{owner} has duplicate container {name}")
        result[name] = container
    return result


def script_for(container: dict, owner: str) -> str:
    if container.get("command") != ["/bin/sh", "-c"]:
        fail("topology", f"{owner} must use command ['/bin/sh', '-c']")
    args = container.get("args")
    if not isinstance(args, list) or len(args) != 1 or not isinstance(args[0], str):
        fail("topology", f"{owner} must have exactly one shell script argument")
    return args[0]


def named_port(container: dict, name: str, owner: str) -> int:
    matches = [
        port.get("containerPort")
        for port in container.get("ports", [])
        if isinstance(port, dict) and port.get("name") == name
    ]
    if len(matches) != 1 or isinstance(matches[0], bool) or not isinstance(matches[0], int):
        fail("topology", f"{owner} must expose one numeric {name} port")
    if not 1 <= matches[0] <= 65535:
        fail("topology", f"{owner} {name} port is outside 1..65535")
    return matches[0]


def process_namespace_is_private(spec: dict, owner: str):
    value = spec.get("shareProcessNamespace", False)
    if value is not False:
        fail("topology", f"{owner} must have shareProcessNamespace=false")


def workload_labels(metadata: dict) -> bool:
    labels = metadata.get("labels") or {}
    return (
        metadata.get("namespace") == "haptic"
        and labels.get("app.kubernetes.io/instance") == "haptic"
        and labels.get("app.kubernetes.io/component") == "loadbalancer"
    )


def extract_topology(workloads: dict, pods: dict) -> dict:
    deployments = [
        item
        for item in workloads.get("items", [])
        if item.get("kind") == "Deployment" and workload_labels(item.get("metadata") or {})
    ]
    if len(deployments) != 1:
        fail("topology", "expected one HAPTIC load-balancer Deployment")
    deployment = deployments[0]
    if (
        not isinstance(deployment["metadata"].get("name"), str)
        or not deployment["metadata"]["name"]
        or not isinstance(deployment["metadata"].get("uid"), str)
        or not deployment["metadata"]["uid"]
        or isinstance(deployment["metadata"].get("generation"), bool)
        or not isinstance(deployment["metadata"].get("generation"), int)
    ):
        fail("topology", "load-balancer Deployment identity is incomplete")
    template_spec = deployment.get("spec", {}).get("template", {}).get("spec", {})
    process_namespace_is_private(template_spec, "load-balancer Deployment template")
    template_containers = unique_by_name(
        template_spec.get("containers"), "load-balancer Deployment template"
    )

    supervised = {}
    for name, container in template_containers.items():
        args = container.get("args") or []
        script = args[0] if len(args) == 1 and isinstance(args[0], str) else ""
        has_supervisor_marker = any(marker in script for marker in SUPERVISOR_MARKERS)
        if has_supervisor_marker and name not in CHILDREN:
            fail("topology", f"unknown supervised container {name}")
        if name not in CHILDREN:
            continue
        if not all(marker in script for marker in SUPERVISOR_MARKERS):
            fail("topology", f"known child container {name} lacks the supervisor contract")
        script = script_for(container, f"Deployment container {name}")
        matches = CHILDREN[name].executable_pattern.findall(script)
        if len(matches) != 1:
            fail("topology", f"could not derive one child executable for {name}")
        supervised[name] = {
            "container": name,
            "expected_executable": matches[0],
            "command": container["command"],
            "args": container["args"],
        }
    if "dataplane" not in supervised:
        fail("topology", "load-balancer Deployment has no supervised dataplane container")

    for name, item in supervised.items():
        definition = CHILDREN[name]
        if definition.port_container not in template_containers:
            fail("topology", f"{name} health port container is absent")
        item["health"] = {
            "method": definition.method,
            "path": definition.path,
            "port": named_port(
                template_containers[definition.port_container],
                definition.port_name,
                f"Deployment container {definition.port_container}",
            ),
            "port_name": definition.port_name,
            "port_container": definition.port_container,
            "accepted_status_codes": list(definition.accepted_status_codes),
            "scope": definition.health_scope,
            "required_config_file": definition.required_config_file,
            "required_config_pattern": definition.required_config_pattern,
        }

    loadbalancer_pods = [
        item for item in pods.get("items", []) if workload_labels(item.get("metadata") or {})
    ]
    if not loadbalancer_pods:
        fail("topology", "no HAPTIC load-balancer pods were found")
    tasks = []
    seen_pods = set()
    for pod in loadbalancer_pods:
        metadata = pod.get("metadata") or {}
        pod_name = metadata.get("name")
        pod_uid = metadata.get("uid")
        node = pod.get("spec", {}).get("nodeName")
        if not all(isinstance(value, str) and value for value in (pod_name, pod_uid, node)):
            fail("topology", "load-balancer pod identity is incomplete")
        if pod_name in seen_pods:
            fail("topology", f"duplicate load-balancer pod {pod_name}")
        seen_pods.add(pod_name)
        process_namespace_is_private(pod.get("spec", {}), f"pod {pod_name}")
        pod_containers = unique_by_name(pod.get("spec", {}).get("containers"), f"pod {pod_name}")
        for name, container in pod_containers.items():
            args = container.get("args") or []
            script = args[0] if len(args) == 1 and isinstance(args[0], str) else ""
            if any(marker in script for marker in SUPERVISOR_MARKERS) and name not in CHILDREN:
                fail("topology", f"pod {pod_name} has unknown supervised container {name}")
            if name in CHILDREN and name not in supervised:
                fail("topology", f"pod {pod_name} has unexpected supervised container {name}")
        statuses = unique_by_name(
            pod.get("status", {}).get("containerStatuses"), f"pod {pod_name} statuses"
        )
        for name, expected in supervised.items():
            if name not in pod_containers or name not in statuses:
                fail("topology", f"pod {pod_name} lacks supervised container {name}")
            actual = pod_containers[name]
            if actual.get("command") != expected["command"] or actual.get("args") != expected["args"]:
                fail("topology", f"pod {pod_name} container {name} differs from the Deployment command")
            definition = CHILDREN[name]
            port_container = pod_containers.get(definition.port_container)
            if port_container is None:
                fail("topology", f"pod {pod_name} lacks health port container {definition.port_container}")
            if named_port(port_container, definition.port_name, f"pod {pod_name}") != expected["health"]["port"]:
                fail("topology", f"pod {pod_name} {name} health port differs from the Deployment")
            status = statuses[name]
            task = {
                "key": f"haptic/{pod_name}/{name}",
                "namespace": "haptic",
                "pod": pod_name,
                "pod_uid": pod_uid,
                "node": node,
                "container": name,
                "container_id": status.get("containerID"),
                "image": status.get("image"),
                "image_id": status.get("imageID"),
                "restart_count": status.get("restartCount"),
                "ready": status.get("ready"),
                "expected_executable": expected["expected_executable"],
                "health": expected["health"],
            }
            if (
                not isinstance(task["container_id"], str)
                or not task["container_id"]
                or not isinstance(task["image"], str)
                or not task["image"]
                or not isinstance(task["image_id"], str)
                or not task["image_id"]
                or isinstance(task["restart_count"], bool)
                or not isinstance(task["restart_count"], int)
                or task["restart_count"] != 0
                or task["ready"] is not True
            ):
                fail("topology", f"pod {pod_name} container {name} runtime identity is incomplete")
            tasks.append(task)

    tasks.sort(key=lambda item: item["key"])
    return {
        "schema_version": SCHEMA_VERSION,
        "deployment": {
            "name": deployment["metadata"].get("name"),
            "uid": deployment["metadata"].get("uid"),
            "generation": deployment["metadata"].get("generation"),
            "share_process_namespace": False,
        },
        "supervised_container_names": sorted(supervised),
        "tasks": tasks,
    }


def identity_shape(identity: dict, expected_executable: str) -> bool:
    if not isinstance(identity, dict):
        return False
    pids = identity.get("matching_pids")
    return (
        identity.get("process_count") == 1
        and isinstance(pids, list)
        and len(pids) == 1
        and identity.get("pid") == pids[0]
        and isinstance(identity.get("pid"), str)
        and DECIMAL.fullmatch(identity["pid"]) is not None
        and identity.get("argv0") == expected_executable
        and identity.get("executable_inode_matches") is True
        and isinstance(identity.get("state"), str)
        and len(identity["state"]) == 1
        and identity["state"] != "Z"
        and identity.get("ppid") == "1"
        and isinstance(identity.get("starttime"), str)
        and DECIMAL.fullmatch(identity["starttime"]) is not None
    )


def stable_identity_capture(child: dict, expected_executable: str) -> bool:
    before = child.get("identity_before_health")
    after = child.get("identity_after_health")
    fields = (
        "process_count",
        "matching_pids",
        "pid",
        "argv0",
        "executable_inode_matches",
        "state",
        "ppid",
        "starttime",
    )
    return (
        identity_shape(before, expected_executable)
        and identity_shape(after, expected_executable)
        and all(before.get(field) == after.get(field) for field in fields)
    )


def capture_children(document: dict) -> dict[str, dict]:
    if document.get("schema_version") != SCHEMA_VERSION or document.get("evidence_valid") is not True:
        fail("capture", "supervised-child capture is not structurally valid")
    children = document.get("children")
    if not isinstance(children, list) or not children:
        fail("capture", "supervised-child capture has no children")
    keyed = {}
    for child in children:
        key = child.get("key") if isinstance(child, dict) else None
        if not isinstance(key, str) or not key or key in keyed:
            fail("capture", "supervised-child capture keys are missing or duplicated")
        if not isinstance(child.get("boot_id"), str) or BOOT_ID.fullmatch(child["boot_id"]) is None:
            fail("capture", f"supervised child {key} has an invalid boot_id")
        if not isinstance(child.get("capture_stable"), bool):
            fail("capture", f"supervised child {key} lacks capture_stable")
        health = child.get("health")
        if not isinstance(health, dict) or not isinstance(health.get("pass"), bool):
            fail("capture", f"supervised child {key} lacks a health verdict")
        keyed[key] = child
    topology = document.get("topology")
    if not isinstance(topology, dict) or topology.get("schema_version") != SCHEMA_VERSION:
        fail("capture", "supervised-child capture lacks its validated topology")
    tasks = topology.get("tasks")
    if not isinstance(tasks, list):
        fail("capture", "supervised-child topology lacks tasks")
    topology_by_key = {
        task.get("key"): task
        for task in tasks
        if isinstance(task, dict) and isinstance(task.get("key"), str)
    }
    if len(topology_by_key) != len(tasks) or topology_by_key.keys() != keyed.keys():
        fail("capture", "supervised-child capture does not match its topology tasks")
    bound_fields = (
        "namespace",
        "pod",
        "pod_uid",
        "node",
        "container",
        "container_id",
        "image",
        "image_id",
        "restart_count",
        "ready",
        "expected_executable",
    )
    health_fields = (
        "method",
        "path",
        "port",
        "port_name",
        "port_container",
        "accepted_status_codes",
        "scope",
        "required_config_file",
        "required_config_pattern",
    )
    for key, task in topology_by_key.items():
        child = keyed[key]
        if any(child.get(field) != task.get(field) for field in bound_fields):
            fail("capture", f"supervised child {key} differs from its topology identity")
        if any(child["health"].get(field) != task["health"].get(field) for field in health_fields):
            fail("capture", f"supervised child {key} differs from its topology health probe")
        health = child["health"]
        status = health.get("http_status_code")
        status_line = health.get("http_status_line")
        probe_exit = health.get("probe_exit_code")
        config_verified = health.get("required_config_verified")
        valid_response = (
            probe_exit == 0
            and isinstance(status, str)
            and re.fullmatch(r"[1-5][0-9]{2}", status) is not None
            and isinstance(status_line, str)
            and re.fullmatch(rf"HTTP/[0-9]+\.[0-9]+ {status}(?: [ -~]*)?", status_line)
            is not None
        )
        accepted = task["health"]["accepted_status_codes"]
        expected_health = (
            config_verified is True
            and valid_response
            and ("any-http" in accepted or status in accepted)
        )
        if (
            isinstance(probe_exit, bool)
            or not isinstance(probe_exit, int)
            or not isinstance(config_verified, bool)
            or health["pass"] is not expected_health
        ):
            fail("capture", f"supervised child {key} has an inconsistent health verdict")
    return keyed


def baseline_result(document: dict) -> dict:
    children = capture_children(document)
    failures = []
    for key, child in sorted(children.items()):
        expected = child.get("expected_executable")
        valid = (
            isinstance(expected, str)
            and expected.startswith("/")
            and child["capture_stable"]
            and stable_identity_capture(child, expected)
            and child["health"]["pass"]
        )
        if not valid:
            failures.append(key)
    if failures:
        fail("baseline", "supervised-child baseline is missing, ambiguous, unstable, or unhealthy: " + ", ".join(failures))
    return {
        "schema_version": SCHEMA_VERSION,
        "evidence_valid": True,
        "pass": True,
        "child_count": len(children),
        "requirement": "one stable healthy child with exact executable, PPID 1, boot ID, PID, and proc stat starttime",
    }


def continuity_result(before: dict, after: dict) -> dict:
    baseline_result(before)
    before_children = capture_children(before)
    after_children = capture_children(after)
    if before.get("topology") != after.get("topology"):
        fail("topology", "supervised-child topology changed across the scenario")
    if before_children.keys() != after_children.keys():
        fail("capture", "supervised-child inventory changed across the scenario")

    results = []
    for key in sorted(before_children):
        old = before_children[key]
        new = after_children[key]
        expected = old["expected_executable"]
        final_identity = new.get("identity_after_health")
        final_identity_valid = identity_shape(final_identity, expected)
        capture_stable = (
            new["capture_stable"]
            and stable_identity_capture(new, expected)
            and final_identity_valid
        )
        identity_unchanged = False
        if final_identity_valid:
            old_identity = old["identity_after_health"]
            identity_unchanged = (
                old["boot_id"],
                old_identity["pid"],
                old_identity["starttime"],
                old_identity["argv0"],
            ) == (
                new["boot_id"],
                final_identity["pid"],
                final_identity["starttime"],
                final_identity["argv0"],
            )
        reasons = []
        if not capture_stable:
            reasons.append("final process is missing, ambiguous, invalid, or changed during capture")
        if capture_stable and not identity_unchanged:
            reasons.append("child boot ID, PID, starttime, or argv0 changed")
        if not new["health"]["pass"]:
            reasons.append("final health check failed")
        results.append(
            {
                "key": key,
                "container": new.get("container"),
                "expected_executable": expected,
                "baseline": {
                    "boot_id": old["boot_id"],
                    "identity": old["identity_after_health"],
                    "health": old["health"],
                },
                "final": {
                    "boot_id": new["boot_id"],
                    "identity_before_health": new["identity_before_health"],
                    "identity_after_health": final_identity,
                    "capture_stable": new["capture_stable"],
                    "health": new["health"],
                },
                "identity_unchanged": identity_unchanged,
                "final_healthy": new["health"]["pass"],
                "pass": not reasons,
                "negative_reasons": reasons,
            }
        )
    negative = [item["key"] for item in results if not item["pass"]]
    return {
        "schema_version": SCHEMA_VERSION,
        "evidence_valid": True,
        "pass": not negative,
        "child_count": len(results),
        "negative_children": negative,
        "children": results,
        "classification": "identity or health changes are measured product outcomes, not harness failures",
    }


def parse_args():
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    topology = subparsers.add_parser("topology")
    topology.add_argument("--workloads", required=True)
    topology.add_argument("--pods", required=True)
    topology.add_argument("--output", required=True)

    baseline = subparsers.add_parser("baseline")
    baseline.add_argument("--input", required=True)
    baseline.add_argument("--output", required=True)

    continuity = subparsers.add_parser("continuity")
    continuity.add_argument("--before", required=True)
    continuity.add_argument("--after", required=True)
    continuity.add_argument("--output", required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        if args.command == "topology":
            result = extract_topology(load_document(args.workloads), load_document(args.pods))
        elif args.command == "baseline":
            result = baseline_result(load_document(args.input))
        else:
            result = continuity_result(load_document(args.before), load_document(args.after))
    except AnalysisFailure as error:
        write_document(args.output, error.result())
        print(f"supervised-child analysis: {error.message}", file=sys.stderr)
        return 1
    write_document(args.output, result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
