"""Keep chart validation complete without making prose changes run the matrix."""

import fnmatch
import re
import subprocess
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
CHART_DOCS = {
    "charts/CLAUDE.md",
    "charts/haptic/README.md",
    "charts/haptic/CHANGELOG.md",
}


class Reference(list):
    pass


class Loader(yaml.SafeLoader):
    pass


Loader.add_constructor("!reference", lambda loader, node: Reference(loader.construct_sequence(node)))


def expand(value, config):
    if isinstance(value, Reference):
        target = config
        for key in value:
            target = target[key]
        return expand(target, config)
    if isinstance(value, list):
        result = []
        for entry in value:
            expanded = expand(entry, config)
            result.extend(expanded if isinstance(expanded, list) else [expanded])
        return result
    return value


def rules(config, name):
    job = config[name]
    if "rules" in job:
        return expand(job["rules"], config)
    parents = job.get("extends", [])
    if isinstance(parents, str):
        parents = [parents]
    for parent in reversed(parents):
        inherited = rules(config, parent)
        if inherited:
            return inherited
    return []


def glob_matches(pattern, path):
    parts, names = pattern.split("/"), path.split("/")
    if not parts or not names:
        return parts == names
    if parts[0] == "**":
        return any(glob_matches("/".join(parts[1:]), "/".join(names[index:]))
                   for index in range(len(names) + 1)) if len(parts) > 1 else True
    return fnmatch.fnmatchcase(names[0], parts[0]) and (
        len(parts) == len(names) == 1 or
        len(parts) > 1 and len(names) > 1 and glob_matches("/".join(parts[1:]), "/".join(names[1:])))


def selects(config, name, path):
    for rule in rules(config, name):
        changes = rule.get("changes", [])
        if isinstance(changes, dict):
            # Ruby and Python share this regexp subset; Python uses \Z for Ruby's \z.
            if re.search(changes["regexp"].replace(r"\z", r"\Z"), path):
                return True
        elif any(glob_matches(pattern, path) for pattern in changes):
            return True
    return False


class ChartRulesTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = yaml.load((ROOT / ".gitlab-ci.yml").read_text(), Loader=Loader)
        cls.chart_files = subprocess.check_output(
            ["git", "ls-files", "-z", "charts"], cwd=ROOT, text=True).rstrip("\0").split("\0")

    def test_every_chart_input_keeps_its_validators_and_image_build(self):
        for path in set(self.chart_files) - CHART_DOCS:
            for job in ("chart-test", "chart-test-minimum-kubernetes", "validate-helm-libraries",
                        "build-snapshot", "build-playground-wasm", "test-acceptance",
                        "test-gitops-lifecycle"):
                with self.subTest(path=path, job=job):
                    self.assertTrue(selects(self.config, job, path))

    def test_chart_prose_does_not_select_expensive_jobs(self):
        for path in CHART_DOCS | {"docs/site/docs/operations/gitops.md"}:
            for job in ("chart-test", "chart-test-minimum-kubernetes", "validate-helm-libraries",
                        "build-snapshot", "build-playground-wasm", "test-acceptance",
                        "test-chart-upgrade", "test-install-without-gateway-api",
                        "test-gitops-lifecycle", ".rules-spoa-chart"):
                with self.subTest(path=path, job=job):
                    self.assertFalse(selects(self.config, job, path))

    def test_unknown_extensions_and_markdown_template_inputs_remain_covered(self):
        for path in ("charts/haptic/.helmignore", "charts/haptic/files/config.newtype",
                     "charts/haptic/templates/README.md", "charts/haptic/files/config.md",
                     "charts/new-chart/template", "charts/haptic/README.md\nvalues.yaml"):
            with self.subTest(path=path):
                self.assertTrue(selects(self.config, ".rules-chart-inputs", path))

    def test_documentation_still_gets_a_strict_build_and_lint(self):
        for path in CHART_DOCS | {"README.md", "docs/site/docs/operations/gitops.md"}:
            with self.subTest(path=path):
                self.assertTrue(selects(self.config, "pages-preview", path))
        commands = self.config["pages-preview"]["script"]
        for config in ("site", "landing"):
            self.assertTrue(any(f"--config-file docs/{config}/mkdocs.yml" in command
                                and "--strict" in command for command in commands))
        self.assertEqual(rules(self.config, "lint")[-1], {"when": "on_success"})

    def test_main_publication_keeps_its_exact_playground_bundle(self):
        default_branch = {"if": "$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH"}
        self.assertIn(default_branch, rules(self.config, "build-playground-wasm"))
        self.assertIn(default_branch, rules(self.config, "trigger-pages"))


if __name__ == "__main__":
    unittest.main()
