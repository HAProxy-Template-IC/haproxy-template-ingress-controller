#!/usr/bin/env python3
"""Reconstruct the rendered artifact tree from `haptic validate --dump-rendered`.

Writes haproxy.cfg + maps/ + general/ + ssl/ into out/<kind>-<n>/ with the
validate temp-dir prefix rewritten to that directory, so `haproxy -dr -c` can be
run against it, and prints a size summary.
"""
import json
import os
import re
import sys

SEP1 = "-" * 80
SEP2 = "=" * 80

SECTION_DIR = {
    "Map Files": "maps",
    "General Files": "general",
    "SSL Certificates": "ssl",
    "Kubernetes Resources": None,   # not files HAProxy reads
    "Status Patches": None,
}


def parse(dump_text):
    """Return (haproxy_cfg, {section: {name: content}})."""
    start = dump_text.find("\n" + SEP2 + "\nRENDERED CONTENT\n" + SEP2)
    if start < 0:
        raise SystemExit("no RENDERED CONTENT block in dump")
    lines = dump_text[start:].split("\n")

    cfg = None
    sections = {}
    cur_section = None
    cur_name = None
    i = 0
    while i < len(lines):
        line = lines[i]
        if line == "### haproxy.cfg":
            assert lines[i + 1] == SEP1
            body, i = read_until_sep(lines, i + 2)
            cfg = body
            continue
        if line.startswith("### "):
            cur_section = line[4:]
            sections.setdefault(cur_section, {})
            i += 1
            continue
        if line.startswith("#### "):
            cur_name = line[5:]
            assert lines[i + 1] == SEP1, lines[i + 1]
            body, i = read_until_sep(lines, i + 2)
            sections[cur_section][cur_name] = body
            continue
        i += 1
    return cfg, sections


def read_until_sep(lines, i):
    buf = []
    while i < len(lines) and lines[i] != SEP1:
        buf.append(lines[i])
        i += 1
    # The dumper adds a trailing newline via Println before the separator.
    if buf and buf[-1] == "":
        buf.pop()
    return "\n".join(buf) + "\n", i + 1


def main():
    dump_path, out_dir = sys.argv[1], sys.argv[2]
    text = open(dump_path, encoding="utf-8", errors="replace").read()
    cfg, sections = parse(text)

    m = re.search(r"/tmp/haproxy-validate-\d+/worker-\d+/test-\d+", cfg)
    if not m:
        raise SystemExit("could not find validate temp path in config")
    old_prefix = m.group(0)

    os.makedirs(out_dir, exist_ok=True)
    cfg = cfg.replace(old_prefix, os.path.abspath(out_dir))

    stats = {"maps": {}, "general": {}, "ssl": {}}
    for section, items in sections.items():
        sub = SECTION_DIR.get(section)
        if sub is None:
            continue
        d = os.path.join(out_dir, sub)
        os.makedirs(d, exist_ok=True)
        for name, content in items.items():
            content = content.replace(old_prefix, os.path.abspath(out_dir))
            with open(os.path.join(d, name), "w", encoding="utf-8") as fh:
                fh.write(content)
            stats[sub][name] = {
                "bytes": len(content.encode()),
                "lines": content.count("\n"),
            }

    cfg_path = os.path.join(out_dir, "haproxy.cfg")
    with open(cfg_path, "w", encoding="utf-8") as fh:
        fh.write(cfg)

    summary = {
        "out_dir": os.path.abspath(out_dir),
        "cfg_bytes": len(cfg.encode()),
        "cfg_lines": cfg.count("\n"),
        "map_count": len(stats["maps"]),
        "map_bytes": sum(v["bytes"] for v in stats["maps"].values()),
        "map_lines": sum(v["lines"] for v in stats["maps"].values()),
        "general_count": len(stats["general"]),
        "general_bytes": sum(v["bytes"] for v in stats["general"].values()),
        "top_maps": sorted(
            ((v["bytes"], k) for k, v in stats["maps"].items()), reverse=True
        )[:6],
    }
    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()
