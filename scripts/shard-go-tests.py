#!/usr/bin/env python3
"""Partition a successful `go test -list '^Test'` inventory into complete shards."""

import argparse
import hashlib
import re
import sys


def partition(inventory, total):
    if total < 1:
        raise ValueError("shard total must be positive")
    names = sorted({line.strip() for line in inventory.splitlines() if re.fullmatch(r"Test\w*", line.strip())})
    if len(names) < total:
        raise ValueError("test inventory has fewer tests than shards")
    ordered = sorted(names, key=lambda name: hashlib.sha256(name.encode()).digest())
    shards = [sorted(ordered[index::total]) for index in range(total)]
    if sorted(name for shard in shards for name in shard) != names:
        raise ValueError("shards do not cover the test inventory exactly once")
    return shards


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("index", type=int, help="one-based shard index")
    parser.add_argument("total", type=int)
    args = parser.parse_args()
    if not 1 <= args.index <= args.total:
        parser.error("shard index must be between one and the shard total")
    try:
        shards = partition(sys.stdin.read(), args.total)
    except ValueError as error:
        parser.error(str(error))
    print("^(" + "|".join(re.escape(name) for name in shards[args.index - 1]) + ")$")


if __name__ == "__main__":
    main()
