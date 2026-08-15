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
"""Classify supervised child failures in exact gateway-api benchmark log sources."""

import argparse
import calendar
import json
import re
import sys
from datetime import datetime
from pathlib import Path


SCHEMA_VERSION = 1
RFC3339 = re.compile(
    r"^(?P<year>[0-9]{4})-(?P<month>[0-9]{2})-(?P<day>[0-9]{2})T"
    r"(?P<hour>[0-9]{2}):(?P<minute>[0-9]{2}):(?P<second>[0-9]{2})"
    r"(?:\.(?P<fraction>[0-9]{1,9}))?Z$"
)
LOG_LINE = re.compile(
    r"^\[pod/(?P<pod>[^/\]\s]+)/(?P<container>[^\]\s]+)\] "
    r"(?P<timestamp>[0-9]{4}-[0-9]{2}-[0-9]{2}T"
    r"[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z)"
    r"(?: (?P<message>.*))?$"
)
BACKOFF_SECONDS = (1, 2, 4, 8, 16, 30)
CHILD_WARNINGS = {
    "dataplane": ("Dataplane API", "config updates are unavailable"),
    "spoa-hub": ("SPOA hub", "SPOA processing is unavailable"),
    "vector": ("Vector", "access-log and merged-metric export are unavailable"),
}
WARNING_PATTERNS = {
    child: re.compile(
        rf"^WARNING: {re.escape(display_name)} exited with status "
        rf"(?P<status>0|[1-9][0-9]{{0,2}}); {re.escape(impact)}; "
        rf"restarting in (?P<backoff>{'|'.join(str(value) for value in BACKOFF_SECONDS)})s$"
    )
    for child, (display_name, impact) in CHILD_WARNINGS.items()
}
HEALTH_WARNING_PATTERNS = {
    child: re.compile(
        rf"^WARNING: {re.escape(display_name)} failed 3 consecutive health checks; "
        rf"{re.escape(impact)}; restarting child$"
    )
    for child, (display_name, impact) in CHILD_WARNINGS.items()
}


class AnalysisFailure(Exception):
    def __init__(self, code: str, message: str, **details):
        super().__init__(message)
        self.code = code
        self.message = message
        self.details = details

    def result(self) -> dict:
        failure = {"code": self.code, "message": self.message}
        failure.update(self.details)
        return {
            "schema_version": SCHEMA_VERSION,
            "evidence_valid": False,
            "pass": False,
            "failure": failure,
        }


def fail(code: str, message: str, **details):
    raise AnalysisFailure(code, message, **details)


def load_document(path: Path) -> dict:
    try:
        with path.open(encoding="utf-8") as handle:
            document = json.load(handle)
    except (OSError, json.JSONDecodeError) as error:
        fail("input", f"could not read {path}: {error}")
    if not isinstance(document, dict):
        fail("input", "manifest must contain a JSON object")
    return document


def write_document(path: Path, document: dict):
    temporary = path.with_name(f"{path.name}.tmp")
    with temporary.open("w", encoding="utf-8") as handle:
        json.dump(document, handle, indent=2, sort_keys=True)
        handle.write("\n")
    temporary.replace(path)


def require_string(document: dict, field: str, owner: str) -> str:
    value = document.get(field)
    if not isinstance(value, str) or not value or "\x00" in value:
        fail("manifest_source", f"{owner} {field} must be a non-empty string", field=field)
    return value


def timestamp_nanoseconds(value: str, field: str) -> int:
    match = RFC3339.fullmatch(value) if isinstance(value, str) else None
    if match is None:
        fail("timestamp", f"{field} must be an RFC3339 UTC timestamp", field=field)
    parts = {name: int(match.group(name)) for name in (
        "year",
        "month",
        "day",
        "hour",
        "minute",
        "second",
    )}
    try:
        parsed = datetime(**parts)
    except ValueError as error:
        fail("timestamp", f"{field} is invalid: {error}", field=field)
    fraction = (match.group("fraction") or "").ljust(9, "0")
    return calendar.timegm(parsed.timetuple()) * 1_000_000_000 + int(fraction or "0")


