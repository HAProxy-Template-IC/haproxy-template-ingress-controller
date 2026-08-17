#!/usr/bin/env python3
"""Run a command and print its wall time and peak RSS (ru_maxrss of children)."""
import resource
import subprocess
import sys
import time

before = resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss
t0 = time.monotonic()
rc = subprocess.call(sys.argv[1:])
wall = time.monotonic() - t0
after = resource.getrusage(resource.RUSAGE_CHILDREN).ru_maxrss
print(
    f"__RUNMAX__ rc={rc} wall_s={wall:.3f} maxrss_kb={after} "
    f"(prev_children_max={before})",
    file=sys.stderr,
)
sys.exit(rc)
