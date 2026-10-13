import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

from scripts.ci.github_pages import inventory, record
from scripts.ci.github_site_archive import archive, download, restore


class SiteArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "source"
        self.source.mkdir()
        (self.source / "index.html").write_text("landing")
        (self.source / "docs").mkdir()
        (self.source / "docs/version.html").write_text("released documentation")
        (self.source / "docs/latest.html").symlink_to("version.html")
        self.bundle = self.root / "site.tar.gz"
        self.output = self.root / "restored"

    def test_roundtrip_preserves_every_logical_file_and_provenance(self):
        wanted = inventory(self.source)
        expected = archive(self.source, self.bundle, {"run": 42})
        result = restore(self.bundle, self.output, expected)
        self.assertEqual(inventory(self.output), wanted)
        self.assertEqual(result["provenance"], {"run": 42})
        self.assertFalse((self.output / "docs/latest.html").is_symlink())
        self.assertEqual(expected["files"], len(wanted))

    def test_truncated_or_changed_archive_never_replaces_output(self):
        expected = archive(self.source, self.bundle, {})
        self.bundle.write_bytes(self.bundle.read_bytes()[:-10])
        with self.assertRaisesRegex(ValueError, "pinned baseline"):
            restore(self.bundle, self.output, expected)
        self.assertFalse(self.output.exists())

    def test_escaping_site_links_and_existing_outputs_are_rejected(self):
        expected = archive(self.source, self.bundle, {})
        self.output.mkdir()
        (self.output / "keep").write_text("existing site")
        with self.assertRaisesRegex(ValueError, "new output"):
            restore(self.bundle, self.output, expected)
        self.assertEqual((self.output / "keep").read_text(), "existing site")
        (self.source / "escape").symlink_to(self.bundle)
        with self.assertRaisesRegex(ValueError, "escapes its root"):
            archive(self.source, self.root / "other.tar.gz", {})

    def test_path_traversal_links_duplicates_and_incomplete_inventories_are_rejected(self):
        for name, kind in (("public/../../escaped", tarfile.REGTYPE),
                           ("public/link", tarfile.SYMTYPE),
                           ("manifest.json", tarfile.REGTYPE),
                           ("public/unrecorded", tarfile.REGTYPE)):
            with self.subTest(name=name):
                with tarfile.open(self.bundle, "w:gz") as bundle:
                    data = json.dumps({"version": 1, "files": {"index.html": {"size": 1, "sha256": "unknown"}}}).encode()
                    member = tarfile.TarInfo("manifest.json")
                    member.size = len(data)
                    bundle.addfile(member, io.BytesIO(data))
                    member = tarfile.TarInfo(name)
                    member.type = kind
                    member.linkname = "../../escaped"
                    bundle.addfile(member, io.BytesIO())
                with self.assertRaises(ValueError):
                    restore(self.bundle, self.output, record(self.bundle))
                self.assertFalse(self.output.exists())
                self.assertFalse((self.root / "escaped").exists())

    def test_baseline_download_requires_a_public_github_release(self):
        for url in ("file:///private", "https://other.example/releases/download/site", "https://github.com/source"):
            with self.subTest(url=url), self.assertRaisesRegex(ValueError, "public GitHub release"):
                download({"url": url}, self.output)


if __name__ == "__main__":
    unittest.main()
