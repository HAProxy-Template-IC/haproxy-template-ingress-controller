from pathlib import Path
import re
import unittest


ROOT = Path(__file__).parents[2]
# image_digest feeds release_is_missing, which reads an exact "not found" error.
PROBES = {("scripts/spoa-release-image.sh", 'docker buildx imagetools inspect "$1" --format')}


class RegistryRetryTests(unittest.TestCase):
    def test_registry_inspections_retry_transient_failures(self):
        for path in [*ROOT.glob("scripts/*.sh"), *ROOT.glob(".github/scripts/*.sh")]:
            relative = str(path.relative_to(ROOT))
            for line in path.read_text().splitlines():
                if "imagetools inspect" not in line or line.lstrip().startswith("#"):
                    continue
                with self.subTest(script=relative, line=line.strip()):
                    if any(relative == script and probe in line for script, probe in PROBES):
                        continue
                    self.assertRegex(line, r"retry\.sh docker buildx imagetools inspect")


if __name__ == "__main__":
    unittest.main()