def normalize_window(document: dict) -> tuple[dict, int, int]:
    window = document.get("window")
    if not isinstance(window, dict):
        fail("manifest_window", "manifest window must be an object")
    start = require_string(window, "start", "window")
    end = require_string(window, "end", "window")
    start_ns = timestamp_nanoseconds(start, "window.start")
    end_ns = timestamp_nanoseconds(end, "window.end")
    if start_ns >= end_ns:
        fail("manifest_window", "manifest window start must precede end")
    return {"start": start, "end": end, "bounds": "inclusive"}, start_ns, end_ns


def resolve_input_file(reference: str, manifest_path: Path, code: str) -> Path:
    path = Path(reference)
    if not path.is_absolute():
        path = manifest_path.parent / path
    if path.is_symlink() or not path.is_file():
        fail(code, f"input file is missing: {reference}", path=reference)
    return path.resolve()


def load_topology_tasks(reference: str, manifest_path: Path, snapshot: str) -> dict:
    path = resolve_input_file(reference, manifest_path, "missing_topology")
    document = load_document(path)
    if (
        isinstance(document.get("schema_version"), bool)
        or document.get("schema_version") != SCHEMA_VERSION
        or document.get("evidence_valid") is not True
    ):
        fail("topology_evidence", f"{snapshot} supervised-child evidence is invalid")
    topology = document.get("topology")
    if (
        not isinstance(topology, dict)
        or isinstance(topology.get("schema_version"), bool)
        or topology.get("schema_version") != SCHEMA_VERSION
    ):
        fail("topology_evidence", f"{snapshot} supervised-child topology is invalid")
    tasks = topology.get("tasks")
    if not isinstance(tasks, list) or not tasks:
        fail("topology_evidence", f"{snapshot} supervised-child topology has no tasks")

    normalized = {}
    for index, task in enumerate(tasks):
        owner = f"{snapshot} topology task {index}"
        if not isinstance(task, dict):
            fail("topology_evidence", f"{owner} must be an object")
        fields = {}
        for field in ("namespace", "pod", "pod_uid", "container", "container_id"):
            value = task.get(field)
            if not isinstance(value, str) or not value or "\x00" in value:
                fail("topology_evidence", f"{owner} {field} must be a non-empty string")
            fields[field] = value
        child = fields["container"]
        if child not in CHILD_WARNINGS:
            fail("topology_child", f"{owner} has unsupported supervised child {child}")
        expected_key = f"{fields['namespace']}/{fields['pod']}/{child}"
        if task.get("key") != expected_key:
            fail("topology_evidence", f"{owner} key does not match its identity")
        logical_key = (fields["namespace"], fields["pod"], child)
        if logical_key in normalized:
            fail("topology_evidence", f"{snapshot} topology has duplicate task {expected_key}")
        normalized[logical_key] = {
            **fields,
            "child": child,
        }

    container_names = topology.get("supervised_container_names")
    expected_names = sorted({task["child"] for task in normalized.values()})
    if (
        not isinstance(container_names, list)
        or any(not isinstance(name, str) for name in container_names)
        or sorted(container_names) != expected_names
        or len(container_names) != len(set(container_names))
    ):
        fail(
            "topology_evidence",
            f"{snapshot} supervised container names do not match its tasks",
        )
    return normalized


