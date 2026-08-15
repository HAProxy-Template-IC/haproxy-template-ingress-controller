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
"""Validate and summarize gateway-api-bench resource samples."""

import argparse
import json
import sys
from decimal import Decimal, InvalidOperation
from pathlib import Path


SCHEMA_VERSION = 1
COMPONENTS = ("controller", "loadbalancer")
EVALUATION_STEP_SECONDS = Decimal(5)
MAX_SOURCE_AGE_SECONDS = Decimal(20)
METRIC_NAMES = {
    "container_cpu": "container_cpu_usage_seconds_total",
    "container_working_set": "container_memory_working_set_bytes",
    "container_rss": "container_memory_rss",
    "pod_cgroup_cpu": "container_cpu_usage_seconds_total",
    "pod_cgroup_working_set": "container_memory_working_set_bytes",
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
            "pass": False,
            "failures": [failure],
        }


class InsufficientSamples(Exception):
    def __init__(
        self,
        sample_count: int,
        timestamps: list[Decimal],
        window_start: Decimal,
        window_end: Decimal,
        source_counts: dict[str, int] | None = None,
    ):
        super().__init__("resource analysis requires at least two fresh source samples per series")
        self.sample_count = sample_count
        self.timestamps = timestamps
        self.window_start = window_start
        self.window_end = window_end
        self.source_counts = source_counts or {}

    def failure_result(self) -> dict:
        return AnalysisFailure(
            "sample_window",
            str(self),
            sample_count=self.sample_count,
            source_sample_counts=self.source_counts,
            window=window_description(
                self.timestamps,
                self.window_start,
                self.window_end,
            ),
        ).result()


def identity_name(key: tuple[str, str, str]) -> str:
    return "/".join(key)


def pod_identity_name(key: tuple[str, str]) -> str:
    return "/".join(key)


def require_nonempty_string(value, field: str, identity: str | None = None) -> str:
    if not isinstance(value, str) or not value:
        details = {"field": field}
        if identity is not None:
            details["identity"] = identity
        raise AnalysisFailure("identity_field", f"{field} must be a non-empty string", **details)
    return value


def normalize_identities(document, snapshot: str) -> dict[tuple[str, str, str], dict]:
    if not isinstance(document, list) or not document:
        raise AnalysisFailure(
            "identity_document",
            f"{snapshot} identities must be a non-empty array",
            snapshot=snapshot,
        )

    identities = {}
    pod_keys = set()
    for pod in document:
        if not isinstance(pod, dict):
            raise AnalysisFailure(
                "identity_document",
                f"{snapshot} identity entry must be an object",
                snapshot=snapshot,
            )
        namespace = require_nonempty_string(pod.get("namespace"), "namespace")
        pod_name = require_nonempty_string(pod.get("name"), "name")
        pod_uid = require_nonempty_string(pod.get("uid"), "uid")
        component = require_nonempty_string(pod.get("component"), "component")
        if component not in COMPONENTS:
            raise AnalysisFailure(
                "identity_component",
                f"unsupported component {component}",
                component=component,
                pod=f"{namespace}/{pod_name}",
                snapshot=snapshot,
            )
        pod_key = (namespace, pod_name)
        if pod_key in pod_keys:
            raise AnalysisFailure(
                "duplicate_pod_identity",
                f"duplicate pod identity {namespace}/{pod_name}",
                pod=f"{namespace}/{pod_name}",
                snapshot=snapshot,
            )
        pod_keys.add(pod_key)

        containers = pod.get("containers")
        if not isinstance(containers, list) or not containers:
            raise AnalysisFailure(
                "identity_containers",
                f"{namespace}/{pod_name} must have at least one container",
                pod=f"{namespace}/{pod_name}",
                snapshot=snapshot,
            )
        for container in containers:
            if not isinstance(container, dict):
                raise AnalysisFailure(
                    "identity_containers",
                    f"{namespace}/{pod_name} container identity must be an object",
                    pod=f"{namespace}/{pod_name}",
                    snapshot=snapshot,
                )
            container_name = require_nonempty_string(container.get("name"), "container")
            key = (namespace, pod_name, container_name)
            display_name = identity_name(key)
            if key in identities:
                raise AnalysisFailure(
                    "duplicate_container_identity",
                    f"duplicate container identity {display_name}",
                    identity=display_name,
                    snapshot=snapshot,
                )
            restart_count = container.get("restartCount")
            if isinstance(restart_count, bool) or not isinstance(restart_count, int):
                raise AnalysisFailure(
                    "identity_field",
                    f"restartCount must be an integer for {display_name}",
                    field="restartCount",
                    identity=display_name,
                    snapshot=snapshot,
                )
            if restart_count != 0:
                raise AnalysisFailure(
                    "container_restarted",
                    f"container {display_name} has restarted",
                    identity=display_name,
                    restart_count=restart_count,
                    snapshot=snapshot,
                )
            if container.get("ready") is not True:
                raise AnalysisFailure(
                    "container_not_ready",
                    f"container {display_name} is not ready",
                    identity=display_name,
                    snapshot=snapshot,
                )
            identities[key] = {
                "namespace": namespace,
                "pod": pod_name,
                "pod_uid": pod_uid,
                "component": component,
                "container": container_name,
                "image": require_nonempty_string(container.get("image"), "image", display_name),
                "image_id": require_nonempty_string(
                    container.get("imageID"), "imageID", display_name
                ),
                "container_id": require_nonempty_string(
                    container.get("containerID"), "containerID", display_name
                ),
                "restart_count": restart_count,
                "ready": True,
            }

    observed_components = {identity["component"] for identity in identities.values()}
    missing_components = sorted(set(COMPONENTS) - observed_components)
    if missing_components:
        raise AnalysisFailure(
            "identity_components_missing",
            "identity snapshot is missing required components",
            missing=missing_components,
            snapshot=snapshot,
        )
    return identities


