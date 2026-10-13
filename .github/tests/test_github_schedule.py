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
        self.assertEqual(lanes({"a": 30, "b": 20}, 2, coverage=["b"], coverage_seconds=20), [["b"], ["a"]])

    def test_invalid_durations_and_unknown_coverage_fail(self):
        for durations, count in (({}, 2), ({"a": 0}, 2), ({"a": 1}, 0)):
            with self.assertRaises(ValueError):
                lanes(durations, count)
        with self.assertRaises(ValueError):
            lanes({"a": 1}, 2, coverage=["unknown"])

    def test_checked_in_schedule_matches_recorded_measurements(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/ci-speed-proof.yml").read_text())
        weights = json.loads((ROOT / ".github/job-duration-weights.json").read_text())
        rebuilt = copy.deepcopy(workflow)
        selected = schedule(rebuilt, weights)
        self.assertEqual(rebuilt, workflow)
        self.assertLessEqual(len(selected) + 1, 60)
        self.assertEqual(sum(map(len, selected)), len(weights["jobs"]))
        del weights["jobs"][selected[0][0]]
        with self.assertRaises(ValueError):
            schedule(rebuilt, weights)


if __name__ == "__main__":
    unittest.main()
