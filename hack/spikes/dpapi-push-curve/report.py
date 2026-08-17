#!/usr/bin/env python3
"""Print the measurement JSONs as a markdown table.

usage: report.py results/*.json
"""
import json
import os
import sys

ROWS = [
    ("a1 DPAPI raw push, skip_reload=true", "dpapi", "push_skip_reload"),
    ("a2 DPAPI raw push, force_reload=true", "dpapi", "push_force_reload"),
    ("a2' … until reload reports done", "dpapi", "reload_complete"),
    ("a0 GET configuration/version", "dpapi", "get_version"),
    ("b0 read back all map files (diff input)", "dpapi", "map_read_all"),
    ("b1 storage map replace (skip_reload)", "dpapi", "map_storage_put"),
    ("b2 runtime map entry add", "dpapi", "map_runtime_add"),
    ("c1 write cfg + all maps to disk", "plain", "write_files"),
    ("c2 master-socket reload after write", "plain", "reload_after_write"),
    ("c  write + reload (total)", "plain", "write_plus_reload"),
    ("d  haproxy -dr -c -f (via docker exec)", "plain", "validate_only"),
    ("d' docker exec overhead baseline", "plain", "docker_exec_baseline"),
    ("e  master-socket reload, no change", "plain", "reload_only"),
]


def main():
    files = sys.argv[1:]
    data = []
    for f in files:
        d = json.load(open(f))
        d["_name"] = os.path.basename(f).replace(".json", "")
        data.append(d)
    header = "| measurement | " + " | ".join(
        f"{d['_name']} p50 / max (ms)" for d in data) + " |"
    print(header)
    print("|" + "---|" * (len(data) + 1))
    for label, phase, key in ROWS:
        cells = []
        for d in data:
            s = (d.get(phase) or {}).get(key)
            cells.append(f"{s['p50']} / {s['max']}" if s else "—")
        print(f"| {label} | " + " | ".join(cells) + " |")
    print()
    for d in data:
        cpu = d.get("dpapi_cpu_seconds", {})
        cfg = d.get("config", {})
        meta = d.get("dpapi_meta", {})
        print(f"{d['_name']}: on-disk cfg after push {cfg.get('bytes')}B/"
              f"{cfg.get('lines')} lines, {cfg.get('maps_files')} maps "
              f"{cfg.get('maps_bytes')}B; dpapi CPU ms per push: "
              f"skip_reload {cpu.get('push_skip_reload', {}).get('p50')}, "
              f"force_reload {cpu.get('push_force_reload', {}).get('p50')}; "
              f"maps read {meta.get('maps_read_bytes')}B")


if __name__ == "__main__":
    main()
