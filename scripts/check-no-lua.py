#!/usr/bin/env python3

import hashlib
from pathlib import Path
import re
import subprocess
import sys


# Existing upstream compatibility fixtures, not HAPTIC feature implementations.
COMPATIBILITY_FIXTURES = {
    "tests/integration/testdata/mailers/mailers.lua":
        "62add896a12d11fb79e61dbe3dee793a0f48ac93476b121ca9fa9198e799131d",
    "tests/integration/testdata/mailers/mailers-legacy.lua":
        "401e0d82719912ff4d4bd0ecf2e9a83bfad6c076b24554dde5b7405e3a52dc6b",
    "tests/integration/testdata/mailers/mailers-with-alerts-lua.cfg":
        "15e8fa9097442fbc2544e8224e4aaa4876d79e192d1a5967f700835c2a0cf166",
}
POLICY_CHECKS = {"scripts/check-no-lua.py", "scripts/tests/test_check_no_lua.py"}
RUNTIME_HOOK = re.compile(
    r"(?<![\w/.-])(?:lua[-_]load\b|lua[-_]prepend[-_]path\b|"
    r"lua\.[a-z_]|tune\.lua\b|core\.register_(?:action|fetches|converters|service|task|init)\s*\(|"
    r"USE_LUA\s*[:?+]?=)", re.IGNORECASE)


def violations(name, content):
    if name in COMPATIBILITY_FIXTURES:
        if hashlib.sha256(content).hexdigest() != COMPATIBILITY_FIXTURES[name]:
            return [f"{name}: existing Lua compatibility fixture changed"]
        return []
    if name in POLICY_CHECKS:
        return []
    if Path(name).suffix.lower() in {".lua", ".luac"} or content.startswith((b"\x1bLua", b"\x1bLJ")):
        return [f"{name}: Lua script or bytecode"]
    return [f"{name}:{line_number}: Lua runtime hook"
            for line_number, line in enumerate(content.decode("utf-8", errors="replace").splitlines(), 1)
            if RUNTIME_HOOK.search(line)]


def check_repository(root):
    paths = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=root)
    errors = []
    for name in sorted(set(paths.decode().split("\0")) - {""}):
        path = root / name
        if path.is_file():
            errors.extend(violations(name, path.read_bytes()))
    return errors


def main():
    errors = check_repository(Path(__file__).resolve().parents[1])
    if errors:
        print("\n".join(errors), file=sys.stderr)
        print("Lua-based HAPTIC features are prohibited. Replace Lua with a native implementation; "
              "see CLAUDE.md rule #4.", file=sys.stderr)
        return 1
    print("No Lua feature scripts or runtime hooks found outside pinned compatibility fixtures.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
