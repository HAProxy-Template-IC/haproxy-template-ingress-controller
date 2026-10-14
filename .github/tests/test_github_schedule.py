import copy
import json
from pathlib import Path
import unittest

import yaml

from scripts.ci.github_schedule import lanes, schedule


ROOT = Path(__file__).parents[2]


class ScheduleTests(unittest.TestCase):
    def test_long_suites_start_first_and_short_suites_fill_free_capacity(self):
        self.assertEqual(lanes({"a": 30, "b": 20, "c": 10}, 2), [["a"], ["b", "c"]])
        self.assertEqual(lanes({"a": 30, "b": 20}, 2, early=["b"]), [["b"], ["a"]])

    def test_invalid_durations_and_unknown_coverage_fail(self):
        for durations, count in (({}, 2), ({"a": 0}, 2), ({"a": 1}, 0)):
            with self.assertRaises(ValueError):
                lanes(durations, count)
        with self.assertRaises(ValueError):
            lanes({"a": 1}, 2, early=["unknown"])

    def test_rebalances_a_greedy_tail_without_losing_jobs_or_early_roots(self):
        durations = {"a": 8, "b": 7, "c": 6, "d": 5, "e": 4}
        selected = lanes(durations, 2, early=["a"], handoff_seconds=0)
        self.assertEqual(sorted(sum(durations[name] for name in lane) for lane in selected), [15, 15])
        self.assertCountEqual([name for lane in selected for name in lane], durations)
        self.assertIn("a", {lane[0] for lane in selected})

    def test_consumers_wait_for_cache_producer_and_retain_their_execution_slot(self):
        self.assertEqual(lanes({"producer": 20, "consumer": 5, "long": 30, "short": 5}, 2,
                               early=["producer"], chains=[["producer", "consumer"]]),
                         [["producer", "consumer", "short"], ["long"]])
        for chains in ([["a", "a"]], [["a", "missing"]], [["a"]], [["a", "b"], ["b", "c"]]):
            with self.subTest(chains=chains), self.assertRaises(ValueError):
                lanes({"a": 30, "b": 20, "c": 10}, 2, chains=chains)
        with self.assertRaises(ValueError):
            lanes({"a": 30, "b": 20}, 2, chains=[["a", "b"]], early=["b"])

    def test_checked_in_schedule_matches_recorded_measurements(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/ci-speed-proof.yml").read_text())
        weights = json.loads((ROOT / ".github/job-duration-weights.json").read_text())
        rebuilt = copy.deepcopy(workflow)
        selected = schedule(rebuilt, weights)
        self.assertEqual(rebuilt, workflow)
        self.assertLessEqual(len(selected) + 1, 60)
        self.assertEqual(sum(map(len, selected)), len(weights["jobs"]))
        roots = {lane[0] for lane in selected}
        coverage = {workflow["jobs"][key]["name"] for key in workflow["jobs"]["coverage"]["needs"]}
        self.assertTrue(coverage | {"unit-command-1", "unit-rest"} <= roots)
        del weights["jobs"][selected[0][0]]
        with self.assertRaises(ValueError):
            schedule(rebuilt, weights)


if __name__ == "__main__":
    unittest.main()
