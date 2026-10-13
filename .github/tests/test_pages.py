import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from scripts.ci.github_pages import inventory, prepare, record, worker_transport


ROOT = Path(__file__).parents[2]


class PagesTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.source = self.root / "source"
        self.output = self.root / "output"
        self.bundle = self.source / "playground/dev"
        self.bundle.mkdir(parents=True)
        self.engine = b"\x00asm" + b"original engine" * 16
        for suffix, value in (("", self.engine), (".gz", b"gzip copy"), (".br", b"brotli copy")):
            (self.bundle / ("playground.wasm" + suffix)).write_bytes(value)
        (self.bundle / "playground.worker.js").write_bytes((ROOT / "cmd/playground/web/playground.worker.js").read_bytes())
        (self.bundle / "index.html").write_text("<script>new Worker('./playground.worker.js')</script>")
        (self.bundle / "release-identity.json").write_text('{"commit": "original"}')
        self.decode = patch("scripts.ci.github_pages.subprocess.run", side_effect=self.decoded)
        self.decode.start()
        self.addCleanup(self.decode.stop)

    def decoded(self, command, *, stdout, check):
        self.assertEqual(command[:3], ["brotli", "--decompress", "--stdout"])
        self.assertTrue(check)
        stdout.write(self.engine)

    def test_preserves_site_and_engine_identity_while_replacing_transport(self):
        before = inventory(self.source)
        result = prepare(self.source, self.output)
        self.assertEqual(result["engine_versions"], 1)
        self.assertEqual(inventory(self.source), before)
        self.assertFalse((self.output / "playground/dev/playground.wasm").exists())
        self.assertFalse((self.output / "playground/dev/playground.wasm.gz").exists())
        self.assertEqual(record(self.output / "playground/dev/playground.wasm.br"), before["playground/dev/playground.wasm.br"])
        self.assertEqual(record(self.output / "playground/dev/release-identity.json"), before["playground/dev/release-identity.json"])
        receipt = json.loads((self.output / "shared/pages-transport.json").read_text())
        self.assertEqual(receipt["engines"]["playground/dev/playground.wasm"], before["playground/dev/playground.wasm"])
        self.assertIn("?transport=", (self.output / "playground/dev/index.html").read_text())

    def test_corrupt_brotli_and_unknown_worker_leave_no_output(self):
        self.engine = b"wrong decoded content"
        with self.assertRaisesRegex(ValueError, "differs from its original"):
            prepare(self.source, self.output)
        self.assertFalse(self.output.exists())
        with self.assertRaisesRegex(ValueError, "Unknown playground worker"):
            worker_transport("unexpected implementation", {})

    def test_size_limit_and_existing_destination_are_enforced(self):
        with self.assertRaisesRegex(ValueError, "limit is 1"):
            prepare(self.source, self.output, limit=1)
        self.assertFalse(self.output.exists())
        self.output.mkdir()
        (self.output / "keep").write_text("owned by another operation")
        with self.assertRaisesRegex(ValueError, "new directory"):
            prepare(self.source, self.output)
        self.assertEqual((self.output / "keep").read_text(), "owned by another operation")

    def test_local_aliases_are_materialized_and_escaping_links_are_rejected(self):
        docs = self.source / "docs/1.0.0"
        docs.mkdir(parents=True)
        (docs / "index.html").write_text("released docs")
        (docs.parent / "latest").symlink_to("1.0.0")
        prepare(self.source, self.output)
        self.assertFalse((self.output / "docs/latest").is_symlink())
        self.assertEqual((self.output / "docs/latest/index.html").read_text(), "released docs")
        (self.source / "escape").symlink_to(self.output)
        with self.assertRaisesRegex(ValueError, "escapes its root"):
            inventory(self.source)

    def test_legacy_documentation_redirects_keep_deep_links_and_assets(self):
        docs = self.source / "docs/latest"
        docs.mkdir(parents=True)
        (docs / "index.html").write_text("released docs")
        (docs / "guide.html").write_text("guide")
        (docs / "style.css").write_text("body{}")
        (self.source / "_redirects").write_text("/controller/latest/* /docs/latest/:splat 301\n/controller/ /docs/ 301\n")
        prepare(self.source, self.output)
        self.assertIn('/docs/latest/guide.html', (self.output / "controller/latest/guide.html").read_text())
        self.assertIn('location.replace("../../docs/latest/guide.html"',
                      (self.output / "controller/latest/guide.html").read_text())
        self.assertEqual((self.output / "controller/latest/style.css").read_text(), "body{}")
        self.assertFalse((self.output / "_redirects").exists())

    def test_unknown_redirects_are_not_silently_discarded(self):
        (self.source / "_redirects").write_text("/old /new 302\n")
        with self.assertRaisesRegex(ValueError, "Unsupported Pages redirect"):
            prepare(self.source, self.output)
        self.assertFalse(self.output.exists())
