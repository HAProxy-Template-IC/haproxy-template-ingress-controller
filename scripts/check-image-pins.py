#!/usr/bin/env python3

import collections
import json
from pathlib import Path
import re
import subprocess
import sys

import yaml


def to_python(pattern):
    return re.sub(r"\(\?<(?![=!])", "(?P<", pattern)


def dependency_name(template, captures):
    def substitute(match):
        name = match.group(1)
        value = captures.get(name)
        if not value:
            raise ValueError(f"dependency template {template!r} has no capture for {name}")
        return value

    result = re.sub(r"\{\{\{?(\w+)\}?\}\}", substitute, template)
    if "{{" in result:
        raise ValueError(f"unsupported dependency template: {template!r}")
    return result


def collect_pins(managers, paths, read_text):
    pins = collections.defaultdict(lambda: collections.defaultdict(set))
    for manager in managers:
        template = manager.get("depNameTemplate", "")
        if not template:
            continue
        patterns = [re.compile(to_python(s)) for s in manager.get("matchStrings", [])]
        matchers = [re.compile(s) for s in manager.get("fileMatch", [])]
        matched_versions = 0
        for path in paths:
            if not any(matcher.search(path) for matcher in matchers):
                continue
            text = read_text(path)
            for pattern in patterns:
                for found in pattern.finditer(text):
                    captures = found.groupdict()
                    version = captures.get("currentValue")
                    if version:
                        matched_versions += 1
                        dep = dependency_name(template, captures)
                        pins[dep][version].add(path)
        if not matched_versions:
            raise ValueError(f"image pin manager {template!r} matched no version pins")
    return pins


def report_pins(pins, out):
    if not pins:
        print("FAIL: no image pins found", file=out)
        return True
    failed = False
    for dep in sorted(pins):
        versions = pins[dep]
        if len(versions) > 1:
            failed = True
            print(f"\nFAIL: {dep} is pinned to {len(versions)} different versions:", file=out)
            for version in sorted(versions):
                for path in sorted(versions[version]):
                    print(f"  {version}\t{path}", file=out)
        else:
            version = next(iter(versions))
            print(f"OK: {dep} = {version} ({len(versions[version])} files agree)", file=out)
    if failed:
        print("\nUpdate every pin of each named dependency to the same version.", file=out)
    return failed


def validate_haproxy_ci_pins(patches, variables):
    expected = {"HAPROXY_IMAGE_" + series.replace(".", ""): patch for series, patch in patches.items()}
    actual = {name: image for name, image in variables.items() if name.startswith("HAPROXY_IMAGE_")}
    if expected.keys() != actual.keys():
        raise ValueError("CI HAProxy image variables must cover exactly the chart's supported series")
    for name, patch in expected.items():
        match = re.fullmatch(r"[^@\s]+:(\d+\.\d+\.\d+)@sha256:[a-f0-9]{64}", str(actual[name]))
        if not match or match.group(1) != patch:
            raise ValueError(f"{name} must pin chart patch {patch} and a SHA-256 image digest")


def main():
    try:
        # Only tracked files belong to this checkout, not its nested worktrees or caches.
        paths = subprocess.run(
            ["git", "ls-files", "-z"], capture_output=True, check=True, text=True
        ).stdout.split("\0")
        managers = json.loads(Path("renovate.json").read_text(encoding="utf-8")).get("customManagers", [])
        chart = yaml.safe_load(Path("charts/haptic/values.yaml").read_text(encoding="utf-8"))
        build_pins = yaml.safe_load(Path(".gitlab/ci/build-pins.yml").read_text(encoding="utf-8"))
        validate_haproxy_ci_pins(chart["haproxyPatchVersions"], build_pins["variables"])
        pins = collect_pins(managers, paths, lambda path: Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError, KeyError, yaml.YAMLError, subprocess.CalledProcessError) as error:
        print(f"Image pin check failed: {error}", file=sys.stderr)
        return 1
    return int(report_pins(pins, sys.stdout))


if __name__ == "__main__":
    sys.exit(main())
