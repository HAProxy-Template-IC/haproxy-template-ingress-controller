import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

from scripts.ci.github_pages import inventory
from scripts.ci.github_site_archive import archive, restore
from scripts.ci.github_site_versions import assemble, catalogs, extract, payload, version_key


class SiteVersionsTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.baseline = self.root / "baseline"
        self.docs = self.root / "docs-input"
        self.wasm = self.root / "wasm-input"
        self.identity = {"version": "dev", "commit": "a" * 40, "pipeline": "123", "job": "suite_docs"}
        self.provenance = {"version": "dev", "docs": self.identity,
                           "playground": {**self.identity, "job": "suite_wasm"}}
        self.write(self.baseline / "index.html", "baseline landing")
        for area in ("docs", "playground"):
            self.write(self.baseline / area / "1.0.0/index.html", "released content")
            self.write(self.baseline / area / "dev/index.html", "old dev")
            self.write(self.baseline / area / "latest/index.html", "old latest")
        self.write(self.docs / "index.html", "new landing")
        self.write(self.docs / "shared/script.js", "new shared script")
        self.write(self.docs / "docs/dev/index.html", "new docs")
        self.write(self.docs / "docs/dev/release-identity.json", json.dumps(self.identity))
        self.write(self.docs / "docs/1.0.0/index.html", "outdated preview history must never be used")
        self.write(self.wasm / "playground/dev/index.html", '<html data-version="dev">new playground</html>')
        self.write(self.wasm / "playground/dev/playground.wasm", "verified engine bytes")
        self.write(self.wasm / "playground/dev/release-identity.json", json.dumps(self.provenance["playground"]))

    def write(self, path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(value)

    def build_payload(self, version="dev", name="payload", playground=True):
        destination = self.root / name
        provenance = {**self.provenance, "version": version}
        payload(self.docs, self.wasm if playground else None, destination, provenance)
        return destination, provenance

    def test_development_payload_updates_only_current_content(self):
        publication = self.build_payload()
        before = inventory(self.baseline)
        output = self.root / "output"
        assemble(self.baseline, [publication], output)
        self.assertEqual((output / "docs/dev/index.html").read_text(), "new docs")
        self.assertEqual((output / "index.html").read_text(), "new landing")
        self.assertEqual((output / "shared/script.js").read_text(), "new shared script")
        self.assertEqual((output / "docs/1.0.0/index.html").read_text(), "released content")
        self.assertEqual(inventory(self.baseline), before)

    def test_multiple_archived_releases_survive_independent_rebuilds(self):
        releases = [self.build_payload(version, name=version) for version in ("1.1.0", "1.2.0-rc.1")]
        restored = []
        for content, provenance in releases:
            bundle = self.root / (content.name + ".tar.gz")
            expected = archive(content, bundle, provenance)
            target = self.root / (content.name + "-restored")
            manifest = restore(bundle, target, expected)
            restored.append((target, manifest["provenance"]))
        for iteration in range(2):
            output = self.root / f"rebuild-{iteration}"
            assemble(self.baseline, restored, output)
            for version in ("1.0.0", "1.1.0", "1.2.0-rc.1"):
                self.assertTrue((output / f"docs/{version}/index.html").is_file())
                self.assertTrue((output / f"playground/{version}/index.html").is_file())
            catalog = json.loads((output / "playground/versions.json").read_text())
            self.assertEqual(catalog["current"], "1.1.0")
            docs = json.loads((output / "docs/versions.json").read_text())
            self.assertEqual([v["version"] for v in docs if v["aliases"]], ["1.1.0"])
            self.assertEqual((output / "index.html").read_text(), "baseline landing")
        self.assertEqual(inventory(self.root / "rebuild-0"), inventory(self.root / "rebuild-1"))

    def test_release_stamping_preserves_engine_and_build_provenance(self):
        content, _ = self.build_payload("1.1.0")
        self.assertEqual((content / "playground/1.1.0/playground.wasm").read_bytes(),
                         (self.wasm / "playground/dev/playground.wasm").read_bytes())
        self.assertIn('data-version="1.1.0"', (content / "playground/1.1.0/index.html").read_text())
        self.assertEqual(json.loads((content / "playground/1.1.0/build-identity.json").read_text()),
                         self.provenance["playground"])
        self.assertEqual(json.loads((content / "docs/1.1.0/release-identity.json").read_text()),
                         {**self.identity, "version": "1.1.0"})

    def test_release_overwrite_and_duplicate_versions_fail_atomically(self):
        release = self.build_payload("1.0.0")
        with self.assertRaisesRegex(ValueError, "cannot be overwritten"):
            assemble(self.baseline, [release], self.root / "output")
        self.assertFalse((self.root / "output").exists())
        development = self.build_payload(name="development")
        with self.assertRaisesRegex(ValueError, "unique"):
            assemble(self.baseline, [development, development], self.root / "output")
        self.assertFalse((self.root / "output").exists())

    def test_mismatched_identity_and_missing_engine_fail(self):
        for key in ("version", "commit", "pipeline", "job"):
            with self.subTest(key=key):
                self.write(self.docs / "docs/dev/release-identity.json", json.dumps({**self.identity, key: "wrong"}))
                with self.assertRaisesRegex(ValueError, "identity differs"):
                    self.build_payload()
                self.assertFalse((self.root / "payload").exists())
        self.write(self.docs / "docs/dev/release-identity.json", json.dumps(self.identity))
        with self.assertRaisesRegex(ValueError, "verified playground"):
            self.build_payload("1.1.0", playground=False)

    def test_prose_only_payload_keeps_the_existing_engine(self):
        publication = self.build_payload(playground=False)
        output = self.root / "output"
        assemble(self.baseline, [publication], output)
        self.assertEqual((output / "playground/dev/index.html").read_text(), "old dev")
        self.assertEqual((output / "docs/dev/index.html").read_text(), "new docs")

    def test_older_release_does_not_rewind_latest_and_all_history_is_retained(self):
        self.write(self.baseline / "docs/2.0.0/index.html", "newer docs")
        self.write(self.baseline / "playground/2.0.0/index.html", "newer playground")
        output = self.root / "output"
        assemble(self.baseline, [self.build_payload("1.1.0")], output)
        self.assertEqual((output / "docs/latest/index.html").read_text(), "newer docs")
        self.assertEqual(json.loads((output / "playground/versions.json").read_text())["current"], "2.0.0")

    def test_stable_is_preferred_to_newer_prerelease_without_deleting_it(self):
        self.write(self.baseline / "docs/2.0.0-rc.1/index.html", "prerelease docs")
        self.write(self.baseline / "playground/2.0.0-rc.1/index.html", "prerelease playground")
        catalogs(self.baseline)
        self.assertEqual(json.loads((self.baseline / "playground/versions.json").read_text())["current"], "1.0.0")
        self.assertTrue((self.baseline / "playground/2.0.0-rc.1/index.html").exists())
        self.assertLess(version_key("1.0.0-rc.10"), version_key("1.0.0"))
        self.assertLess(version_key("1.0.0-rc.2"), version_key("1.0.0-rc.10"))

    def test_new_catalog_removes_only_legacy_aliases_for_redirect_regeneration(self):
        self.write(self.baseline / "controller/latest/assets.js", "stale alias")
        self.write(self.baseline / "controller/1.0.0/index.html", "historical URL")
        catalogs(self.baseline)
        self.assertFalse((self.baseline / "controller/latest").exists())
        self.assertEqual((self.baseline / "controller/1.0.0/index.html").read_text(), "historical URL")

    def test_unsafe_archives_and_expansion_limit_leave_no_output(self):
        for name, kind in (("../escape", tarfile.REGTYPE), ("/absolute", tarfile.REGTYPE),
                           ("link", tarfile.SYMTYPE), ("hardlink", tarfile.LNKTYPE),
                           ("file", tarfile.REGTYPE)):
            with self.subTest(name=name):
                bundle = self.root / "input.tar.gz"
                with tarfile.open(bundle, "w:gz") as stream:
                    member = tarfile.TarInfo(name)
                    member.type = kind
                    member.linkname = "../outside"
                    member.size = 2 if member.isfile() else 0
                    stream.addfile(member, io.BytesIO(b"xx"))
                with self.assertRaises(ValueError):
                    extract(bundle, self.root / "extracted", limit=1)
                self.assertFalse((self.root / "extracted").exists())

    def test_tar_roundtrip_and_duplicate_paths(self):
        bundle = self.root / "input.tar.gz"
        with tarfile.open(bundle, "w:gz") as stream:
            stream.add(self.docs, arcname=".")
        extract(bundle, self.root / "extracted")
        self.assertEqual(inventory(self.docs), inventory(self.root / "extracted"))
        with tarfile.open(bundle, "w:gz") as stream:
            stream.add(self.docs / "index.html", arcname="index.html")
            stream.add(self.docs / "index.html", arcname="./index.html")
        with self.assertRaisesRegex(ValueError, "duplicate"):
            extract(bundle, self.root / "duplicate")
        self.assertFalse((self.root / "duplicate").exists())


if __name__ == "__main__":
    unittest.main()
