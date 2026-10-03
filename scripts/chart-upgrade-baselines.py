#!/usr/bin/env python3
"""Print the chart versions to upgrade FROM, given registry tags on stdin.

Usage: chart-upgrade-baselines.py <version-under-test> < tags-list.json
"""

import json
import re
import sys

STABLE = re.compile(r"^(\d+)\.(\d+)\.(\d+)$")
VERSION = re.compile(r"^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$")
REQUIRED = ["0.2.0-alpha.3"]


def not_above(tag, target):
    """A stable tag at or below target; X.Y.Z sits above any X.Y.Z-pre."""
    match = VERSION.match(target)
    if not match:
        raise ValueError(f"version under test is not semver: {target!r}")
    target_core = tuple(int(x) for x in match.group(1, 2, 3))
    core = tuple(int(x) for x in STABLE.match(tag).groups())
    return core < target_core or (core == target_core and match.group(4) is None)


def baselines(tags, target):
    stable = sorted((t for t in tags if STABLE.match(t)), key=lambda v: tuple(int(x) for x in v.split(".")))
    if not stable:
        raise ValueError("No published stable chart versions found")
    missing = set(REQUIRED) - set(tags)
    if missing:
        raise ValueError(f"Missing required upgrade baselines: {sorted(missing)}")
    return [t for t in stable if not_above(t, target)] + REQUIRED


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    try:
        print("\n".join(baselines(json.load(sys.stdin).get("tags", []), sys.argv[1])))
    except ValueError as err:
        sys.exit(str(err))


if __name__ == "__main__":
    main()