def validate_stable_identities(before_document, after_document) -> dict[tuple[str, str, str], dict]:
    before = normalize_identities(before_document, "before")
    after = normalize_identities(after_document, "after")
    before_keys = set(before)
    after_keys = set(after)
    if before_keys != after_keys:
        raise AnalysisFailure(
            "identity_set_changed",
            "pod or container identities changed during the scenario",
            missing_after=[identity_name(key) for key in sorted(before_keys - after_keys)],
            added_after=[identity_name(key) for key in sorted(after_keys - before_keys)],
        )
    changed = [
        {
            "identity": identity_name(key),
            "before": before[key],
            "after": after[key],
        }
        for key in sorted(before)
        if before[key] != after[key]
    ]
    if changed:
        raise AnalysisFailure(
            "identity_changed",
            "pod or container identity fields changed during the scenario",
            changed=changed,
        )
    return before


def pod_identities(container_identities: dict[tuple[str, str, str], dict]) -> dict:
    pods = {}
    for identity in container_identities.values():
        key = (identity["namespace"], identity["pod"])
        pod = {
            "namespace": identity["namespace"],
            "pod": identity["pod"],
            "pod_uid": identity["pod_uid"],
            "component": identity["component"],
        }
        if key in pods and pods[key] != pod:
            raise AnalysisFailure(
                "pod_identity_changed",
                f"container identities disagree about pod {pod_identity_name(key)}",
                pod=pod_identity_name(key),
            )
        pods[key] = pod
    return pods


def parse_decimal(value, field: str, **details) -> Decimal:
    if isinstance(value, bool) or not isinstance(value, (int, float, str)):
        raise AnalysisFailure("sample_value", f"{field} must be numeric", field=field, **details)
    try:
        parsed = Decimal(str(value))
    except InvalidOperation as error:
        raise AnalysisFailure(
            "sample_value", f"{field} must be numeric", field=field, **details
        ) from error
    if not parsed.is_finite():
        raise AnalysisFailure("sample_value", f"{field} must be finite", field=field, **details)
    return parsed


