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
        names = [job["name"] for job in self.workflow["jobs"].values()]
        self.assertEqual(len(names), len(set(names)))
        self.assertEqual(set(names), set(self.manifest["required_jobs"]) | {"verified"})
        mapped = {name for group in self.manifest["gitlab_job_mapping"].values() for name in group}
        mapped |= {name for group in self.manifest["additional_gitlab_job_mapping"].values() for name in group}
        self.assertEqual(mapped, set(names))
        self.assertEqual(self.manifest["allowed_failure_jobs"], ["gateway-canary"])
        self.assertEqual({job["name"] for job in self.workflow["jobs"].values()
                          if job.get("continue-on-error")}, {"gateway-canary"})

    def test_skipped_jobs_keep_unique_names_without_matrix_expansion(self):
        names = [job["name"] for job in self.workflow["jobs"].values()]
        self.assertEqual(len(names), len(set(names)))
        self.assertEqual(set(names), set(self.manifest["required_jobs"]) | {"verified"})
        for job in self.workflow["jobs"].values():
            self.assertNotIn("strategy", job)
            if "CI_SUITE" in job.get("env", {}):
                suite = json.loads(job["env"]["CI_SUITE"])
                self.assertEqual(job["name"], suite["name"])
                self.assertEqual(job["runs-on"], suite["runner"])

    def test_coverage_has_every_profile_without_waiting_for_unrelated_suites(self):
        jobs = self.workflow["jobs"]
        self.assertEqual({jobs[group]["name"] for group in jobs["coverage"]["needs"]}, {
            "coverage-rest", "coverage-renderer",
            "integration-3.0", "integration-3.1", "integration-3.2", "integration-3.3", "integration-3.4",
        })
        self.assertEqual(set(jobs["verified"]["needs"]), set(jobs) - {"verified"})

    def test_execution_chains_reserve_capacity_for_coverage_aggregation(self):
        jobs = self.workflow["jobs"]
        suites = {key: job for key, job in jobs.items() if "CI_SUITE" in job.get("env", {})}
        roots = {key for key, job in suites.items() if not job.get("needs")}
        self.assertLessEqual(len(roots) + 1, 60)
        children = collections.Counter()
        for key, job in suites.items():
            if job.get("needs"):
                self.assertIn(job["needs"], suites)
                children[job["needs"]] += 1
            visited = set()
            current = key
            while current not in roots:
                self.assertNotIn(current, visited, "Execution chains must not contain cycles")
                visited.add(current)
                current = suites[current]["needs"]
        self.assertTrue(all(count == 1 for count in children.values()), "A lane must not fork")

    def test_prose_profile_preserves_every_gitlab_common_gate(self):
        policy = json.loads((ROOT / ".gitlab/ci/verification-policy.json").read_text())
        mapping = self.manifest["gitlab_job_mapping"]
        expected = {name for job in policy["common_jobs"] for key, names in mapping.items()
                    if key == job or key.startswith(job + ": [") for name in names}
        prose = self.manifest["prose"]
        self.assertEqual(prose["paths"], policy["prose_paths"])
        self.assertEqual(prose["patterns"], policy["prose_patterns"])
        self.assertEqual(set(prose["required_jobs"]), expected)
        for key, job in self.workflow["jobs"].items():
            if key == "verified":
                self.assertEqual(job["if"], "always()")
                continue
            condition = ("${{ !cancelled() }}" if job["name"] in expected else
                         "${{ !cancelled() && inputs.verification_profile != 'prose' }}")
            self.assertEqual(job["if"], condition)
        inputs = self.workflow["on"]["workflow_call"]["inputs"]
        self.assertEqual(inputs["verification_profile"]["default"], "full")

    def test_e2e_has_every_shard_of_every_supported_version(self):
        groups = collections.defaultdict(set)
        for job in self.workflow["jobs"].values():
            name = job["name"]
            if name.startswith(("e2e-hp", "e2e-minimum-kubernetes-")):
                self.assertEqual(int(job["env"]["E2E_SHARD_TOTAL"]), 4)
                group, index = name.rsplit("-", 1)
                self.assertEqual(int(job["env"]["E2E_SHARD_INDEX"]), int(index))
                groups[group].add(int(index))
        self.assertEqual(set(groups), {"e2e-hp3.0", "e2e-hp3.1", "e2e-hp3.2", "e2e-hp3.3", "e2e-hp3.4",
                                       "e2e-minimum-kubernetes"})
        for group, indexes in groups.items():
            with self.subTest(group=group):
                self.assertEqual(indexes, {1, 2, 3, 4})

    def test_unit_shards_cover_command_and_renderer_with_one_cache_writer_each(self):
        groups = collections.defaultdict(list)
        for job in self.workflow["jobs"].values():
            suite = json.loads(job.get("env", {}).get("CI_SUITE", "{}"))
            if suite.get("suite") != "unit-shard" or suite["shard"] == "rest":
                continue
            groups[suite["shard"]].append(int(suite["index"]))
            self.assertEqual(job["env"]["SHARD_INDEX"], suite["index"])
            cache_steps = {step.get("name"): step for step in job["steps"]}
            writer = cache_steps["Reuse Go downloads and compilation"]
            reader = cache_steps["Restore shared unit compilation"]
            self.assertEqual(writer["with"]["key"], reader["with"]["key"])
            self.assertIn("fromJSON(env.CI_SUITE).index == 1", writer["if"])
            self.assertIn("fromJSON(env.CI_SUITE).index != 1", reader["if"])
        self.assertEqual(set(groups), {"command", "renderer"})
        for indexes in groups.values():
            self.assertEqual(sorted(indexes), [1, 2, 3])


if __name__ == "__main__":
    unittest.main()