def normalize_authoritative_tasks(document: dict, manifest_path: Path) -> tuple[dict, dict]:
    supervised_children = document.get("supervised_children")
    if not isinstance(supervised_children, dict):
        fail("manifest_topology", "manifest supervised_children must be an object")
    references = {}
    resolved_paths = {}
    task_sets = {}
    for snapshot in ("before", "after"):
        reference = supervised_children.get(snapshot)
        if not isinstance(reference, str) or not reference or "\x00" in reference:
            fail("manifest_topology", f"manifest supervised_children.{snapshot} is missing")
        references[snapshot] = reference
        resolved_paths[snapshot] = resolve_input_file(
            reference,
            manifest_path,
            "missing_topology",
        )
        task_sets[snapshot] = load_topology_tasks(reference, manifest_path, snapshot)
    if resolved_paths["before"] == resolved_paths["after"]:
        fail("manifest_topology", "before and after supervised-child captures must differ")
    if task_sets["before"] != task_sets["after"]:
        fail("topology_changed", "supervised-child topology changed across the capture window")
    return references, task_sets["before"]


def normalize_sources(
    document: dict,
    manifest_path: Path,
    authoritative_tasks: dict,
    window: dict,
    end_ns: int,
) -> list[dict]:
    sources = document.get("sources")
    if not isinstance(sources, list) or not sources:
        fail("manifest_sources", "manifest must contain at least one source")

    normalized = []
    logical_sources = set()
    identity_sources = set()
    container_ids = set()
    log_paths = set()
    pod_names = {}
    pod_uids = {}
    for index, item in enumerate(sources):
        owner = f"source {index}"
        if not isinstance(item, dict):
            fail("manifest_source", f"{owner} must be an object", source_index=index)
        source = {
            field: require_string(item, field, owner)
            for field in (
                "namespace",
                "pod",
                "pod_uid",
                "container",
                "container_id",
                "child",
                "log_file",
            )
        }
        child = source["child"]
        if child not in CHILD_WARNINGS:
            fail("manifest_source", f"{owner} has unknown child {child}", source_index=index)
        if source["container"] != child:
            fail(
                "manifest_source",
                f"{owner} container does not match child {child}",
                source_index=index,
            )

        capture = item.get("capture")
        if not isinstance(capture, dict):
            fail("capture_incomplete", f"{owner} capture metadata must be an object")
        rc = capture.get("rc")
        if isinstance(rc, bool) or not isinstance(rc, int) or rc != 0:
            fail("capture_incomplete", f"{owner} capture rc must be exactly 0", rc=rc)
        started_at = capture.get("started_at")
        if not isinstance(started_at, str) or not started_at or "\x00" in started_at:
            fail("capture_incomplete", f"{owner} started_at must be an RFC3339 timestamp")
        started_at_ns = timestamp_nanoseconds(started_at, f"{owner}.capture.started_at")
        if started_at_ns < end_ns:
            fail("capture_incomplete", f"{owner} started before the manifest window ended")
        captured_at = capture.get("captured_at")
        if not isinstance(captured_at, str) or not captured_at or "\x00" in captured_at:
            fail("capture_incomplete", f"{owner} captured_at must be an RFC3339 timestamp")
        captured_at_ns = timestamp_nanoseconds(captured_at, f"{owner}.capture.captured_at")
        if captured_at_ns < started_at_ns:
            fail("capture_incomplete", f"{owner} completed before its capture started")
        if capture.get("since_time") != window["start"]:
            fail("capture_incomplete", f"{owner} since_time must equal the manifest window start")
        tail = capture.get("tail")
        if isinstance(tail, bool) or not isinstance(tail, int) or tail != -1:
            fail("capture_incomplete", f"{owner} tail must be exactly -1", tail=tail)
        if capture.get("timestamps") is not True:
            fail("capture_incomplete", f"{owner} timestamps must be true")
        if capture.get("prefix") is not True:
            fail("capture_incomplete", f"{owner} prefix must be true")
        source["capture"] = {
            "rc": 0,
            "started_at": started_at,
            "captured_at": captured_at,
            "since_time": window["start"],
            "tail": -1,
            "timestamps": True,
            "prefix": True,
        }
        source["_captured_at_ns"] = captured_at_ns

        pod_name_key = (source["namespace"], source["pod"])
        known_uid = pod_names.setdefault(pod_name_key, source["pod_uid"])
        if known_uid != source["pod_uid"]:
            fail("manifest_source", f"{owner} pod name maps to multiple UIDs", source_index=index)
        pod_uid_key = (source["namespace"], source["pod_uid"])
        known_name = pod_uids.setdefault(pod_uid_key, source["pod"])
        if known_name != source["pod"]:
            fail("manifest_source", f"{owner} pod UID maps to multiple names", source_index=index)

        logical_key = (source["namespace"], source["pod"], source["container"])
        identity_key = (source["pod_uid"], source["container_id"], child)
        if logical_key in logical_sources or identity_key in identity_sources:
            fail("duplicate_source", f"duplicate source {source['namespace']}/{source['pod']}/{child}")
        if source["container_id"] in container_ids:
            fail("duplicate_source", f"duplicate container ID {source['container_id']}")

        log_path = Path(source["log_file"])
        if not log_path.is_absolute():
            log_path = manifest_path.parent / log_path
        resolved_log_path = log_path.resolve()
        if resolved_log_path in log_paths:
            fail("duplicate_source", f"duplicate log file {source['log_file']}")
        if log_path.is_symlink() or not log_path.is_file():
            fail(
                "missing_source",
                f"source log file is missing: {source['log_file']}",
                source=f"{source['namespace']}/{source['pod']}/{child}",
            )

        logical_sources.add(logical_key)
        identity_sources.add(identity_key)
        container_ids.add(source["container_id"])
        log_paths.add(resolved_log_path)
        source["_path"] = resolved_log_path
        normalized.append(source)

    normalized = sorted(normalized, key=lambda item: (
        item["namespace"],
        item["pod"],
        item["container"],
    ))
    observed = {
        (source["namespace"], source["pod"], source["container"]): source
        for source in normalized
    }
    missing = sorted(set(authoritative_tasks) - set(observed))
    unexpected = sorted(set(observed) - set(authoritative_tasks))
    if missing or unexpected:
        fail(
            "source_inventory",
            "manifest sources do not match the authoritative supervised-child tasks",
            missing=["/".join(key) for key in missing],
            unexpected=["/".join(key) for key in unexpected],
        )
    for key, expected in authoritative_tasks.items():
        actual = observed[key]
        identity_fields = ("namespace", "pod", "pod_uid", "container", "container_id", "child")
        if any(actual[field] != expected[field] for field in identity_fields):
            fail(
                "source_identity_mismatch",
                f"manifest source {'/'.join(key)} differs from authoritative topology",
                expected={field: expected[field] for field in identity_fields},
                observed={field: actual[field] for field in identity_fields},
            )
    return normalized


