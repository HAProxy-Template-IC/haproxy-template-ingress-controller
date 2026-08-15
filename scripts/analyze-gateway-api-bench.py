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
"""Validate and summarize gateway-api-bench probe output."""

import argparse
import json
import math
import re
import sys
from collections import Counter, defaultdict
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import NamedTuple


SUCCESS_MARKER = "probe completed: 200"
ERROR_MARKER = "unexpected status code:"
RESERVED_METADATA_KEYS = {
    "errors",
    "expectations",
    "failures",
    "gateways",
    "observed",
    "pass",
    "scenario",
    "samples",
    "schema_version",
}
LABEL_PATTERN = re.compile(r"(?:^|\s)(gateway|iter|latency)=([^\s]+)")
STATUS_PATTERN = re.compile(r"unexpected status code:\s*([0-9]+)")
DURATION_TOKEN_PATTERN = re.compile(
    r"(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:ns|us|\N{MICRO SIGN}s|\N{GREEK SMALL LETTER MU}s|ms|s|m|h)"
)
DURATION_NUMBER_PATTERN = re.compile(r"([0-9]+(?:\.[0-9]*)?|\.[0-9]+)(.*)")
DURATION_UNITS_NS = {
    "ns": Decimal(1),
    "us": Decimal(1_000),
    "\N{MICRO SIGN}s": Decimal(1_000),
    "\N{GREEK SMALL LETTER MU}s": Decimal(1_000),
    "ms": Decimal(1_000_000),
    "s": Decimal(1_000_000_000),
    "m": Decimal(60_000_000_000),
    "h": Decimal(3_600_000_000_000),
}


class Sample(NamedTuple):
    gateway: str
    iteration: int
    latency: str
    latency_ns: int
    line_number: int


class UnexpectedStatus(NamedTuple):
    status: int | None
    gateway: str | None
    iteration: int | None
    line_number: int


def parse_go_duration_ns(value: str) -> int:
    sign = 1
    unsigned = value
    if unsigned.startswith(("+", "-")):
        if unsigned[0] == "-":
            sign = -1
        unsigned = unsigned[1:]
    if not unsigned:
        raise ValueError("empty duration")

    total = Decimal(0)
    position = 0
    for token_match in DURATION_TOKEN_PATTERN.finditer(unsigned):
        if token_match.start() != position:
            raise ValueError(f"invalid Go duration: {value}")
        token = token_match.group(0)
        number_match = DURATION_NUMBER_PATTERN.fullmatch(token)
        if number_match is None:
            raise ValueError(f"invalid Go duration: {value}")
        try:
            number = Decimal(number_match.group(1))
        except InvalidOperation as error:
            raise ValueError(f"invalid Go duration: {value}") from error
        total += number * DURATION_UNITS_NS[number_match.group(2)]
        position = token_match.end()

    if position != len(unsigned):
        raise ValueError(f"invalid Go duration: {value}")
    total *= sign
    integral = total.to_integral_value()
    if total != integral:
        raise ValueError(f"duration is not an integral number of nanoseconds: {value}")
    result = int(integral)
    if result < 0:
        raise ValueError(f"duration must not be negative: {value}")
    return result


def parse_probe_log(text: str) -> tuple[list[Sample], list[UnexpectedStatus], list[dict]]:
    samples = []
    errors = []
    malformed = []

    for line_number, line in enumerate(text.splitlines(), start=1):
        label_pairs = LABEL_PATTERN.findall(line)
        labels = dict(label_pairs)
        duplicate_labels = sorted(
            label
            for label, count in Counter(label for label, _ in label_pairs).items()
            if count > 1
        )
        if (SUCCESS_MARKER in line or ERROR_MARKER in line) and duplicate_labels:
            malformed.append(
                {
                    "line_number": line_number,
                    "reason": f"duplicate labels: {', '.join(duplicate_labels)}",
                }
            )
            continue
        if SUCCESS_MARKER in line:
            missing = sorted({"gateway", "iter", "latency"} - labels.keys())
            if missing:
                malformed.append(
                    {
                        "line_number": line_number,
                        "reason": f"missing labels: {', '.join(missing)}",
                    }
                )
                continue
            try:
                iteration = int(labels["iter"])
                if iteration < 0:
                    raise ValueError("iteration must not be negative")
                latency_ns = parse_go_duration_ns(labels["latency"])
            except ValueError as error:
                malformed.append({"line_number": line_number, "reason": str(error)})
                continue
            samples.append(
                Sample(
                    gateway=labels["gateway"],
                    iteration=iteration,
                    latency=labels["latency"],
                    latency_ns=latency_ns,
                    line_number=line_number,
                )
            )

        if ERROR_MARKER in line:
            status_match = STATUS_PATTERN.search(line)
            try:
                iteration = int(labels["iter"]) if "iter" in labels else None
            except ValueError:
                iteration = None
            errors.append(
                UnexpectedStatus(
                    status=int(status_match.group(1)) if status_match else None,
                    gateway=labels.get("gateway"),
                    iteration=iteration,
                    line_number=line_number,
                )
            )

    return samples, errors, malformed


