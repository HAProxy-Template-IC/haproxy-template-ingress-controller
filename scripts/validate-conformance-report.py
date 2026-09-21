#!/usr/bin/env python3
"""Validate a complete Gateway API report without hiding partial support."""

import argparse
import json
from pathlib import Path

import yaml


PROFILES = {"GATEWAY-HTTP", "GATEWAY-TLS", "GATEWAY-GRPC"}


def validate_status(status, profile, level):
    if not isinstance(status, dict):
        raise ValueError(f"{profile} is missing its {level} result")
    result = status.get("result")
    if result not in {"success", "partial", "failure"}:
        raise ValueError(f"{profile} has an invalid {level} result")
    counts = status.get("statistics", {})
    if not isinstance(counts, dict) or any(
        type(counts.get(key)) is not int or counts[key] < 0
        for key in ("Passed", "Skipped", "Failed")
    ):
        raise ValueError(f"{profile} has invalid {level} statistics")
    if counts["Failed"] and result != "failure":
        raise ValueError(f"{profile} hides failed {level} tests")
    if level == "core" and (
        result != "success"
        or counts["Passed"] == 0
        or counts["Skipped"]
        or counts["Failed"]
        or status.get("skippedTests")
        or status.get("failedTests")
    ):
        raise ValueError(f"{profile} has incomplete or failing core coverage")
    if result == "failure" or status.get("failedTests"):
        raise ValueError(f"{profile} has failing {level} coverage")
    return {
        "result": result,
        "statistics": counts,
        "skipped_tests": status.get("skippedTests", []),
        "unsupported_features": status.get("unsupportedFeatures", []),
    }


def validate_report(report, implementation_version, gateway_version):
    if not isinstance(report, dict) or report.get("kind") != "ConformanceReport":
        raise ValueError("a ConformanceReport document is required")
    identity = report.get("implementation", {})
    if not isinstance(identity, dict) or (
        identity.get("project") != "haptic"
        or identity.get("organization") != "haproxy-haptic"
        or identity.get("version") != implementation_version
    ):
        raise ValueError("report implementation does not match the tested HAPTIC version")
    if report.get("gatewayAPIVersion") != gateway_version:
        raise ValueError("report Gateway API version does not match the tested suite")
    if report.get("gatewayAPIChannel") not in {"standard", "experimental"}:
        raise ValueError("report Gateway API channel is missing or invalid")
    profiles = report.get("profiles")
    if not isinstance(profiles, list) or len(profiles) != len(PROFILES):
        raise ValueError("all three Gateway conformance profiles are required")
    if any(not isinstance(profile, dict) or not isinstance(profile.get("name"), str) for profile in profiles):
        raise ValueError("report contains an invalid profile")
    if {profile.get("name") for profile in profiles} != PROFILES:
        raise ValueError("report has missing, duplicate, or unexpected profiles")
    results = {}
    for profile in profiles:
        name = profile["name"]
        results[name] = {"core": validate_status(profile.get("core"), name, "core")}
        if "extended" in profile:
            results[name]["extended"] = validate_status(profile["extended"], name, "extended")
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("report", type=Path)
    parser.add_argument("--implementation-version", required=True)
    parser.add_argument("--gateway-version", required=True)
    args = parser.parse_args()
    try:
        if not args.report.is_file() or not 0 < args.report.stat().st_size <= 2 * 1024 * 1024:
            raise ValueError("conformance report is missing, empty, or larger than 2 MiB")
        report = yaml.safe_load(args.report.read_text())
        results = validate_report(report, args.implementation_version, args.gateway_version)
    except (OSError, ValueError, yaml.YAMLError) as error:
        parser.exit(1, f"Invalid conformance report: {error}\n")
    print(json.dumps(results, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