def parse_metric(
    document,
    metric_key: str,
    expected_identities: dict,
    identity_scope: str,
    window_start: Decimal,
    window_end: Decimal,
    source_timestamps: bool = False,
) -> dict:
    expected_metric_name = METRIC_NAMES[metric_key]
    input_name = f"{metric_key}_source_timestamps" if source_timestamps else metric_key
    if not isinstance(document, dict) or document.get("status") != "success":
        raise AnalysisFailure(
            "prometheus_response",
            f"{input_name} response status is not success",
            metric=input_name,
        )
    for annotation in ("warnings", "infos"):
        messages = document.get(annotation, [])
        if not isinstance(messages, list) or messages:
            raise AnalysisFailure(
                "prometheus_annotations",
                f"{input_name} response contains Prometheus {annotation}",
                annotation=annotation,
                messages=messages,
                metric=input_name,
            )
    data = document.get("data")
    if not isinstance(data, dict) or data.get("resultType") != "matrix":
        raise AnalysisFailure(
            "prometheus_response",
            f"{input_name} response must contain a matrix result",
            metric=input_name,
        )
    result = data.get("result")
    if not isinstance(result, list):
        raise AnalysisFailure(
            "prometheus_response",
            f"{input_name} result must be an array",
            metric=input_name,
        )

    series_by_identity = {}
    for series in result:
        if not isinstance(series, dict) or not isinstance(series.get("metric"), dict):
            raise AnalysisFailure(
                "prometheus_series",
                f"{input_name} series must contain metric labels",
                metric=input_name,
            )
        labels = series["metric"]
        if any(not isinstance(value, str) for value in labels.values()):
            raise AnalysisFailure(
                "metric_labels",
                f"{input_name} labels must be strings",
                metric=input_name,
            )
        namespace = require_nonempty_string(labels.get("namespace"), "namespace")
        pod = require_nonempty_string(labels.get("pod"), "pod")
        if identity_scope == "container":
            container = require_nonempty_string(labels.get("container"), "container")
            if container == "POD":
                raise AnalysisFailure(
                    "metric_identity_scope",
                    f"{input_name} contains a Kubernetes POD pseudo-container",
                    metric=input_name,
                    pod=f"{namespace}/{pod}",
                )
            key = (namespace, pod, container)
            display_name = identity_name(key)
        elif identity_scope == "pod_cgroup":
            if labels.get("container") not in (None, ""):
                raise AnalysisFailure(
                    "metric_identity_scope",
                    f"{input_name} must contain only pod-cgroup series",
                    container=labels.get("container"),
                    metric=input_name,
                    pod=f"{namespace}/{pod}",
                )
            key = (namespace, pod)
            display_name = pod_identity_name(key)
        else:
            raise ValueError(f"unsupported identity scope: {identity_scope}")
        returned_metric_name = labels.get("__name__")
        if source_timestamps and returned_metric_name is not None:
            raise AnalysisFailure(
                "metric_name",
                f"timestamp() must drop the metric name for {display_name}",
                expected=None,
                identity=display_name,
                metric=input_name,
                observed=returned_metric_name,
            )
        if not source_timestamps and returned_metric_name != expected_metric_name:
            raise AnalysisFailure(
                "metric_name",
                f"unexpected metric name for {display_name}",
                expected=expected_metric_name,
                identity=display_name,
                metric=input_name,
                observed=returned_metric_name,
            )
        if metric_key in ("container_cpu", "pod_cgroup_cpu") and labels.get("cpu") not in (
            None,
            "",
            "total",
        ):
            raise AnalysisFailure(
                "metric_cpu_scope",
                f"{input_name} contains a per-CPU series for {display_name}",
                cpu=labels.get("cpu"),
                identity=display_name,
                metric=input_name,
            )
        if key in series_by_identity:
            raise AnalysisFailure(
                "duplicate_series",
                f"duplicate {input_name} series for {display_name}",
                identity=display_name,
                metric=input_name,
            )
        values = series.get("values")
        if not isinstance(values, list) or not values:
            raise AnalysisFailure(
                "series_samples",
                f"{input_name} series has no samples for {display_name}",
                identity=display_name,
                metric=input_name,
            )
        samples = {}
        seen_timestamps = set()
        previous_timestamp = None
        for sample in values:
            if not isinstance(sample, list) or len(sample) != 2:
                raise AnalysisFailure(
                    "series_samples",
                    f"{input_name} sample must be [timestamp, value]",
                    identity=display_name,
                    metric=input_name,
                )
            timestamp = parse_decimal(
                sample[0], "timestamp", identity=display_name, metric=input_name
            )
            value = parse_decimal(sample[1], "value", identity=display_name, metric=input_name)
            if timestamp < 0 or value < 0:
                raise AnalysisFailure(
                    "sample_value",
                    f"{input_name} samples must not be negative for {display_name}",
                    identity=display_name,
                    metric=input_name,
                )
            if previous_timestamp is not None and timestamp < previous_timestamp:
                raise AnalysisFailure(
                    "sample_timestamp_order",
                    f"{input_name} timestamps must increase for {display_name}",
                    identity=display_name,
                    metric=input_name,
                    timestamp=decimal_number(timestamp),
                )
            if timestamp > window_end or (
                timestamp < window_start
                and any(existing >= window_start for existing in samples)
            ):
                raise AnalysisFailure(
                    "sample_outside_window",
                    f"{input_name} contains an evaluation outside the requested window",
                    identity=display_name,
                    metric=input_name,
                    timestamp=decimal_number(timestamp),
                    window_start=decimal_number(window_start),
                    window_end=decimal_number(window_end),
                )
            if timestamp in seen_timestamps:
                raise AnalysisFailure(
                    "duplicate_sample_timestamp",
                    f"duplicate {input_name} timestamp for {display_name}",
                    identity=display_name,
                    metric=input_name,
                    timestamp=decimal_number(timestamp),
                )
            seen_timestamps.add(timestamp)
            previous_timestamp = timestamp
            samples[timestamp] = value
        series_by_identity[key] = {
            "labels": dict(labels),
            "samples": samples,
        }

    expected_keys = set(expected_identities)
    observed_keys = set(series_by_identity)
    if expected_keys != observed_keys:
        raise AnalysisFailure(
            "metric_identity_mismatch",
            f"{input_name} series do not match stable {identity_scope} identities",
            metric=input_name,
            missing=["/".join(key) for key in sorted(expected_keys - observed_keys)],
            unexpected=["/".join(key) for key in sorted(observed_keys - expected_keys)],
        )
    return series_by_identity


