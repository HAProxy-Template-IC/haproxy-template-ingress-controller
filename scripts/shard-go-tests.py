#!/usr/bin/env python3
"""Partition a successful `go test -list '^Test'` inventory into complete shards."""

import argparse
import hashlib
import json
import re
import statistics
import sys


def unmeasured_weight(weights):
    """Estimate a test without a weight as a typical serial cluster test."""
    # Sub-second entries are helper tests that never touch the cluster.
    serial_seconds = [w["seconds"] for w in weights.values() if not w["parallel"] and w["seconds"] >= 1]
    return {"seconds": statistics.median(serial_seconds) if serial_seconds else 1.0, "parallel": False}


def partition(inventory, total, weights=None, include=None):
    """Split the inventory into `total` shards of balanced measured cost.

    weights maps a test name to {"seconds": float, "parallel": bool}, as written
    by go-test-weights.py. Serial and parallel tests are balanced separately:
    serial tests add up on a shard's wall clock, parallel ones overlap.
    """
    if total < 1:
        raise ValueError("shard total must be positive")
    names = sorted({line.strip() for line in inventory.splitlines() if re.fullmatch(r"Test\w*", line.strip())})
    if include is not None:
        selected = include.splitlines()
        if not selected or any(not re.fullmatch(r"Test\w*", name) for name in selected):
            raise ValueError("selection must contain one test name per line")
        if len(selected) != len(set(selected)):
            raise ValueError("selection contains duplicate test names")
        missing = set(selected) - set(names)
        if missing:
            raise ValueError("selected tests missing from compiled inventory: " + ", ".join(sorted(missing)))
        names = sorted(selected)
    if len(names) < total:
        raise ValueError("test inventory has fewer tests than shards")
    weights = weights or {}
    unmeasured = unmeasured_weight(weights)

    def hashed(name):
        return hashlib.sha256(name.encode()).digest()

    shards = [[] for _ in range(total)]
    for parallel in (False, True):
        load = [0.0] * total
        lane = [name for name in names if weights.get(name, unmeasured)["parallel"] == parallel]
        for name in sorted(lane, key=lambda name: (-weights.get(name, unmeasured)["seconds"], hashed(name))):
            target = min(range(total), key=lambda index: (load[index], len(shards[index]), index))
            shards[target].append(name)
            load[target] += weights.get(name, unmeasured)["seconds"]
    shards = [sorted(shard) for shard in shards]
    if sorted(name for shard in shards for name in shard) != names:
        raise ValueError("shards do not cover the test inventory exactly once")
    return shards


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("index", type=int, help="one-based shard index")
    parser.add_argument("total", type=int)
    parser.add_argument("--weights", help="JSON file written by go-test-weights.py")
    parser.add_argument("--include", help="File of exact test names to select from the compiled inventory")
    args = parser.parse_args()
    if not 1 <= args.index <= args.total:
        parser.error("shard index must be between one and the shard total")
    weights = None
    if args.weights:
        with open(args.weights, encoding="utf-8") as handle:
            weights = json.load(handle)
    include = None
    if args.include:
        with open(args.include, encoding="utf-8") as handle:
            include = handle.read()
    try:
        shards = partition(sys.stdin.read(), args.total, weights, include)
    except ValueError as error:
        parser.error(str(error))
    print("^(" + "|".join(re.escape(name) for name in shards[args.index - 1]) + ")$")


if __name__ == "__main__":
    main()
