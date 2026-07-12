"""Publish the repo-root ADRs as site pages.

The Architecture Decision Records live in ``docs/adr/`` at the repo root —
next to the code they govern, where reviews and agents cite them. This hook
publishes each ``NNNN-*.md`` under ``development/adr/<NNNN-slug>/`` without
copying: every ADR is registered as a generated page whose source path points
at the real file (hooks may read outside ``docs_dir`` — hooks/changelog.py
set the precedent), so the repo file stays the single source of truth.

The hook also:

- builds a nav group under *Development* titled from each file's H1, so a new
  ADR appears on the site without touching ``mkdocs.yml``
- rewrites bare ``ADR-NNNN`` cross-references inside ADR pages into links
  (the ADRs reference each other as plain text, which resolves fine in a
  repo checkout but would be dead text on the site)
- points each page's edit button at the real ``docs/adr/`` path
"""

import re
from pathlib import Path

from mkdocs.structure.files import File

DEST_DIR = "development/adr"
NAV_GROUP = "Architecture Decision Records"
EDIT_URL = "https://gitlab.com/haproxy-haptic/haptic/-/edit/main/docs/adr/"


def _adr_paths(config):
    root = Path(config["docs_dir"]).resolve().parents[2]
    return sorted((root / "docs" / "adr").glob("[0-9][0-9][0-9][0-9]-*.md"))


def _title(path):
    """Nav title from the file's H1 (single source of truth for titles)."""
    h1 = path.read_text(encoding="utf-8").split("\n", 1)[0]
    return h1.lstrip("#").strip().replace("`", "")


def on_config(config):
    group = [{_title(p): f"{DEST_DIR}/{p.name}"} for p in _adr_paths(config)]
    if not group:
        raise RuntimeError("hooks/adrs.py: no ADRs found under docs/adr/")
    for item in config["nav"]:
        if isinstance(item, dict) and "Development" in item:
            item["Development"].append({NAV_GROUP: group})
            return config
    raise RuntimeError("hooks/adrs.py: no 'Development' section in nav")


def on_files(files, config):
    for path in _adr_paths(config):
        files.append(
            File.generated(
                config, f"{DEST_DIR}/{path.name}", abs_src_path=str(path)
            )
        )
    return files


def on_pre_page(page, config, files):
    # Generated files get no edit_uri; the real source has an editable URL.
    if page.file.src_uri.startswith(DEST_DIR + "/"):
        page.edit_url = EDIT_URL + Path(page.file.src_uri).name
    return page


def on_page_markdown(markdown, page, config, files):
    if not page.file.src_uri.startswith(DEST_DIR + "/"):
        return markdown
    names = {p.name[:4]: p.name for p in _adr_paths(config)}

    def link(match):
        name = names.get(match.group(1))
        return f"[ADR-{match.group(1)}]({name})" if name else match.group(0)

    out, fenced = [], False
    for line in markdown.splitlines(keepends=True):
        if line.lstrip().startswith("```"):
            fenced = not fenced
        if not fenced and not line.startswith("#"):
            # Rewrite only outside inline code spans (odd segments are code);
            # (?<!\[) leaves already-linked references alone.
            parts = line.split("`")
            for i in range(0, len(parts), 2):
                parts[i] = re.sub(r"(?<!\[)\bADR-(\d{4})\b", link, parts[i])
            line = "`".join(parts)
        out.append(line)
    return "".join(out)
