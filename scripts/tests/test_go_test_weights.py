"""Shard weights come from what `go test -v` reports, not from hand-kept numbers."""

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "go-test-weights.py"
SPEC = importlib.util.spec_from_file_location("go_test_weights", SCRIPT)
WEIGHTS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(WEIGHTS)

SHARD_ONE = """\
2026-10-02T09:01:42Z 01O === RUN   TestSerial
2026-10-02T09:01:42Z 01O === RUN   TestWide
2026-10-02T09:01:42Z 01O === PAUSE TestWide
2026-10-02T09:01:42Z 01O === PAUSE TestWide/inner
2026-10-02T09:01:42Z 01O --- PASS: TestSerial (368.54s)
2026-10-02T09:01:42Z 01O     --- PASS: TestSerial/inner (368.50s)
2026-10-02T09:01:42Z 01O --- FAIL: TestWide (12.30s)
"""
SHARD_TWO = """\
--- PASS: TestSerial (300.00s)
--- SKIP: TestSkipped (0.00s)
"""


class GoTestWeightsTest(unittest.TestCase):
    def test_top_level_results_and_parallel_marks(self):
        self.assertEqual(WEIGHTS.weights([SHARD_ONE, SHARD_TWO]), {
            "TestSerial": {"seconds": 368.5, "parallel": False},
            "TestWide": {"seconds": 12.3, "parallel": True},
        })


if __name__ == "__main__":
    unittest.main()
