"""Keep the user search index focused on user documentation."""

_development_pages = set()


def _pages(items):
    for item in items:
        if isinstance(item, str):
            yield item
        elif isinstance(item, dict):
            for value in item.values():
                yield from _pages(value if isinstance(value, list) else [value])


def on_config(config):
    _development_pages.clear()
    for section in config["nav"]:
        if isinstance(section, dict) and "Development" in section:
            _development_pages.update(_pages(section["Development"]))


def on_page_markdown(markdown, page, config, files):
    if page.file.src_uri in _development_pages:
        page.meta.setdefault("search", {})["exclude"] = True
    return markdown
