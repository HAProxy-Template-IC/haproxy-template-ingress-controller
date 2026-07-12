"""Redirect stubs for moved pages.

Pages get restructured; inbound links (search results, bookmarks, other
sites) keep pointing at the old URLs. This hook publishes, at build time,
an HTML stub at each old URL that forwards to the page's new home — the
same meta-refresh + canonical + fallback-link pattern mkdocs-redirects
emits.

Keys are old doc src paths (relative to ``docs_dir``); values are new URL
paths relative to the site root, optionally with a ``#fragment``.
"""

from pathlib import Path

REDIRECTS = {
    "configuration.md": "deploying-with-helm/",
    "operations/troubleshooting.md": "troubleshooting/#install-issues",
    "development/design/considerations.md": (
        "development/design/architecture-overview/"
        "#operating-assumptions-and-constraints"
    ),
    "development/design/appendices.md": "development/design/",
}

_STUB = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Redirecting...</title>
<link rel="canonical" href="{canonical}">
<meta name="robots" content="noindex">
<meta http-equiv="refresh" content="0; url={url}">
</head>
<body>
Redirecting to <a href="{url}">{url}</a>...
</body>
</html>
"""


def _old_url(src_path):
    """Old src path -> directory URL, the way mkdocs derives page URLs."""
    path = src_path[: -len(".md")]
    if path == "index" or path.endswith("/index"):
        path = path[: -len("index")].rstrip("/")
    return path + "/" if path else ""


def on_post_build(config):
    if not config["use_directory_urls"]:
        # _old_url mirrors directory-URL derivation; "page.html" layouts
        # would collide with real output files.
        raise RuntimeError("hooks/redirects.py requires use_directory_urls: true")
    site = Path(config["site_dir"])
    base = (config["site_url"] or "/").rstrip("/") + "/"
    for old_src, new_path in REDIRECTS.items():
        # A redirect to a missing page would ship a silent 404 — stubs are
        # written after mkdocs' own link checking, so verify the target here.
        target = site / new_path.split("#")[0]
        if not (target / "index.html").is_file():
            raise RuntimeError(
                f"redirect target missing for {old_src!r}: {new_path!r}"
            )
        url = base + new_path
        out = site / _old_url(old_src) / "index.html"
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(
            # Canonical URLs identify a document, not a fragment.
            _STUB.format(url=url, canonical=url.split("#")[0]),
            encoding="utf-8",
        )