def nearest_rank(sorted_values: list[int], percentile: int) -> int:
    rank = math.ceil(percentile * len(sorted_values) / 100)
    return sorted_values[max(1, rank) - 1]


def latency_statistics(values: list[int]) -> dict:
    ordered = sorted(values)
    middle = len(ordered) // 2
    if len(ordered) % 2:
        median: int | float = ordered[middle]
    else:
        median = (ordered[middle - 1] + ordered[middle]) / 2
    return {
        "max": ordered[-1],
        "mean": sum(ordered) / len(ordered),
        "median": median,
        "p90": nearest_rank(ordered, 90),
        "p95": nearest_rank(ordered, 95),
        "p99": nearest_rank(ordered, 99),
    }


def build_result(
    samples: list[Sample],
    errors: list[UnexpectedStatus],
    malformed: list[dict],
    expected_samples: int,
    expected_routes: int,
    expected_gateways: int,
    metadata: dict,
    initial_failures: list[dict] | None = None,
    expected_gateway_names: list[str] | None = None,
) -> dict:
    failures = list(initial_failures or [])
    samples_by_key = defaultdict(list)
    for sample in samples:
        samples_by_key[(sample.gateway, sample.iteration)].append(sample)
    unique_samples = {key: values[0] for key, values in samples_by_key.items()}
    duplicate_keys = [
        {"gateway": gateway, "iter": iteration, "count": len(samples_by_key[(gateway, iteration)])}
        for gateway, iteration in sorted(samples_by_key)
        if len(samples_by_key[(gateway, iteration)]) > 1
    ]

    route_sets = defaultdict(set)
    for gateway, iteration in unique_samples:
        route_sets[gateway].add(iteration)
    observed_gateways = sorted(route_sets)
    observed_routes = sorted({iteration for routes in route_sets.values() for iteration in routes})
    expected_iterations = set(range(expected_routes))

    if expected_samples != expected_routes * expected_gateways:
        failures.append(
            {
                "code": "inconsistent_expectations",
                "expected_samples": expected_samples,
                "routes_times_gateways": expected_routes * expected_gateways,
            }
        )
    if len(unique_samples) != expected_samples:
        failures.append(
            {
                "code": "sample_count",
                "expected": expected_samples,
                "observed": len(unique_samples),
            }
        )
    if len(observed_routes) != expected_routes:
        failures.append(
            {"code": "route_count", "expected": expected_routes, "observed": len(observed_routes)}
        )
    if set(observed_routes) != expected_iterations:
        failures.append(
            {
                "code": "route_ids",
                "missing": sorted(expected_iterations - set(observed_routes)),
                "unexpected": sorted(set(observed_routes) - expected_iterations),
            }
        )
    if len(observed_gateways) != expected_gateways:
        failures.append(
            {
                "code": "gateway_count",
                "expected": expected_gateways,
                "observed": len(observed_gateways),
            }
        )
    if expected_gateway_names is not None:
        expected_gateway_set = set(expected_gateway_names)
        if len(expected_gateway_names) != expected_gateways:
            failures.append(
                {
                    "code": "expected_gateway_names_count",
                    "expected": expected_gateways,
                    "observed": len(expected_gateway_names),
                }
            )
        if len(expected_gateway_set) != len(expected_gateway_names):
            duplicates = sorted(
                gateway
                for gateway, count in Counter(expected_gateway_names).items()
                if count > 1
            )
            failures.append({"code": "duplicate_expected_gateway_names", "gateways": duplicates})
        if set(observed_gateways) != expected_gateway_set:
            failures.append(
                {
                    "code": "gateway_names",
                    "missing": sorted(expected_gateway_set - set(observed_gateways)),
                    "unexpected": sorted(set(observed_gateways) - expected_gateway_set),
                }
            )
    incomplete_gateways = [
        {"gateway": gateway, "expected": expected_routes, "observed": len(route_sets[gateway])}
        for gateway in observed_gateways
        if len(route_sets[gateway]) != expected_routes
    ]
    if incomplete_gateways:
        failures.append({"code": "gateway_route_count", "gateways": incomplete_gateways})
    gateways_with_wrong_routes = [
        {
            "gateway": gateway,
            "missing": sorted(expected_iterations - route_sets[gateway]),
            "unexpected": sorted(route_sets[gateway] - expected_iterations),
        }
        for gateway in observed_gateways
        if route_sets[gateway] != expected_iterations
    ]
    if gateways_with_wrong_routes:
        failures.append({"code": "gateway_route_ids", "gateways": gateways_with_wrong_routes})
    distinct_route_sets = {tuple(sorted(routes)) for routes in route_sets.values()}
    if len(distinct_route_sets) > 1:
        failures.append({"code": "gateway_route_sets_differ"})
    if duplicate_keys:
        failures.append({"code": "duplicate_samples", "samples": duplicate_keys})
    if errors:
        failures.append({"code": "unexpected_statuses", "count": len(errors)})
    if malformed:
        failures.append({"code": "malformed_probe_lines", "lines": malformed})

    error_counts = Counter(error.gateway for error in errors if error.gateway is not None)
    gateway_results = {}
    for gateway in observed_gateways:
        values = [
            sample.latency_ns
            for (sample_gateway, _), sample in unique_samples.items()
            if sample_gateway == gateway
        ]
        gateway_results[gateway] = {
            "count": len(values),
            "error_count": error_counts[gateway],
            "latency_ns": latency_statistics(values),
        }

    result = {
        key: value for key, value in metadata.items() if key not in RESERVED_METADATA_KEYS
    }
    expectations = {
        "gateways": expected_gateways,
        "routes": expected_routes,
        "samples": expected_samples,
    }
    if expected_gateway_names is not None:
        expectations["gateway_names"] = sorted(expected_gateway_names)
    result.update(
        {
            "schema_version": 1,
            "scenario": "probe",
            "expectations": expectations,
            "observed": {
                "duplicate_samples": sum(len(values) - 1 for values in samples_by_key.values()),
                "error_count": len(errors),
                "gateways": len(observed_gateways),
                "routes": len(observed_routes),
                "sample_lines": len(samples),
                "samples": len(unique_samples),
            },
            "samples": [
                {
                    "gateway": sample.gateway,
                    "iter": sample.iteration,
                    "latency": sample.latency,
                    "latency_ns": sample.latency_ns,
                    "line_number": sample.line_number,
                }
                for sample in sorted(
                    samples,
                    key=lambda item: (item.gateway, item.iteration, item.line_number),
                )
            ],
            "gateways": gateway_results,
            "errors": [
                {
                    "gateway": error.gateway,
                    "iter": error.iteration,
                    "line_number": error.line_number,
                    "status": error.status,
                }
                for error in sorted(
                    errors,
                    key=lambda item: (
                        item.gateway or "",
                        item.iteration if item.iteration is not None else -1,
                        item.line_number,
                    ),
                )
            ],
            "failures": failures,
            "pass": not failures,
        }
    )
    return result


