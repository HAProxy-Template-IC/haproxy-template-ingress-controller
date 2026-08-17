#!/usr/bin/env python3
"""Add a worker runtime socket to the global section.

The bundled chart ships no `stats socket` — the Dataplane API is pointed at the
master socket alone (master_runtime). With that, DPAPI 3.4 lists no runtime
maps, so the runtime map-entry path cannot be exercised. Adding an explicit
worker socket is the only way to measure it; the deviation is recorded in
RESULTS.md.

usage: add_stats_socket.py CONFIG
"""
import sys

path = sys.argv[1]
lines = open(path).read().split("\n")
out = []
done = False
for line in lines:
    out.append(line)
    if not done and line.strip() == "global":
        out.append("  stats socket /etc/haproxy/haproxy-runtime.sock mode 666 level admin expose-fd listeners")
        done = True
open(path, "w").write("\n".join(out))
print("added" if done else "no global section found")
