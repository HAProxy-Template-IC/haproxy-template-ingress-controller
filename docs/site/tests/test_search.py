"""Check search audience filtering through the generated Material index."""

import json
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parents[3]


class UserSearchTest(unittest.TestCase):
    def test_development_pages_remain_readable_but_leave_user_search(self):
        with tempfile.TemporaryDirectory(prefix="haptic-user-search-") as temp:
            root = Path(temp)
            docs = root / "docs/site/docs"
            docs.mkdir(parents=True)
            pages = {
                "index.md": "# Install HAPTIC\n\nUser installation steps.\n",
                "development/design.md": "# Internal design\n\nDeveloper steps.\n",
                "libraries/internals.md": "# Library internals\n\nInternal steps.\n",
            }
            for name, content in pages.items():
                page = docs / name
                page.parent.mkdir(parents=True, exist_ok=True)
                page.write_text(content, encoding="utf-8")
            adrs = root / "docs/adr"
            adrs.mkdir()
            (adrs / "0001-design.md").write_text(
                "# Design decision\n\nArchitecture steps.\n", encoding="utf-8"
            )
            config = docs.parent / "mkdocs.yml"
            config.write_text(
                "site_name: Search fixture\n"
                "site_url: https://example.com/docs/dev/\n"
                "theme:\n  name: material\n"
                "plugins:\n  - search\n"
                "nav:\n"
                "  - Get started: index.md\n"
                "  - Development:\n"
                "    - Design: development/design.md\n"
                "    - Libraries:\n"
                "      - Internals: libraries/internals.md\n"
                "hooks:\n"
                f"  - {REPO / 'docs/site/hooks/adrs.py'}\n"
                f"  - {REPO / 'docs/site/hooks/search.py'}\n",
                encoding="utf-8",
            )
            site = root / "output"
            subprocess.run(
                ["mkdocs", "build", "--strict", "--config-file", str(config),
                 "--site-dir", str(site)],
                check=True, capture_output=True, text=True,
            )
            index = json.loads((site / "search/search_index.json").read_text())
            self.assertTrue(index["docs"])
            self.assertEqual({entry["title"] for entry in index["docs"]}, {"Install HAPTIC"})
            for path, title in [
                ("development/design", "Internal design"),
                ("libraries/internals", "Library internals"),
                ("development/adr/0001-design", "Design decision"),
            ]:
                with self.subTest(page=path):
                    self.assertIn(title, (site / path / "index.html").read_text())
                    self.assertIn(f"{path}/", (site / "index.html").read_text())


if __name__ == "__main__":
    unittest.main()