def exit_warning(message: str, source: dict, timestamp: str, line_number: int) -> dict | None:
    for child, pattern in WARNING_PATTERNS.items():
        match = pattern.fullmatch(message)
        if match is None:
            continue
        if child != source["child"]:
            fail(
                "warning_source_mismatch",
                f"{source['log_file']}:{line_number} contains a warning for {child}",
                source_child=source["child"],
                warning_child=child,
            )
        status = int(match.group("status"))
        if status > 255:
            fail(
                "malformed_supervisor_warning",
                f"{source['log_file']}:{line_number} has an invalid exit status",
            )
        signal = status - 128 if 129 <= status <= 192 else None
        return {
            "timestamp": timestamp,
            "exit_timestamp": timestamp,
            "namespace": source["namespace"],
            "pod": source["pod"],
            "pod_uid": source["pod_uid"],
            "container": source["container"],
            "container_id": source["container_id"],
            "child": child,
            "status": status,
            "signal": signal,
            "restart_backoff_seconds": int(match.group("backoff")),
            "log_file": source["log_file"],
            "line": line_number,
            "exit_observed": True,
            "exit_line": line_number,
            "restart_reason": "child-exit",
            "health_check_failure": None,
        }

    known_child_warning = any(
        message.startswith(f"WARNING: {display_name} exited")
        for display_name, _ in CHILD_WARNINGS.values()
    )
    generic_supervisor_warning = (
        message.startswith("WARNING: ")
        and " exited with status " in message
        and "restarting in " in message
    )
    if known_child_warning or generic_supervisor_warning:
        fail(
            "malformed_supervisor_warning",
            f"{source['log_file']}:{line_number} contains a malformed supervisor warning",
        )
    return None


