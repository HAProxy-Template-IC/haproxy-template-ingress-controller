"""Ensure a development preview preserves released docs and the playground."""

import hashlib
import json
from pathlib import Path
import re
import sys


def main():
    mode, directory = sys.argv[1:]
    root = Path(directory)
    manifest = Path(".pilot/docs-preview-preserved.json")
    if mode == "record":
        releases = [path for path in (root / "docs").iterdir()
                    if path.is_dir() and re.fullmatch(r"\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.\d+)?", path.name)]
        if not releases or not (root / "playground/dev/index.html").is_file():
            raise ValueError("Published preview base is missing released docs or the playground")
        paths = sorted({path.relative_to(root).as_posix()
                        for parent in releases + [root / "playground"]
                        for path in parent.rglob("*") if path.is_file()})
        expected = {}
        for path in paths:
            with (root / path).open("rb") as source:
                expected[path] = hashlib.file_digest(source, "sha256").hexdigest()
        manifest.write_text(json.dumps(expected, indent=2) + "\n")
    elif mode == "verify":
        expected = json.loads(manifest.read_text())
        if not expected:
            raise ValueError("Published preview inventory is empty")
        for path, digest in expected.items():
            with (root / path).open("rb") as source:
                if hashlib.file_digest(source, "sha256").hexdigest() != digest:
                    raise ValueError(f"Preview changed published file: {path}")
        print(f"Preserved {len(expected)} published documentation and playground files")
    else:
        raise ValueError(f"Unknown preservation mode: {mode}")


if __name__ == "__main__":
    main()
