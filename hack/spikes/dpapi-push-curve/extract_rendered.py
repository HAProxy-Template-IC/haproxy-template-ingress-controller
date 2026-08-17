#!/usr/bin/env python3
"""Split `haptic validate --dump-rendered` text output into real files.

usage: extract_rendered.py DUMP_TXT OUTDIR
Writes OUTDIR/haproxy.cfg and OUTDIR/maps/<name>.
"""
import os
import sys

SEP1 = "-" * 80
SEP2 = "=" * 80


def main():
    dump, outdir = sys.argv[1], sys.argv[2]
    lines = open(dump).read().split("\n")
    # Jump to the RENDERED CONTENT block.
    try:
        start = next(i for i, l in enumerate(lines) if l.strip() == "RENDERED CONTENT")
    except StopIteration:
        sys.exit("no RENDERED CONTENT block — did the render fail?")
    i = start
    section = None       # "haproxy.cfg" | "Map Files" | ...
    name = None
    written = []
    os.makedirs(os.path.join(outdir, "maps"), exist_ok=True)
    os.makedirs(os.path.join(outdir, "general"), exist_ok=True)
    os.makedirs(os.path.join(outdir, "certs"), exist_ok=True)
    while i < len(lines):
        line = lines[i]
        if line.startswith("### "):
            section = line[4:].strip()
            name = "haproxy.cfg" if section == "haproxy.cfg" else None
        elif line.startswith("#### "):
            name = line[5:].strip()
        elif line == SEP1 and name is not None:
            body = []
            i += 1
            while i < len(lines) and lines[i] != SEP1:
                body.append(lines[i])
                i += 1
            if section == "haproxy.cfg":
                path = os.path.join(outdir, "haproxy.cfg")
            elif section == "Map Files":
                path = os.path.join(outdir, "maps", os.path.basename(name))
            elif section == "General Files":
                path = os.path.join(outdir, "general", os.path.basename(name))
            elif section == "SSL Certificates":
                path = os.path.join(outdir, "certs", os.path.basename(name))
            else:
                path = None
            if path:
                # The dumper prints content then an extra newline before SEP1.
                text = "\n".join(body)
                if text.endswith("\n"):
                    text = text[:-1]
                with open(path, "w") as f:
                    f.write(text + "\n")
                written.append(path)
            name = None if section != "haproxy.cfg" else None
        i += 1
    for p in written:
        print(f"{os.path.getsize(p):>10}  {p}")


if __name__ == "__main__":
    main()