def health_warning(message: str, source: dict, timestamp: str, line_number: int) -> dict | None:
    for child, pattern in HEALTH_WARNING_PATTERNS.items():
        if pattern.fullmatch(message) is None:
            continue
        if child != source["child"]:
            fail(
                "warning_source_mismatch",
                f"{source['log_file']}:{line_number} contains a warning for {child}",
                source_child=source["child"],
                warning_child=child,
            )
        return {
            "timestamp": timestamp,
            "line": line_number,
            "consecutive_failures": 3,
        }

    known_child_warning = any(
        message.startswith(f"WARNING: {display_name} failed")
        for display_name, _ in CHILD_WARNINGS.values()
    )
    generic_health_warning = (
        message.startswith("WARNING: ")
        and " consecutive health checks; " in message
        and message.endswith("; restarting child")
    )
    if known_child_warning or generic_health_warning:
        fail(
            "malformed_supervisor_warning",
            f"{source['log_file']}:{line_number} contains a malformed supervisor warning",
        )
    return None


def health_restart_event(warning: dict, source: dict) -> dict:
    return {
        "timestamp": warning["timestamp"],
        "exit_timestamp": None,
        "namespace": source["namespace"],
        "pod": source["pod"],
        "pod_uid": source["pod_uid"],
        "container": source["container"],
        "container_id": source["container_id"],
        "child": source["child"],
        "status": None,
        "signal": None,
        "restart_backoff_seconds": None,
        "log_file": source["log_file"],
        "line": warning["line"],
        "exit_observed": False,
        "exit_line": None,
        "restart_reason": "health-check-failure",
        "health_check_failure": warning,
    }


def associate_exit_warning(health_event: dict, exit_event: dict):
    health_event.update(
        {
            "exit_timestamp": exit_event["exit_timestamp"],
            "status": exit_event["status"],
            "signal": exit_event["signal"],
            "restart_backoff_seconds": exit_event["restart_backoff_seconds"],
            "exit_observed": True,
            "exit_line": exit_event["line"],
        }
    )


