import datetime
import unittest

from scripts.ci.prune_snapshots import expired

NOW = datetime.datetime(2026, 10, 15, tzinfo=datetime.timezone.utc)


def version(tags, days_old):
    created = (NOW - datetime.timedelta(days=days_old)).isoformat().replace("+00:00", "Z")
    return {"id": len(tags) * 1000 + days_old, "created_at": created,
            "metadata": {"container": {"tags": tags}}}


class PruneSnapshotsTests(unittest.TestCase):
    def test_selects_only_snapshot_versions_past_the_window(self):
        old_controller = version(["main-0123abcd-haproxy3.4"], 181)
        old_bundle = version(["main-0123abcd"], 200)
        old_chart = version(["0.0.0-main.g0123abcd"], 365)
        keep = [
            version(["main-0123abcd-haproxy3.4"], 179),
            version(["0.6.0-haproxy3.4"], 400),
            version(["0.6.0", "main-0123abcd"], 400),
            version([], 400),
            version(["sha256-" + "a" * 64 + ".sig"], 400),
            version(["main-0123abc"], 400),
            version(["0.0.0-main.0123abcd"], 400),
        ]
        selected = expired([old_controller, old_bundle, old_chart, *keep], NOW, 180)
        self.assertEqual(selected, [old_controller, old_bundle, old_chart])


if __name__ == "__main__":
    unittest.main()
