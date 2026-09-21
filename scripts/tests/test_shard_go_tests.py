"""A sharded run must preserve the complete compiled test inventory."""

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "shard-go-tests.py"
SPEC = importlib.util.spec_from_file_location("shard_go_tests", SCRIPT)
SHARD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SHARD)


class ShardTest(unittest.TestCase):
    def test_disjoint_complete_balanced_partitions(self):
        names = ["Test"] + [f"TestCase{index}" for index in range(173)]
        inventory = "\n".join(names + ["ok example/package 0.001s", "? example/helper [no test files]"])
        for total in [1, 2, 3, 8]:
            with self.subTest(total=total):
                shards = SHARD.partition(inventory, total)
                flattened = [name for shard in shards for name in shard]
                self.assertCountEqual(flattened, names)
                self.assertEqual(len(flattened), len(set(flattened)))
                self.assertLessEqual(max(map(len, shards)) - min(map(len, shards)), 1)

    def test_inventory_order_and_duplicate_package_names(self):
        original = "TestOne\nTestTwo\nTestThree\nTestFour"
        repeated = "TestFour\nTestTwo\nTestOne\nTestOne\nTestThree"
        self.assertEqual(SHARD.partition(original, 3), SHARD.partition(repeated, 3))

    def test_rejects_empty_shards_and_invalid_totals(self):
        for inventory, total in [("ok example/pkg 0.01s", 1), ("TestOne", 2), ("TestOne", 0)]:
            with self.subTest(inventory=inventory, total=total):
                with self.assertRaises(ValueError):
                    SHARD.partition(inventory, total)


if __name__ == "__main__":
    unittest.main()
