"""Publish the portable skill unchanged, with a reproducible ZIP download."""

import json
from hashlib import sha256
from io import BytesIO
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

import yaml
from mkdocs.structure.files import File, InclusionLevel


class SkillFile(File):
    def is_documentation_page(self):
        # SKILL.md and its references must remain installable Markdown.
        return False


def on_files(files, config):
    root = Path(config["docs_dir"]).resolve().parents[2]
    source = root / "skills" / "haptic"
    if not (source / "SKILL.md").is_file():
        raise RuntimeError("skills/haptic/SKILL.md is missing")
    contents = {
        path.relative_to(source).as_posix(): path.read_bytes()
        for path in sorted(source.rglob("*"))
        if path.is_file()
    }
    archive = BytesIO()
    with ZipFile(archive, "w", compression=ZIP_DEFLATED) as bundle:
        for name, content in sorted(contents.items()):
            files.append(SkillFile.generated(
                config, f"agent-skills/haptic/{name}", content=content
            ))
            entry = ZipInfo(name)
            entry.compress_type = ZIP_DEFLATED
            entry.create_system = 3
            entry.external_attr = 0o100644 << 16
            bundle.writestr(entry, content)
    content = archive.getvalue()
    digest = sha256(content).hexdigest()
    files.append(File.generated(config, "agent-skills/haptic.zip", content=content))
    files.append(File.generated(
        config, "agent-skills/haptic.zip.sha256",
        content=f"{digest}  haptic.zip\n",
    ))
    if config.get("extra", {}).get("agent_skill_discovery"):
        metadata = yaml.safe_load(contents["SKILL.md"].decode().split("---", 2)[1])
        index = {
            "$schema": "https://schemas.agentskills.io/discovery/0.2.0/schema.json",
            "skills": [{
                "name": metadata["name"],
                "description": metadata["description"],
                "type": "archive",
                "url": "../../agent-skills/haptic.zip",
                "digest": f"sha256:{digest}",
            }],
        }
        files.append(File.generated(
            config, ".well-known/agent-skills/index.json",
            content=json.dumps(index, indent=2) + "\n",
            inclusion=InclusionLevel.INCLUDED,
        ))
    return files
