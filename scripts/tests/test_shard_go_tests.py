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

    def test_weights_balance_serial_cost(self):
        weights = {
            "TestHeavy": {"seconds": 360.0, "parallel": False},
            "TestMedium": {"seconds": 180.0, "parallel": False},
            "TestWide": {"seconds": 200.0, "parallel": True},
        }
        weights.update({f"TestLight{index}": {"seconds": 30.0, "parallel": False} for index in range(12)})
        inventory = "\n".join(list(weights) + ["TestUnmeasured"])
        shards = SHARD.partition(inventory, 3, weights)

        def serial(shard):
            return sum(weights.get(name, {"seconds": 30.0, "parallel": False})["seconds"]
                       for name in shard if not weights.get(name, {"parallel": False})["parallel"])

        self.assertCountEqual([name for shard in shards for name in shard], inventory.split())
        self.assertEqual(max(map(serial, shards)), 360.0, "nothing serial may join the heaviest test")
        self.assertEqual(sorted(map(serial, shards)), [270.0, 300.0, 360.0])

    def test_unmeasured_tests_cost_a_typical_cluster_test(self):
        weights = {"TestA": 20.0, "TestB": 30.0, "TestC": 40.0, "TestWide": 90.0}
        weights.update({f"TestHelper{index}": 0.0 for index in range(10)})
        weights = {name: {"seconds": seconds, "parallel": name == "TestWide"} for name, seconds in weights.items()}
        self.assertEqual(SHARD.unmeasured_weight(weights), {"seconds": 30.0, "parallel": False})

    def test_weighted_partition_is_deterministic(self):
        weights = {f"TestCase{index}": {"seconds": float(index % 7), "parallel": index % 3 == 0} for index in range(40)}
        reordered = "\n".join(reversed(list(weights)))
        self.assertEqual(SHARD.partition("\n".join(weights), 3, weights),
                         SHARD.partition(reordered, 3, dict(reversed(weights.items()))))

    def test_explicit_selection_retains_exact_names(self):
        shards = SHARD.partition("TestOne\nTestTwo\nTestThree", 1, include="TestOne\nTestThree\n")
        self.assertEqual(shards, [["TestOne", "TestThree"]])

    def test_explicit_selection_rejects_missing_duplicate_empty_or_pattern_names(self):
        for selection in ("TestMissing", "TestOne\nTestOne", "", "Test.*", "TestOne\n\n"):
            with self.subTest(selection=selection), self.assertRaises(ValueError):
                SHARD.partition("TestOne\nTestTwo", 1, include=selection)

    def test_rejects_empty_shards_and_invalid_totals(self):
        for inventory, total in [("ok example/pkg 0.01s", 1), ("TestOne", 2), ("TestOne", 0)]:
            with self.subTest(inventory=inventory, total=total):
                with self.assertRaises(ValueError):
                    SHARD.partition(inventory, total)


if __name__ == "__main__":
    unittest.main()
