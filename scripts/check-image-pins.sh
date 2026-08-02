#!/usr/bin/env bash
#
# Every file that pins an image must pin the same version.
#
# The file set is renovate.json's own customManagers, so the gate cannot drift
# from the config that does the bumping: a file the bot updates is a file this
# checks, and adding one to a manager enrols it here automatically.
#
# Renovate updates every file it matches atomically, so a mismatch means a pin
# the bot never saw — a file missing from the manager, or added to it after the
# branch was cut. Both leave one copy behind, and only some of those copies are
# gated elsewhere.

set -euo pipefail

cd "$(dirname "$0")/.."

python3 - <<'PY'
import collections, json, re, subprocess, sys

with open("renovate.json", encoding="utf-8") as fh:
    managers = json.load(fh).get("customManagers", [])

# Tracked files only, which is exactly the set renovate can rewrite. Walking the
# tree instead needs a skip list, and a name-based one is quietly wrong twice
# over: it prunes docs/site (the source of the reference table this is most
# meant to gate) along with the mkdocs output, and it descends into
# .claude/worktrees, where a stale checkout fails the gate for a pin nobody ships.
repo_files = subprocess.run(
    ["git", "ls-files", "-z"], capture_output=True, check=True, text=True
).stdout.split("\0")

# Renovate regexes are JS-flavoured; Python spells named groups (?P<name>).
def to_python(pattern):
    return re.sub(r"\(\?<(?![=!])", "(?P<", pattern)

# depName -> version -> files. Grouped across ALL managers, not per manager:
# an image pinned by two managers (values.yaml and the docs table, say) is
# exactly the case that goes wrong, so per-manager grouping would miss it.
pins = collections.defaultdict(lambda: collections.defaultdict(set))

for manager in managers:
    dep = manager.get("depNameTemplate", "")
    # A templated depName expands to several real deps whose versions legitimately
    # differ (the per-series HAProxy pins); there is nothing to compare.
    if not dep or "{{" in dep:
        continue
    patterns = [re.compile(to_python(s)) for s in manager.get("matchStrings", [])]
    for file_match in manager.get("fileMatch", []):
        matcher = re.compile(file_match)
        for path in repo_files:
            if not matcher.search(path):
                continue
            try:
                with open(path, encoding="utf-8") as fh:
                    text = fh.read()
            except (OSError, UnicodeDecodeError):
                continue
            for pattern in patterns:
                for found in pattern.finditer(text):
                    version = found.groupdict().get("currentValue")
                    if version:
                        pins[dep][version].add(path)

failed = False
for dep in sorted(pins):
    versions = pins[dep]
    if len(versions) > 1:
        failed = True
        print(f"\nFAIL: {dep} is pinned to {len(versions)} different versions:")
        for version in sorted(versions):
            for path in sorted(versions[version]):
                print(f"  {version}\t{path}")
    else:
        version = next(iter(versions))
        print(f"OK: {dep} = {version} ({len(versions[version])} files agree)")

if failed:
    print(
        "\nBump every copy to the same version. If one of these files should not "
        "be tracked,\nremove it from the customManager in renovate.json rather "
        "than letting it drift."
    )
    sys.exit(1)
PY