def analyze_source(source: dict, start_ns: int, end_ns: int) -> tuple[list[dict], int]:
    try:
        lines = source["_path"].read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError) as error:
        fail("source_read", f"could not read {source['log_file']}: {error}")

    events = []
    pending_health_event = None
    post_end_line_count = 0
    last_timestamp_ns = None
    for line_number, line in enumerate(lines, start=1):
        match = LOG_LINE.fullmatch(line)
        if match is None:
            fail(
                "malformed_log_line",
                f"{source['log_file']}:{line_number} lacks an exact pod/container timestamp prefix",
            )
        if match.group("pod") != source["pod"] or match.group("container") != source["container"]:
            fail(
                "log_source_mismatch",
                f"{source['log_file']}:{line_number} does not match its manifest source",
                expected_pod=source["pod"],
                expected_container=source["container"],
                observed_pod=match.group("pod"),
                observed_container=match.group("container"),
            )
        timestamp = match.group("timestamp")
        timestamp_ns = timestamp_nanoseconds(timestamp, f"{source['log_file']}:{line_number}")
        if last_timestamp_ns is not None and timestamp_ns < last_timestamp_ns:
            fail(
                "log_timestamp_order",
                f"{source['log_file']}:{line_number} precedes an earlier log line",
                timestamp=timestamp,
            )
        last_timestamp_ns = timestamp_ns
        if timestamp_ns > source["_captured_at_ns"]:
            fail(
                "capture_timestamp",
                f"{source['log_file']}:{line_number} follows its capture completion timestamp",
                timestamp=timestamp,
                captured_at=source["capture"]["captured_at"],
            )
        if timestamp_ns < start_ns:
            fail(
                "log_timestamp_window",
                f"{source['log_file']}:{line_number} precedes the manifest window",
                timestamp=timestamp,
            )

        message = match.group("message") or ""
        if timestamp_ns > end_ns:
            post_end_line_count += 1
            if pending_health_event is None:
                continue
            repeated_health_warning = health_warning(message, source, timestamp, line_number)
            if repeated_health_warning is not None:
                fail(
                    "unpaired_health_restart",
                    f"{source['log_file']}:{line_number} follows an unpaired health warning",
                )
            event = exit_warning(message, source, timestamp, line_number)
            if event is None:
                continue
            associate_exit_warning(pending_health_event, event)
            pending_health_event = None
            continue

        pending = health_warning(message, source, timestamp, line_number)
        if pending is not None:
            if pending_health_event is not None:
                fail(
                    "unpaired_health_restart",
                    f"{source['log_file']}:{line_number} follows an unpaired health warning",
                )
            pending_health_event = health_restart_event(pending, source)
            pending_health_event["_timestamp_ns"] = timestamp_ns
            events.append(pending_health_event)
            continue

        event = exit_warning(message, source, timestamp, line_number)
        if event is not None:
            if pending_health_event is not None:
                associate_exit_warning(pending_health_event, event)
                pending_health_event = None
                continue
            event["_timestamp_ns"] = timestamp_ns
            events.append(event)
    return events, post_end_line_count


def analyze_manifest(document: dict, manifest_path: Path) -> dict:
    if (
        isinstance(document.get("schema_version"), bool)
        or document.get("schema_version") != SCHEMA_VERSION
    ):
        fail("schema_version", f"manifest schema_version must be {SCHEMA_VERSION}")
    window, start_ns, end_ns = normalize_window(document)
    topology_references, authoritative_tasks = normalize_authoritative_tasks(
        document,
        manifest_path,
    )
    sources = normalize_sources(
        document,
        manifest_path,
        authoritative_tasks,
        window,
        end_ns,
    )
    events = []
    post_end_sources = []
    for source in sources:
        source_events, post_end_line_count = analyze_source(source, start_ns, end_ns)
        events.extend(source_events)
        if post_end_line_count:
            post_end_sources.append(
                {
                    key: source[key]
                    for key in (
                        "namespace",
                        "pod",
                        "pod_uid",
                        "container",
                        "container_id",
                        "child",
                        "log_file",
                    )
                }
                | {"line_count": post_end_line_count}
            )
    events.sort(key=lambda item: (
        item["_timestamp_ns"],
        item["namespace"],
        item["pod"],
        item["container"],
        item["line"],
    ))
    for event in events:
        del event["_timestamp_ns"]
    output_sources = [
        {
            key: value
            for key, value in source.items()
            if key not in ("_path", "_captured_at_ns")
        }
        for source in sources
    ]
    return {
        "schema_version": SCHEMA_VERSION,
        "evidence_valid": True,
        "pass": not events,
        "window": window,
        "supervised_children": {
            **topology_references,
            "task_count": len(authoritative_tasks),
        },
        "source_count": len(output_sources),
        "sources": output_sources,
        "event_count": len(events),
        "events": events,
        "post_end_lines": {
            "count": sum(item["line_count"] for item in post_end_sources),
            "sources": post_end_sources,
        },
        "classification": (
            "supervised child failures are run-level product outcomes, not evidence failures"
        ),
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    manifest_path = Path(args.manifest)
    try:
        result = analyze_manifest(load_document(manifest_path), manifest_path)
    except AnalysisFailure as error:
        result = error.result()
        write_document(Path(args.output), result)
        print(f"supervisor-log analysis: {error.message}", file=sys.stderr)
        return 1
    write_document(Path(args.output), result)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
