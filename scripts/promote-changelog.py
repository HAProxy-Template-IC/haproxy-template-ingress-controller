#!/usr/bin/env python3
"""Promote CHANGELOG.md [Unreleased] to a release; a final release absorbs its pre-releases."""

from itertools import takewhile
from pathlib import Path
import re
import sys


UNRELEASED = "## [Unreleased]"
RELEASE = re.compile(r"## \[([^\]]+)\]")
BULLET = re.compile(r" *[-*+] ")
ORDER = ("Added", "Changed", "Deprecated", "Removed", "Fixed", "Security")
LAST_SUBSECTION = "Helm chart"


class ChangelogError(Exception):
    pass


def indent(line):
    return len(line) - len(line.lstrip(" "))


def trim(lines):
    lines = list(lines)
    while lines and not lines[0].strip():
        lines.pop(0)
    while lines and not lines[-1].strip():
        lines.pop()
    return lines


def parse_items(lines):
    """Parse a Markdown list into (head lines, child items) pairs."""
    blocks, base, blank = [], None, False
    for line in lines:
        if not line.strip():
            blank = True
            if blocks:
                blocks[-1].append(line)
            continue
        if base is None:
            base = indent(line)
        if not blocks or (indent(line) <= base and (blank or BULLET.match(line))):
            blocks.append([line])
        else:
            blocks[-1].append(line)
        blank = False
    items = []
    for block in blocks:
        nested = next((i for i, line in enumerate(block) if i and BULLET.match(line)
                       and indent(line) > indent(block[0])), len(block))
        items.append((tuple(trim(block[:nested])), parse_items(block[nested:])))
    return items


def merge_items(into, items):
    for head, children in items:
        match = next((item for item in into if item[0] == head), None)
        if match is None:
            match = (head, [])
            into.append(match)
        merge_items(match[1], children)


def render_items(items):
    for head, children in items:
        yield from head
        yield from render_items(children)


class Section:
    def __init__(self):
        self.items = []
        self.subsections = {}

    def absorb(self, lines, level):
        prefix = "#" * level + " "
        body, parts = [], []
        target = body
        for line in lines:
            if line.startswith(prefix):
                target = []
                parts.append((line[len(prefix):].strip(), target))
            else:
                target.append(line)
        merge_items(self.items, parse_items(body))
        for title, part in parts:
            self.subsections.setdefault(title, Section()).absorb(part, level + 1)

    def blocks(self, level):
        """Render as blank-line-separated blocks; empty subsections vanish."""
        blocks = [list(render_items(self.items))] if self.items else []
        titles = list(self.subsections)

        def rank(title):
            if title == LAST_SUBSECTION:
                return 2, 0
            return (0, ORDER.index(title)) if title in ORDER else (1, titles.index(title))

        for title in sorted(titles, key=rank):
            if sub := self.subsections[title].blocks(level + 1):
                blocks += [["#" * level + " " + title], *sub]
        return blocks


def fold(bodies, version, today):
    """Merge release bodies, newest first, into one section; entries keep release order."""
    merged, intro = Section(), []
    for body in reversed(bodies):
        at = next((i for i, line in enumerate(body) if line.startswith("### ")), len(body))
        intro = trim(body[:at]) or intro
        merged.absorb(body[at:], 3)
    blocks = [[f"## [{version}] - {today}"]] + ([intro] if intro else []) + merged.blocks(3)
    return "\n\n".join("\n".join(block) for block in blocks).split("\n")


def insert_heading(lines, start, version, today):
    promoted = lines[:start + 1] + ["", f"## [{version}] - {today}"] + lines[start + 1:]
    return "\n".join(promoted) + "\n", f"Promoting CHANGELOG.md [Unreleased] to [{version}]..."


def promote(text, version, today):
    """Return the promoted changelog and a progress message, or raise ChangelogError."""
    lines = text.splitlines()
    bounds = [i for i, line in enumerate(lines) if RELEASE.match(line)] + [len(lines)]
    names = [RELEASE.match(lines[start])[1] for start in bounds[:-1]]
    base, _, suffix = version.partition("-")
    ours = [name for name in names if name.startswith(base + "-")]
    if version in names:
        if suffix or not ours:
            return text, f"CHANGELOG.md already has a [{version}] section — skipping the [Unreleased] promotion"
        raise ChangelogError(f"CHANGELOG.md has both [{version}] and [{ours[0]}]. "
                             f"Merge them into [{version}] by hand, then re-run.")
    unreleased = next((k for k, start in enumerate(bounds[:-1]) if lines[start] == UNRELEASED), None)
    if unreleased is None:
        raise ChangelogError("CHANGELOG.md has no [Unreleased] section to promote")
    start = bounds[unreleased]
    if suffix:
        return insert_heading(lines, start, version, today)
    pending = list(takewhile(lambda name: "-" in name, names[unreleased + 1:]))
    if other := [name for name in pending if name not in ours]:
        raise ChangelogError(f"CHANGELOG.md: [{other[0]}] is unreleased and isn't a {version} pre-release, "
                             f"so {version} can't absorb it. Move its entries into [Unreleased] "
                             "and delete its heading, then re-run.")
    if stray := [name for name in ours if name not in pending]:
        raise ChangelogError(f"CHANGELOG.md: [{stray[0]}] is not adjacent to [Unreleased], so {version} "
                             "can't absorb it. Move it directly below [Unreleased], then re-run.")
    if not pending:
        return insert_heading(lines, start, version, today)
    folded = range(unreleased, unreleased + len(pending) + 1)
    end = bounds[folded[-1] + 1]
    rest = [""] + lines[end:] if end < len(lines) else []
    bodies = [lines[bounds[k] + 1:bounds[k + 1]] for k in folded]
    promoted = lines[:start] + [UNRELEASED, ""] + fold(bodies, version, today) + rest
    sections = ", ".join(f"[{names[k]}]" for k in folded)
    return "\n".join(promoted) + "\n", f"Folding CHANGELOG.md {sections} into [{version}]..."


def main(version, today, path):
    changelog = Path(path)
    try:
        promoted, message = promote(changelog.read_text(), version, today)
    except ChangelogError as err:
        sys.exit(f"Error: {err}")
    print(message)
    changelog.write_text(promoted)


if __name__ == "__main__":
    main(*sys.argv[1:])
