"""The nightly-scale trend gate finds its baselines and compares like with like."""

import importlib.util
import urllib.error
from pathlib import Path
import unittest
from unittest import mock


SCRIPT = Path(__file__).resolve().parents[1] / "scale-trend.py"
SPEC = importlib.util.spec_from_file_location("scale_trend", SCRIPT)
TREND = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(TREND)

EPYC = "AMD EPYC 7B13"
XEON = "Intel(R) Xeon(R) CPU @ 2.20GHz"


def run(job_id, p95=2.0, rss=900e6, model=EPYC):
    return {
        "_job_id": job_id,
        "runner_cpu_model": model,
        "change_convergence_seconds_p95": p95,
        "controller_rss_bytes": rss,
        "change_convergence_seconds_median": 1.5,
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
        self.assertIn("trend: controller_rss_bytes: no comparable baseline", report)


class FetchHistoryTest(unittest.TestCase):
    API = "https://gitlab.example/api/v4"

    def fake_get(self, responses):
        def get(url, headers=None):
            path = url[len(self.API):]
            for prefix, response in responses.items():
                if path.startswith(prefix):
                    if isinstance(response, Exception):
                        raise response
                    self.headers[prefix] = headers
                    return response
            raise AssertionError(f"unexpected request {url}")
        self.headers = {}
        return get

    def http_error(self, code):
        err = urllib.error.HTTPError("u", code, "e", {}, None)
        self.addCleanup(err.close)
        return err

    def test_collects_successful_nightly_scale_jobs_across_pages(self):
        responses = {
            "/projects/7/pipelines?ref=main&source=schedule&per_page=30&page=1": ([{"id": 100}, {"id": 99}], "2"),
            "/projects/7/pipelines?ref=main&source=schedule&per_page=30&page=2": ([{"id": 98}], ""),
            "/projects/7/pipelines/100/": ([{"id": 1, "name": "nightly-scale"}], ""),
            "/projects/7/pipelines/99/jobs?scope%5B%5D=success&per_page=100&page=1": ([{"id": 2, "name": "lint"}], "2"),
            "/projects/7/pipelines/99/jobs?scope%5B%5D=success&per_page=100&page=2": ([{"id": 3, "name": "nightly-scale"}], ""),
            "/projects/7/pipelines/98/": ([{"id": 4, "name": "nightly-scale"}], ""),
            "/projects/7/jobs/3/": ({"config_lines": 3}, ""),
            "/projects/7/jobs/4/": ({"config_lines": 4}, ""),
        }
        with mock.patch.object(TREND, "get", self.fake_get(responses)):
            runs = TREND.fetch_history(self.API, "7", "tok", "100")
        self.assertEqual([r["_job_id"] for r in runs], [3, 4])
        self.assertEqual(self.headers["/projects/7/jobs/3/"], {"JOB-TOKEN": "tok"})
        self.assertIsNone(self.headers["/projects/7/pipelines?ref=main&source=schedule&per_page=30&page=1"])

    def test_expired_artifact_is_skipped(self):
        responses = {
            "/projects/7/pipelines?": ([{"id": 99}, {"id": 98}], ""),
            "/projects/7/pipelines/99/": ([{"id": 3, "name": "nightly-scale"}], ""),
            "/projects/7/pipelines/98/": ([{"id": 4, "name": "nightly-scale"}], ""),
            "/projects/7/jobs/3/": self.http_error(404),
            "/projects/7/jobs/4/": ({}, ""),
        }
        with mock.patch.object(TREND, "get", self.fake_get(responses)):
            runs = TREND.fetch_history(self.API, "7", "tok", "1")
        self.assertEqual([r["_job_id"] for r in runs], [4])

    def test_api_errors_fail_loudly(self):
        responses = {"/projects/7/pipelines?": self.http_error(403)}
        with mock.patch.object(TREND, "get", self.fake_get(responses)):
            with self.assertRaises(urllib.error.HTTPError):
                TREND.fetch_history(self.API, "7", "tok", "1")

    def test_stops_at_max_runs(self):
        pipelines = [{"id": i} for i in range(1, 40)]
        responses = {
            "/projects/7/pipelines?": (pipelines, ""),
            "/projects/7/pipelines/": ([{"id": 5, "name": "nightly-scale"}], ""),
            "/projects/7/jobs/5/": ({}, ""),
        }
        with mock.patch.object(TREND, "get", self.fake_get(responses)):
            runs = TREND.fetch_history(self.API, "7", "tok", "0")
        self.assertEqual(len(runs), TREND.MAX_RUNS)


if __name__ == "__main__":
    unittest.main()
