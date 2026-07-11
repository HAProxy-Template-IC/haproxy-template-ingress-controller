"""Build-time changelog sync.

The docs-site changelog page is generated from the repo-root CHANGELOG.md on
every mkdocs build, so /docs/dev/ (built from main) always shows the current
[Unreleased] section instead of drifting until the next release. Versioned
builds run from the release tag's checkout, where CHANGELOG.md is the released
one — so they stay correct too.

The full CHANGELOG.md (title, intro, releases) is used as-is; only the mkdocs
front matter is prepended, and repo-relative links — which don't resolve on
the docs site — are rewritten to GitLab source URLs.
"""

from pathlib import Path

FRONT_MATTER = """\
---
hide:
  - navigation
---

"""

BLOB = "https://gitlab.com/haproxy-haptic/haptic/-/blob/main/"


def on_page_read_source(page, config):
    if page.file.src_uri != "changelog.md":
        return None
    root = Path(config["docs_dir"]).resolve().parents[2]
    changelog = (root / "CHANGELOG.md").read_text(encoding="utf-8")
    changelog = changelog.replace("](./docs/", "](" + BLOB + "docs/")
    changelog = changelog.replace("](./charts/", "](" + BLOB + "charts/")
    return FRONT_MATTER + changelog