def decimal_number(value: Decimal) -> int | float:
    integral = value.to_integral_value()
    if value == integral:
        return int(integral)
    return float(value)


def nearest_rank(values: list[Decimal], percentile: int) -> Decimal:
    ordered = sorted(values)
    rank = max(1, (percentile * len(ordered) + 99) // 100)
    return ordered[rank - 1]


def value_statistics(values: list[Decimal]) -> dict:
    return {
        "mean": decimal_number(sum(values, Decimal(0)) / Decimal(len(values))),
        "p95": decimal_number(nearest_rank(values, 95)),
        "max": decimal_number(max(values)),
        "last": decimal_number(values[-1]),
    }


def window_description(
    timestamps: list[Decimal],
    window_start: Decimal,
    window_end: Decimal,
) -> dict:
    description = {
        "requested_start_unix_seconds": decimal_number(window_start),
        "requested_end_unix_seconds": decimal_number(window_end),
        "source_sample_boundary": "(start,end]",
        "evaluation_step_seconds": decimal_number(EVALUATION_STEP_SECONDS),
        "max_source_age_seconds": decimal_number(MAX_SOURCE_AGE_SECONDS),
        "sample_count": len(timestamps),
        "interval_count": max(0, len(timestamps) - 1),
    }
    if timestamps:
        description.update(
            {
                "start_unix_seconds": decimal_number(timestamps[0]),
                "end_unix_seconds": decimal_number(timestamps[-1]),
                "window_seconds": decimal_number(timestamps[-1] - timestamps[0]),
            }
        )
    return description


def aligned_evaluation_timestamps(
    metric_series: dict[str, dict],
    source_timestamp_series: dict[str, dict],
    window_start: Decimal,
    window_end: Decimal,
) -> list[Decimal]:
    references = []
    for metric_key in sorted(metric_series):
        for identity in sorted(metric_series[metric_key]):
            values = metric_series[metric_key][identity]
            sources = source_timestamp_series[metric_key][identity]
            value_labels = dict(values["labels"])
            del value_labels["__name__"]
            if sources["labels"] != value_labels:
                raise AnalysisFailure(
                    "source_labels_mismatch",
                    "timestamp() labels do not match the value series",
                    identity="/".join(identity),
                    metric=metric_key,
                    value_labels=value_labels,
                    source_labels=sources["labels"],
                )
            value_grid = tuple(values["samples"])
            source_grid = tuple(sources["samples"])
            if source_grid != value_grid:
                raise AnalysisFailure(
                    "source_evaluation_grid_mismatch",
                    "timestamp() and value series do not share an evaluation grid",
                    identity="/".join(identity),
                    metric=metric_key,
                    value_timestamps=[decimal_number(value) for value in value_grid],
                    source_timestamps=[decimal_number(value) for value in source_grid],
                )
            references.append((metric_key, identity, value_grid))
    reference_metric, reference_identity, expected = references[0]
    for metric_key, identity, observed in references[1:]:
        if observed != expected:
            raise AnalysisFailure(
                "sample_timestamps_mismatch",
                "Prometheus series do not share one complete timestamp grid",
                reference={
                    "metric": reference_metric,
                    "identity": "/".join(reference_identity),
                },
                observed={"metric": metric_key, "identity": "/".join(identity)},
                reference_timestamps=[decimal_number(value) for value in expected],
                observed_timestamps=[decimal_number(value) for value in observed],
            )
    timestamps = list(expected)
    for left, right in zip(timestamps, timestamps[1:]):
        if right - left != EVALUATION_STEP_SECONDS:
            raise AnalysisFailure(
                "sample_step",
                "Prometheus evaluations must use the pinned five-second step",
                left=decimal_number(left),
                right=decimal_number(right),
                observed_step_seconds=decimal_number(right - left),
                expected_step_seconds=decimal_number(EVALUATION_STEP_SECONDS),
            )

    for metric_key in sorted(source_timestamp_series):
        for identity in sorted(source_timestamp_series[metric_key]):
            previous_source = None
            for evaluation, source in source_timestamp_series[metric_key][identity][
                "samples"
            ].items():
                details = {
                    "evaluation_timestamp": decimal_number(evaluation),
                    "identity": "/".join(identity),
                    "metric": metric_key,
                    "source_timestamp": decimal_number(source),
                }
                if source > window_end:
                    raise AnalysisFailure(
                        "source_timestamp_outside_window",
                        "a source timestamp is after the requested window",
                        window_end=decimal_number(window_end),
                        **details,
                    )
                if source > evaluation:
                    raise AnalysisFailure(
                        "source_timestamp_future",
                        "a source timestamp is after its evaluation",
                        **details,
                    )
                if previous_source is not None and source < previous_source:
                    raise AnalysisFailure(
                        "source_timestamp_regression",
                        "source timestamps must not decrease",
                        previous_source_timestamp=decimal_number(previous_source),
                        **details,
                    )
                previous_source = source

    first_retained = len(timestamps)
    for index, evaluation in enumerate(timestamps):
        if evaluation > window_start and all(
            source_timestamp_series[metric_key][identity]["samples"][evaluation]
            > window_start
            for metric_key in sorted(source_timestamp_series)
            for identity in sorted(source_timestamp_series[metric_key])
        ):
            first_retained = index
            break
    retained = timestamps[first_retained:]

    source_counts = {}
    for metric_key in sorted(source_timestamp_series):
        for identity in sorted(source_timestamp_series[metric_key]):
            retained_sources = [
                source_timestamp_series[metric_key][identity]["samples"][evaluation]
                for evaluation in retained
            ]
            source_counts[f"{metric_key}:{'/'.join(identity)}"] = len(
                set(retained_sources)
            )
            for evaluation, source in zip(retained, retained_sources):
                if source <= window_start:
                    raise AnalysisFailure(
                        "source_timestamp_outside_window",
                        "a retained source timestamp is outside the requested window",
                        evaluation_timestamp=decimal_number(evaluation),
                        identity="/".join(identity),
                        metric=metric_key,
                        source_timestamp=decimal_number(source),
                        window_start=decimal_number(window_start),
                    )
                age = evaluation - source
                if age > MAX_SOURCE_AGE_SECONDS:
                    raise AnalysisFailure(
                        "source_timestamp_stale",
                        "a retained source sample is more than ten seconds old",
                        age_seconds=decimal_number(age),
                        evaluation_timestamp=decimal_number(evaluation),
                        identity="/".join(identity),
                        max_source_age_seconds=decimal_number(MAX_SOURCE_AGE_SECONDS),
                        metric=metric_key,
                        source_timestamp=decimal_number(source),
                    )

    if len(retained) < 2 or any(count < 2 for count in source_counts.values()):
        raise InsufficientSamples(
            len(retained),
            retained,
            window_start,
            window_end,
            source_counts,
        )
    return retained


def cpu_statistics(values: list[Decimal], timestamps: list[Decimal], scope: str) -> dict:
    deltas = [right - left for left, right in zip(values, values[1:])]
    for index, delta in enumerate(deltas):
        if delta < 0:
            raise AnalysisFailure(
                "cpu_counter_reset",
                f"CPU counter decreased for {scope}",
                interval=index,
                scope=scope,
            )
    durations = [right - left for left, right in zip(timestamps, timestamps[1:])]
    rates = [delta / duration for delta, duration in zip(deltas, durations)]
    counter_delta = values[-1] - values[0]
    window = timestamps[-1] - timestamps[0]
    result = value_statistics(rates)
    result.update(
        {
            "counter_delta_seconds": decimal_number(counter_delta),
            "window_seconds": decimal_number(window),
            "normalized_cores": decimal_number(counter_delta / window),
        }
    )
    return result


def summarize_group(
    keys: list[tuple],
    cpu_series: dict,
    working_set_series: dict,
    timestamps: list[Decimal],
    scope: str,
    count_key: str,
    rss_series: dict | None = None,
) -> dict:
    def summed(series: dict) -> list[Decimal]:
        return [
            sum((series[key][timestamp] for key in keys), Decimal(0))
            for timestamp in timestamps
        ]

    result = {
        count_key: len(keys),
        "cpu_cores": cpu_statistics(summed(cpu_series), timestamps, scope),
        "working_set_bytes": value_statistics(summed(working_set_series)),
    }
    if rss_series is not None:
        result["rss_bytes"] = value_statistics(summed(rss_series))
    return result


def analyze_resources(
    container_cpu_document,
    container_working_set_document,
    container_rss_document,
    pod_cgroup_cpu_document,
    pod_cgroup_working_set_document,
    container_cpu_source_timestamps_document,
    container_working_set_source_timestamps_document,
    container_rss_source_timestamps_document,
    pod_cgroup_cpu_source_timestamps_document,
    pod_cgroup_working_set_source_timestamps_document,
    before_identities_document,
    after_identities_document,
    window_start: Decimal,
    window_end: Decimal,
) -> dict:
    if not window_start.is_finite() or not window_end.is_finite():
        raise AnalysisFailure("window_bounds", "resource window bounds must be finite")
    if window_start < 0 or window_end <= window_start:
        raise AnalysisFailure(
            "window_bounds",
            "resource window end must be greater than its non-negative start",
            window_start=decimal_number(window_start),
            window_end=decimal_number(window_end),
        )
    identities = validate_stable_identities(before_identities_document, after_identities_document)
    pods = pod_identities(identities)
    metric_documents = {
        "container_cpu": container_cpu_document,
        "container_working_set": container_working_set_document,
        "container_rss": container_rss_document,
        "pod_cgroup_cpu": pod_cgroup_cpu_document,
        "pod_cgroup_working_set": pod_cgroup_working_set_document,
    }
    source_timestamp_documents = {
        "container_cpu": container_cpu_source_timestamps_document,
        "container_working_set": container_working_set_source_timestamps_document,
        "container_rss": container_rss_source_timestamps_document,
        "pod_cgroup_cpu": pod_cgroup_cpu_source_timestamps_document,
        "pod_cgroup_working_set": pod_cgroup_working_set_source_timestamps_document,
    }
    identity_contracts = {
        "container_cpu": (identities, "container"),
        "container_working_set": (identities, "container"),
        "container_rss": (identities, "container"),
        "pod_cgroup_cpu": (pods, "pod_cgroup"),
        "pod_cgroup_working_set": (pods, "pod_cgroup"),
    }
    metric_series = {}
    source_timestamp_series = {}
    for metric_key in metric_documents:
        expected_identities, identity_scope = identity_contracts[metric_key]
        metric_series[metric_key] = parse_metric(
            metric_documents[metric_key],
            metric_key,
            expected_identities,
            identity_scope,
            window_start,
            window_end,
        )
        source_timestamp_series[metric_key] = parse_metric(
            source_timestamp_documents[metric_key],
            metric_key,
            expected_identities,
            identity_scope,
            window_start,
            window_end,
            source_timestamps=True,
        )
    timestamps = aligned_evaluation_timestamps(
        metric_series,
        source_timestamp_series,
        window_start,
        window_end,
    )
    metric_series = {
        metric_key: {
            identity: series["samples"]
            for identity, series in series_by_identity.items()
        }
        for metric_key, series_by_identity in metric_series.items()
    }
    keys = sorted(identities)

    containers = []
    for key in keys:
        identity = dict(identities[key])
        identity.update(
            {
                "cpu_cores": cpu_statistics(
                    [
                        metric_series["container_cpu"][key][timestamp]
                        for timestamp in timestamps
                    ],
                    timestamps,
                    identity_name(key),
                ),
                "working_set_bytes": value_statistics(
                    [
                        metric_series["container_working_set"][key][timestamp]
                        for timestamp in timestamps
                    ]
                ),
                "rss_bytes": value_statistics(
                    [
                        metric_series["container_rss"][key][timestamp]
                        for timestamp in timestamps
                    ]
                ),
            }
        )
        containers.append(identity)

    components = {}
    for component in COMPONENTS:
        component_keys = [key for key in keys if identities[key]["component"] == component]
        components[component] = summarize_group(
            component_keys,
            metric_series["container_cpu"],
            metric_series["container_working_set"],
            timestamps,
            f"container diagnostics {component}",
            "container_count",
            metric_series["container_rss"],
        )

    pod_keys = sorted(pods)
    pod_results = []
    for key in pod_keys:
        pod = dict(pods[key])
        pod.update(
            {
                "cpu_cores": cpu_statistics(
                    [metric_series["pod_cgroup_cpu"][key][timestamp] for timestamp in timestamps],
                    timestamps,
                    f"upstream-compatible pod cgroup {pod_identity_name(key)}",
                ),
                "working_set_bytes": value_statistics(
                    [
                        metric_series["pod_cgroup_working_set"][key][timestamp]
                        for timestamp in timestamps
                    ]
                ),
            }
        )
        pod_results.append(pod)

    pod_components = {}
    for component in COMPONENTS:
        component_keys = [key for key in pod_keys if pods[key]["component"] == component]
        pod_components[component] = summarize_group(
            component_keys,
            metric_series["pod_cgroup_cpu"],
            metric_series["pod_cgroup_working_set"],
            timestamps,
            f"upstream-compatible pod cgroups {component}",
            "pod_count",
        )

    return {
        "schema_version": SCHEMA_VERSION,
        "pass": True,
        "failures": [],
        "statistics": {
            "mean": "arithmetic mean on the validated evaluation grid",
            "p95": "nearest-rank",
            "cpu": "counter deltas divided by evaluation interval seconds",
        },
        "sample_validation": {
            "source": "paired timestamp(selector) query_range matrices",
            "source_boundary": "(start,end]",
            "max_source_age_seconds": decimal_number(MAX_SOURCE_AGE_SECONDS),
        },
        "window": window_description(timestamps, window_start, window_end),
        "identity": {
            "components": list(COMPONENTS),
            "pod_count": len(pods),
            "container_count": len(keys),
        },
        "upstream_compatible_pod_cgroups": {
            "series_scope": 'cAdvisor pod cgroups selected by container=""',
            "pods": pod_results,
            "components": pod_components,
            "fleet": summarize_group(
                pod_keys,
                metric_series["pod_cgroup_cpu"],
                metric_series["pod_cgroup_working_set"],
                timestamps,
                "upstream-compatible pod cgroups fleet",
                "pod_count",
            ),
        },
        "haptic_container_diagnostics": {
            "series_scope": "cAdvisor real containers, excluding POD pseudo-containers",
            "containers": containers,
            "components": components,
            "fleet": summarize_group(
                keys,
                metric_series["container_cpu"],
                metric_series["container_working_set"],
                timestamps,
                "container diagnostics fleet",
                "container_count",
                metric_series["container_rss"],
            ),
        },
    }


def read_json(path: Path, input_name: str):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise AnalysisFailure(
            "input_json",
            f"failed to read {input_name} JSON",
            input=input_name,
            error=str(error),
        ) from error


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cpu", required=True, type=Path)
    parser.add_argument("--working-set", required=True, type=Path)
    parser.add_argument("--rss", required=True, type=Path)
    parser.add_argument("--pod-cgroup-cpu", required=True, type=Path)
    parser.add_argument("--pod-cgroup-working-set", required=True, type=Path)
    parser.add_argument("--cpu-source-timestamps", required=True, type=Path)
    parser.add_argument("--working-set-source-timestamps", required=True, type=Path)
    parser.add_argument("--rss-source-timestamps", required=True, type=Path)
    parser.add_argument("--pod-cgroup-cpu-source-timestamps", required=True, type=Path)
    parser.add_argument("--pod-cgroup-working-set-source-timestamps", required=True, type=Path)
    parser.add_argument("--identities-before", required=True, type=Path)
    parser.add_argument("--identities-after", required=True, type=Path)
    parser.add_argument("--window-start", required=True)
    parser.add_argument("--window-end", required=True)
    parser.add_argument("--allow-insufficient-samples", action="store_true")
    parser.add_argument("--output", required=True, type=Path)
    return parser.parse_args(argv)


def write_result(path: Path, result: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def raw_artifacts(args: argparse.Namespace) -> dict:
    def relative_name(path: Path) -> str:
        return str(Path(path.parent.name) / path.name)

    return {
        "haptic_container_cpu": relative_name(args.cpu),
        "haptic_container_cpu_source_timestamps": relative_name(
            args.cpu_source_timestamps
        ),
        "haptic_container_working_set": relative_name(args.working_set),
        "haptic_container_working_set_source_timestamps": relative_name(
            args.working_set_source_timestamps
        ),
        "haptic_container_rss": relative_name(args.rss),
        "haptic_container_rss_source_timestamps": relative_name(
            args.rss_source_timestamps
        ),
        "upstream_compatible_pod_cgroup_cpu": relative_name(args.pod_cgroup_cpu),
        "upstream_compatible_pod_cgroup_cpu_source_timestamps": relative_name(
            args.pod_cgroup_cpu_source_timestamps
        ),
        "upstream_compatible_pod_cgroup_working_set": relative_name(
            args.pod_cgroup_working_set
        ),
        "upstream_compatible_pod_cgroup_working_set_source_timestamps": relative_name(
            args.pod_cgroup_working_set_source_timestamps
        ),
    }


def insufficient_result(error: InsufficientSamples, args: argparse.Namespace) -> dict:
    return {
        "schema_version": SCHEMA_VERSION,
        "analysis_status": "not_gated",
        "gating": False,
        "pass": None,
        "reason": str(error),
        "source_sample_counts": error.source_counts,
        "window": window_description(
            error.timestamps,
            error.window_start,
            error.window_end,
        ),
        "raw_artifacts": raw_artifacts(args),
    }


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv if argv is not None else sys.argv[1:])
    try:
        window_start = parse_decimal(args.window_start, "window-start")
        window_end = parse_decimal(args.window_end, "window-end")
        result = analyze_resources(
            read_json(args.cpu, "cpu"),
            read_json(args.working_set, "working_set"),
            read_json(args.rss, "rss"),
            read_json(args.pod_cgroup_cpu, "pod-cgroup-cpu"),
            read_json(args.pod_cgroup_working_set, "pod-cgroup-working-set"),
            read_json(args.cpu_source_timestamps, "cpu-source-timestamps"),
            read_json(
                args.working_set_source_timestamps,
                "working-set-source-timestamps",
            ),
            read_json(args.rss_source_timestamps, "rss-source-timestamps"),
            read_json(
                args.pod_cgroup_cpu_source_timestamps,
                "pod-cgroup-cpu-source-timestamps",
            ),
            read_json(
                args.pod_cgroup_working_set_source_timestamps,
                "pod-cgroup-working-set-source-timestamps",
            ),
            read_json(args.identities_before, "identities-before"),
            read_json(args.identities_after, "identities-after"),
            window_start,
            window_end,
        )
    except InsufficientSamples as error:
        if args.allow_insufficient_samples:
            result = insufficient_result(error, args)
        else:
            result = error.failure_result()
    except AnalysisFailure as error:
        result = error.result()
    write_result(args.output, result)
    if result["pass"] is False:
        print(f"gateway-api-bench resource validation failed; see {args.output}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