def positive_integer(value: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be greater than zero")
    return parsed


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--log", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--expected-samples", required=True, type=positive_integer)
    parser.add_argument("--expected-routes", required=True, type=positive_integer)
    parser.add_argument("--expected-gateways", required=True, type=positive_integer)
    parser.add_argument("--expected-gateway", action="append")
    parser.add_argument("--metadata", type=Path)
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv if argv is not None else sys.argv[1:])
    failures = []
    metadata = {}

    if args.metadata is not None:
        try:
            loaded_metadata = json.loads(args.metadata.read_text(encoding="utf-8"))
            if not isinstance(loaded_metadata, dict):
                raise ValueError("metadata must be a JSON object")
            metadata = loaded_metadata
            collisions = sorted(RESERVED_METADATA_KEYS & metadata.keys())
            if collisions:
                failures.append({"code": "reserved_metadata_keys", "keys": collisions})
        except (OSError, UnicodeError, json.JSONDecodeError, ValueError) as error:
            failures.append({"code": "metadata", "message": str(error)})

    try:
        log_text = args.log.read_text(encoding="utf-8", errors="replace")
        samples, errors, malformed = parse_probe_log(log_text)
    except OSError as error:
        samples, errors, malformed = [], [], []
        failures.append({"code": "log", "message": str(error)})

    result = build_result(
        samples=samples,
        errors=errors,
        malformed=malformed,
        expected_samples=args.expected_samples,
        expected_routes=args.expected_routes,
        expected_gateways=args.expected_gateways,
        metadata=metadata,
        initial_failures=failures,
        expected_gateway_names=args.expected_gateway,
    )
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    if not result["pass"]:
        print(f"gateway-api-bench probe validation failed; see {args.output}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
