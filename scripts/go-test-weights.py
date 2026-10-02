#!/usr/bin/env python3
"""Write per-test shard weights from `go test -v` logs, such as CI job traces.

Usage: go-test-weights.py LOG... > tests/e2e/shard-weights.json
A test is parallel when its log shows `=== PAUSE` for it. A test measured in
several logs keeps its longest duration; a skip measures nothing and is left out.
"""

import json
import re
import sys

RESULT = re.compile(r"--- (?:PASS|FAIL): (Test\w+) \(([\d.]+)s\)")
PAUSE = re.compile(r"=== PAUSE (Test\w+)\s*$")


def weights(logs):
    seconds, parallel = {}, set()
    for log in logs:
        for line in log.splitlines():
            if match := PAUSE.search(line):
                parallel.add(match.group(1))
            elif match := RESULT.search(line):
                name, duration = match.group(1), float(match.group(2))
                seconds[name] = max(seconds.get(name, 0.0), duration)
    return {name: {"seconds": round(seconds[name], 1), "parallel": name in parallel} for name in sorted(seconds)}


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    logs = []
    for path in sys.argv[1:]:
        with open(path, encoding="utf-8", errors="replace") as handle:
            logs.append(handle.read())
    json.dump(weights(logs), sys.stdout, indent=1)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
