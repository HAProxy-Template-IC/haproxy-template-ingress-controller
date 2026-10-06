#!/usr/bin/env python3
"""Advance current command pins without changing historical upgrade paths."""

from pathlib import Path
import re
import sys


HEADING = re.compile(r"^ {0,3}(#{1,6})\s+(.+?)\s*#*\s*$")
UPGRADE = re.compile(r"Upgrading to (\d+\.\d+(?:\.\d+)?(?:-[a-z]+\.\d+)?)$")
FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})(.*)$")


def update_document(content, previous, version, haproxy):
    pins = re.compile(rf"(--version |--expect-chart-version ){re.escape(previous)}\b")
    image = re.compile(rf"(haptic:){re.escape(previous)}-haproxy[0-9.]*")
    sections = []
    fence = None
    updated = []
    for line in content.splitlines(keepends=True):
        delimiter = FENCE.match(line)
        if fence is not None:
            if (delimiter and delimiter[1][0] == fence[0]
                    and len(delimiter[1]) >= len(fence) and not delimiter[2].strip()):
                fence = None
        elif delimiter:
            fence = delimiter[1]
        elif heading := HEADING.match(line):
            depth = len(heading[1])
            while sections and sections[-1][0] >= depth:
                sections.pop()
            if upgrade := UPGRADE.fullmatch(heading[2]):
                sections.append((depth, upgrade[1]))
        if all(version == target or version.startswith((target + ".", target + "-"))
               for _, target in sections):
            line = pins.sub(lambda match: match[1] + version, line)
            line = image.sub(lambda match: match[1] + version + "-haproxy" + haproxy, line)
        updated.append(line)
    return "".join(updated)


if __name__ == "__main__":
    previous, version, haproxy, *files = sys.argv[1:]
    for filename in files:
        path = Path(filename)
        path.write_text(update_document(path.read_text(), previous, version, haproxy))
