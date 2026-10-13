"""Archive and restore a complete website with a verified file inventory."""

import argparse
import gzip
import io
import json
from pathlib import Path, PurePosixPath
import shutil
import tarfile
import tempfile
import urllib.parse
import urllib.request

from scripts.ci.github_pages import inventory, record


def archive(source, destination, provenance):
    if destination.exists() or destination.resolve().is_relative_to(source.resolve()):
        raise ValueError("Website archive must be a new file outside the source site")
    files = inventory(source)
    if not files or "index.html" not in files:
        raise ValueError("Website archive requires a complete site")
    manifest = {"version": 1, "files": files, "provenance": provenance}
    encoded = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode()
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=destination.parent, prefix=".site-archive-") as temporary:
        output = Path(temporary) / "site.tar.gz"
        with output.open("wb") as raw, gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0, compresslevel=6) as compressed:
            with tarfile.open(fileobj=compressed, mode="w|") as bundle:
                member = tarfile.TarInfo("manifest.json")
                member.size = len(encoded)
                member.mode = 0o644
                bundle.addfile(member, io.BytesIO(encoded))
                for name, expected in files.items():
                    member = tarfile.TarInfo("public/" + name)
                    member.size = expected["size"]
                    member.mode = 0o644
                    with (source / name).open("rb") as content:
                        bundle.addfile(member, content)
        if inventory(source) != files:
            raise ValueError("Website changed while its archive was being created")
        output.rename(destination)
    return {**record(destination), "files": len(files), "site_bytes": sum(f["size"] for f in files.values())}


def restore(bundle, destination, expected):
    if destination.exists():
        raise ValueError("Website restore requires a new output directory")
    if record(bundle) != {key: expected[key] for key in ("size", "sha256")}:
        raise ValueError("Website archive checksum or size differs from its pinned baseline")
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=destination.parent, prefix=".site-restore-") as temporary:
        output = Path(temporary) / "public"
        output.mkdir()
        names = set()
        manifest = None
        with tarfile.open(bundle, "r:gz") as source:
            for member in source:
                name = PurePosixPath(member.name)
                if (not member.isfile() or member.name in names or name.is_absolute() or
                        ".." in name.parts or member.name != name.as_posix() or
                        (member.name != "manifest.json" and (not name.parts or name.parts[0] != "public"))):
                    raise ValueError("Website archive contains an invalid or duplicate file")
                names.add(member.name)
                with source.extractfile(member) as content:
                    if member.name == "manifest.json":
                        manifest = json.load(content)
                    else:
                        path = output.joinpath(*name.parts[1:])
                        path.parent.mkdir(parents=True, exist_ok=True)
                        with path.open("xb") as target:
                            shutil.copyfileobj(content, target)
        if (not manifest or manifest.get("version") != 1 or not manifest.get("files") or
                "index.html" not in manifest["files"] or inventory(output) != manifest["files"]):
            raise ValueError("Website archive does not match its complete file inventory")
        output.rename(destination)
    return manifest


def download(config, destination):
    url = urllib.parse.urlparse(config["url"])
    if url.scheme != "https" or url.netloc != "github.com" or "/releases/download/" not in url.path:
        raise ValueError("Website baseline must come from a public GitHub release")
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=destination.parent, prefix=".site-download-") as temporary:
        bundle = Path(temporary) / "site.tar.gz"
        with urllib.request.urlopen(config["url"], timeout=120) as source, bundle.open("wb") as target:
            shutil.copyfileobj(source, target)
        return restore(bundle, destination, config)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    create = commands.add_parser("create")
    create.add_argument("--source", type=Path, required=True)
    create.add_argument("--archive", type=Path, required=True)
    create.add_argument("--provenance", type=Path, required=True)
    extract = commands.add_parser("restore")
    extract.add_argument("--config", type=Path, required=True)
    extract.add_argument("--output", type=Path, required=True)
    extract.add_argument("--archive", type=Path)
    args = parser.parse_args()
    if args.command == "create":
        print(json.dumps(archive(args.source, args.archive, json.loads(args.provenance.read_text())), indent=2))
    else:
        config = json.loads(args.config.read_text())
        result = restore(args.archive, args.output, config) if args.archive else download(config, args.output)
        print(f"Restored {len(result['files'])} verified website files")


if __name__ == "__main__":
    main()
