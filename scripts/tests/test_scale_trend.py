"""The scale trend gate compares like with like."""

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "scale-trend.py"
SPEC = importlib.util.spec_from_file_location("scale_trend", SCRIPT)
TREND = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(TREND)

EPYC = "AMD EPYC 7B13"
XEON = "Intel(R) Xeon(R) CPU @ 2.20GHz"


def run(job_id, p95=2.0, rss=900e6, model=EPYC, publication_p95=3.0):
    return {
        "_job_id": job_id,
        "scale_metrics_version": 2,
        "runner_cpu_model": model,
        "change_convergence_seconds_p95": p95,
        "haproxycfg_publication_seconds_p95": publication_p95,
        "controller_rss_bytes": rss,
        "change_convergence_seconds_median": 1.5,
        "haproxycfg_publication_seconds_median": 2.0,
        "seed_to_converged_seconds": 44.0,
        "controller_container_cpu_seconds_delta": 330.0,
        "haproxy_reloads_total_delta": 4,
        "config_lines": 3958,
    }


class EvaluateTest(unittest.TestCase):
    def verdict(self, cur, history):
        lines, failed = TREND.evaluate(cur, history)
        return "\n".join(lines), failed

    def test_one_slow_baseline_does_not_move_the_median(self):
        history = [run(1), run(2, p95=4.2), run(3), run(4)]
        report, failed = self.verdict(run(0, p95=2.1), history)
        self.assertFalse(failed, report)
        self.assertIn("trend ok: change_convergence_seconds_p95: median 2 -> 2.1", report)

    def test_regression_against_the_median_fails(self):
        report, failed = self.verdict(run(0, p95=3.1), [run(1), run(2), run(3)])
        self.assertTrue(failed)
        self.assertIn("TREND FAIL: change_convergence_seconds_p95: median 2 -> 3.1 (+55%) > +50%", report)

    def test_rss_regression_fails(self):
        _, failed = self.verdict(run(0, rss=1400e6), [run(1), run(2), run(3)])
        self.assertTrue(failed)

    def test_soft_metric_only_warns(self):
        cur = run(0)
        cur["seed_to_converged_seconds"] = 90.0
        report, failed = self.verdict(cur, [run(1), run(2), run(3)])
        self.assertFalse(failed)
        self.assertIn("TREND WARN: seed_to_converged_seconds", report)

    def test_other_cpu_models_are_not_baselines(self):
        history = [run(1, p95=1.0, model=XEON), run(2, p95=1.0, model=XEON), run(3, p95=1.0, model=XEON),
                   run(4), run(5), run(6)]
        report, failed = self.verdict(run(0, p95=2.2), history)
        self.assertFalse(failed, report)
        self.assertIn(f"skipped baselines from other CPU models: {XEON}", report)
        self.assertIn("jobs 4, 5, 6", report)

    def test_window_takes_the_newest_runs(self):
        history = [run(i) for i in range(1, 8)] + [run(8, p95=0.5), run(9, p95=0.5)]
        report, _ = self.verdict(run(0), history)
        self.assertIn("median of 7 runs (jobs 1, 2, 3, 4, 5, 6, 7)", report)

    def test_too_few_same_model_baselines_is_reported_not_gated(self):
        history = [run(1, model=XEON)] * 5 + [run(2), run(3)]
        report, failed = self.verdict(run(0, p95=9.0), history)
        self.assertFalse(failed)
        self.assertIn("TREND UNGATED: 2 of 3 required baselines", report)

    def test_unrecorded_cpu_model_fails(self):
        cur = run(0)
        del cur["runner_cpu_model"]
        report, failed = self.verdict(cur, [run(1), run(2), run(3)])
        self.assertTrue(failed)
        self.assertIn("no runner_cpu_model", report)

    def test_missing_metric_is_not_compared(self):
        cur = run(0)
        del cur["controller_rss_bytes"]
        report, failed = self.verdict(cur, [run(1), run(2), run(3)])
        self.assertFalse(failed)
        self.assertIn("TREND UNGATED: controller_rss_bytes: no comparable baseline", report)

    def test_redefined_metric_ignores_baselines_from_before_the_redefinition(self):
        history = [run(1), run(2), run(3)]
        for old in history:
            old["change_convergence_seconds_p95"] = 0.5
            del old["scale_metrics_version"]
        report, failed = self.verdict(run(0, p95=2.0), history)
        self.assertFalse(failed, report)
        self.assertIn("TREND UNGATED: change_convergence_seconds_p95: no comparable baseline (0 values)", report)

    def test_renamed_metric_keeps_its_history(self):
        history = [run(1), run(2), run(3)]
        for old in history:
            old["change_marker_seconds_p95"] = old.pop("haproxycfg_publication_seconds_p95")
        report, failed = self.verdict(run(0, publication_p95=4.6), history)
        self.assertTrue(failed)
        self.assertIn("TREND FAIL: haproxycfg_publication_seconds_p95: median 3 -> 4.6 (+53%) > +50%", report)


if __name__ == "__main__":
    unittest.main()
