import collections
import json
from pathlib import Path
import unittest

import yaml


ROOT = Path(__file__).parents[2]


class WorkflowInventoryTests(unittest.TestCase):
    def setUp(self):
        self.workflow = yaml.safe_load((ROOT / ".github/workflows/ci-speed-proof.yml").read_text())
        self.manifest = json.loads((ROOT / ".github/ci-verification.json").read_text())

    def test_gate_requires_every_declared_job_and_preserves_baseline_coverage(self):
        names = []
        for job in self.workflow["jobs"].values():
            matrix = job.get("strategy", {}).get("matrix")
            if matrix:
                names.extend(row["name"] for row in matrix["include"])
            else:
                names.append(job["name"])
        self.assertEqual(len(names), len(set(names)))
        self.assertEqual(set(names), set(self.manifest["required_jobs"]) | {"verified"})
        mapped = {name for group in self.manifest["gitlab_job_mapping"].values() for name in group}
        self.assertEqual(mapped, set(names))

    def test_coverage_has_every_profile_without_waiting_for_unrelated_suites(self):
        jobs = self.workflow["jobs"]
        self.assertEqual(set(jobs["coverage"]["needs"]), {"coverage-inputs", "coverage-seed"})
        rows = [row for group in ("coverage-inputs", "coverage-seed")
                for row in jobs[group]["strategy"]["matrix"]["include"]]
        self.assertEqual({row["name"] for row in rows}, {
            "coverage-rest", "coverage-renderer",
            "integration-3.0", "integration-3.1", "integration-3.2", "integration-3.3", "integration-3.4",
        })
        self.assertEqual(set(jobs["verified"]["needs"]), {
            "verification", "verification-short", "coverage-inputs", "coverage-seed", "coverage",
        })

    def test_long_suites_fit_initial_capacity_before_short_jobs_are_released(self):
        jobs = self.workflow["jobs"]
        initial = [row for job in jobs.values() if not job.get("needs")
                   for row in job.get("strategy", {}).get("matrix", {}).get("include", [])]
        self.assertLessEqual(len(initial), 60)
        initial_names = {row["name"] for row in initial}
        for name in self.manifest["required_jobs"]:
            if name.startswith(("e2e-", "gateway-", "ingress-", "helm-", "unit-", "gitops-")) or name == "snapshot":
                self.assertIn(name, initial_names)
        self.assertEqual(jobs["verification-short"]["needs"], "coverage-seed")
        self.assertEqual(jobs["verification-short"]["if"], "${{ !cancelled() }}")
        seed = jobs["coverage-seed"]["strategy"]["matrix"]["include"]
        self.assertEqual([row["name"] for row in seed], ["coverage-renderer"])

    def test_e2e_matrix_has_every_shard_of_every_supported_version(self):
        job = self.workflow["jobs"]["verification"]
        total = int(job["env"]["E2E_SHARD_TOTAL"])
        groups = collections.defaultdict(set)
        for row in job["strategy"]["matrix"]["include"]:
            name = row["name"]
            if name.startswith(("e2e-hp", "e2e-minimum-kubernetes-")):
                group, index = name.rsplit("-", 1)
                groups[group].add(int(index))
        self.assertEqual(set(groups), {"e2e-hp3.0", "e2e-hp3.1", "e2e-hp3.2", "e2e-hp3.3", "e2e-hp3.4",
                                       "e2e-minimum-kubernetes"})
        for group, indexes in groups.items():
            with self.subTest(group=group):
                self.assertEqual(indexes, set(range(1, total + 1)))


if __name__ == "__main__":
    unittest.main()
