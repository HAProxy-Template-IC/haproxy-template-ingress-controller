"""Exercise skill downloads through a real MkDocs build."""

import hashlib
import json
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path
from urllib.parse import urljoin
from zipfile import ZipFile


REPO = Path(__file__).resolve().parents[3]


class AgentSkillPackageTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix="haptic-skill-package-")
        cls.addClassCleanup(cls.temp.cleanup)
        cls.root = Path(cls.temp.name)
        cls.docs = cls.root / "docs" / "site" / "docs"
        cls.docs.mkdir(parents=True)
        shutil.copytree(REPO / "skills", cls.root / "skills")
        shutil.copyfile(REPO / "LICENSE", cls.root / "LICENSE")
        cls.config = cls.docs.parent / "mkdocs.yml"
        cls.config.write_text(
            "site_name: Skill fixture\n"
            "site_url: https://example.com/preview/docs/dev/\n"
            "extra:\n  agent_skill_discovery: true\n"
            "hooks:\n"
            f"  - {REPO / 'docs/site/hooks/agent_skills.py'}\n",
            encoding="utf-8",
        )
        (cls.docs / "index.md").write_text(
            "# Skill\n\n"
            "[Download](agent-skills/haptic.zip)\n\n"
            "[Instructions](agent-skills/haptic/SKILL.md)\n",
            encoding="utf-8",
        )
        cls.site = cls.root / "output"
        cls.build()

    @classmethod
    def build(cls):
        subprocess.run(
            ["mkdocs", "build", "--strict", "--config-file", str(cls.config),
             "--site-dir", str(cls.site)],
            check=True, capture_output=True, text=True,
        )

    def test_archive_and_raw_files_preserve_complete_skill(self):
        source = self.root / "skills" / "haptic"
        expected = {
            path.relative_to(source).as_posix(): path.read_bytes()
            for path in source.rglob("*") if path.is_file()
        }
        expected["LICENSE"] = (self.root / "LICENSE").read_bytes()
        with ZipFile(self.site / "agent-skills" / "haptic.zip") as archive:
            self.assertEqual(set(archive.namelist()), set(expected))
            for name, content in expected.items():
                with self.subTest(name=name):
                    self.assertEqual(archive.read(name), content)
                    self.assertEqual(
                        (self.site / "agent-skills" / "haptic" / name).read_bytes(), content
                    )

    def test_download_checksum_and_reproducibility(self):
        archive = self.site / "agent-skills" / "haptic.zip"
        original = archive.read_bytes()
        checksum = archive.with_suffix(".zip.sha256").read_text()
        self.assertEqual(
            checksum, f"{hashlib.sha256(original).hexdigest()}  haptic.zip\n"
        )
        self.build()
        self.assertEqual(archive.read_bytes(), original)

    def test_bundled_references_resolve_inside_download(self):
        root = self.site / "agent-skills" / "haptic"
        for document in root.rglob("*.md"):
            for target in re.findall(r"\]\(([^)]+)\)", document.read_text()):
                if "://" in target or target.startswith("#"):
                    continue
                path = (document.parent / target.split("#", 1)[0]).resolve()
                with self.subTest(document=document.name, target=target):
                    self.assertTrue(path.is_relative_to(root))
                    self.assertTrue(path.is_file())

    def test_discovery_resolves_archive_and_verifies_its_digest(self):
        index_path = ".well-known/agent-skills/index.json"
        index = json.loads((self.site / index_path).read_text())
        self.assertEqual(
            index["$schema"],
            "https://schemas.agentskills.io/discovery/0.2.0/schema.json",
        )
        self.assertEqual(len(index["skills"]), 1)
        skill = index["skills"][0]
        self.assertEqual(skill["name"], "haptic")
        self.assertEqual(skill["type"], "archive")
        self.assertTrue(0 < len(skill["description"]) <= 1024)
        for base in ["https://example.com/", "https://example.com/preview/"]:
            self.assertEqual(
                urljoin(base + index_path, skill["url"]),
                base + "agent-skills/haptic.zip",
            )
        archive = (self.site / "agent-skills/haptic.zip").read_bytes()
        self.assertEqual(skill["digest"], f"sha256:{hashlib.sha256(archive).hexdigest()}")

    def test_missing_entrypoint_fails_the_build(self):
        entrypoint = self.root / "skills" / "haptic" / "SKILL.md"
        content = entrypoint.read_bytes()
        entrypoint.unlink()
        try:
            with self.assertRaises(subprocess.CalledProcessError) as failure:
                self.build()
            self.assertIn("SKILL.md is missing", failure.exception.stderr)
        finally:
            entrypoint.write_bytes(content)


if __name__ == "__main__":
    unittest.main()
