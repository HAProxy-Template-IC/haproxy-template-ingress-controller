import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).parents[1] / "analyze-gateway-api-outcomes.py"
SPEC = importlib.util.spec_from_file_location("analyze_gateway_api_outcomes", SCRIPT)
ANALYZER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ANALYZER)


class LifecycleOutcomesTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.directory = Path(self.tmp.name)
        self.identities = [
            {"namespace": "haptic", "name": name, "uid": name + "-uid", "component": "controller",
             "containers": [{"name": "controller", "image": "haptic:test", "imageID": "sha256:abc",
                             "containerID": name + "-container", "ready": True, "restartCount": 0}]}
            for name in ("controller-a", "controller-b")
        ]
        self.clean_metrics = "\n".join(f"{name} 0" for name in sorted(ANALYZER.SCALARS)) + "\n"
        for phase, epoch in (("before", "100"), ("after", "200")):
            directory = self.directory / phase
            (directory / "controller-metrics").mkdir(parents=True)
            (directory / "epoch.txt").write_text(epoch)
            self.write_identities(phase, self.identities)
            for pod in self.identities:
                self.write_metrics(phase, self.clean_metrics, pod["name"])

    def write_identities(self, phase, identities):
        (self.directory / phase / "haptic-identities.json").write_text(json.dumps(identities))

    def write_metrics(self, phase, metrics, pod="controller-a"):
        (self.directory / phase / "controller-metrics" / f"{pod}.prom").write_text(metrics)

    def test_clean_lifecycle_allows_absent_unused_vectors(self):
        result = ANALYZER.analyze(self.directory)
        self.assertTrue(result["pass"])
        self.assertTrue(result["evidence_valid"])
        self.assertEqual(len(result["metrics"]), 7)
        self.assertTrue(all(len(metric["per_pod"]) == 2 for metric in result["metrics"]))

    def test_recovered_ramp_or_teardown_errors_fail_even_with_clean_steady_snapshots(self):
        for phase in ("steady-activity-start", "steady-activity-end"):
            directory = self.directory / phase
            directory.mkdir()
            (directory / "counters.json").write_text('{"outcome_quality": {"pass": true}}')
        for metric in sorted(ANALYZER.METRICS):
            with self.subTest(metric=metric):
                if metric in ANALYZER.SCALARS:
                    metrics = self.clean_metrics.replace(f"{metric} 0", f"{metric} 1")
                else:
                    metrics = self.clean_metrics + f'{metric}{{reason="rejected"}} 1\n'
                self.write_metrics("after", metrics)
                result = ANALYZER.analyze(self.directory)
                self.assertFalse(result["pass"])
                self.assertTrue(result["evidence_valid"])
                self.assertEqual(next(row for row in result["metrics"] if row["metric"] == metric)["delta"], "1")

    def test_existing_errors_outside_the_scenario_are_not_new_errors(self):
        metrics = self.clean_metrics.replace("haptic_reconciliation_errors_total 0",
                                             "haptic_reconciliation_errors_total 3")
        for phase in ("before", "after"):
            self.write_metrics(phase, metrics)
        self.assertTrue(ANALYZER.analyze(self.directory)["pass"])

    def test_counter_advance_cannot_disappear_in_numeric_rounding(self):
        metric = "haptic_reconciliation_errors_total"
        for before, after, delta in (("0", "1e-999", "1e-999"),
                                     ("1000000000000000000000000000000", "1000000000000000000000000000001", "1")):
            with self.subTest(before=before, after=after):
                self.write_metrics("before", self.clean_metrics.replace(f"{metric} 0", f"{metric} {before}"))
                self.write_metrics("after", self.clean_metrics.replace(f"{metric} 0", f"{metric} {after}"))
                result = ANALYZER.analyze(self.directory)
                self.assertFalse(result["pass"])
                measured = next(item for item in result["metrics"] if item["metric"] == metric)
                self.assertEqual(ANALYZER.Decimal(measured["delta"]), ANALYZER.Decimal(delta))
                self.assertEqual(ANALYZER.Decimal(measured["per_pod"][0]["before"]), ANALYZER.Decimal(before))
                self.assertEqual(ANALYZER.Decimal(measured["per_pod"][0]["after"]), ANALYZER.Decimal(after))
                self.assertEqual(ANALYZER.Decimal(measured["per_pod"][0]["delta"]), ANALYZER.Decimal(delta))

    def test_series_and_fleet_sums_preserve_exact_decimal_evidence(self):
        metric = "haptic_apply_rejected_total"
        for pod in ("controller-a", "controller-b"):
            before = f'{metric}{{reason="one"}} 1e30\n'
            after = before + f'{metric}{{reason="two"}} 1\n'
            self.write_metrics("before", self.clean_metrics + before, pod)
            self.write_metrics("after", self.clean_metrics + after, pod)
        result = ANALYZER.analyze(self.directory)
        measured = next(item for item in result["metrics"] if item["metric"] == metric)
        self.assertFalse(result["pass"])
        self.assertEqual(measured["delta"], "2")
        for row in measured["per_pod"]:
            self.assertEqual(row["after"], "1000000000000000000000000000001")
            self.assertEqual(row["delta"], "1")

    def test_missing_or_malformed_scalars_fail_closed(self):
        metric = "haptic_reconciliation_errors_total"
        for replacement in ("", f"{metric} 0\n{metric} 0", f"{metric} NaN", f"{metric} +Inf",
                            f"{metric} -1", f"{metric} invalid", f"{metric} 0 extra",
                            f"{metric} 1e999", f"{metric} 1_0",
                            f'{metric}{{reason="x"}} 0'):
            with self.subTest(replacement=replacement):
                self.write_metrics("after", self.clean_metrics.replace(f"{metric} 0", replacement))
                with self.assertRaises((ValueError, ANALYZER.InvalidOperation)):
                    ANALYZER.analyze(self.directory)

    def test_vector_series_cannot_reset_or_disappear_behind_a_fleet_sum(self):
        metric = "haptic_apply_rejected_total"
        old = f'{metric}{{reason="one"}} 5\n{metric}{{reason="two"}} 0\n'
        for new in (f'{metric}{{reason="one"}} 4\n{metric}{{reason="two"}} 2\n',
                    f'{metric}{{reason="two"}} 6\n', ""):
            with self.subTest(new=new):
                self.write_metrics("before", self.clean_metrics + old)
                self.write_metrics("after", self.clean_metrics + new)
                with self.assertRaisesRegex(ValueError, "disappeared|decreased"):
                    ANALYZER.analyze(self.directory)

    def test_pod_counter_reset_cannot_hide_behind_another_pod(self):
        metric = "haptic_reconciliation_errors_total"
        self.write_metrics("before", self.clean_metrics.replace(f"{metric} 0", f"{metric} 5"))
        self.write_metrics("after", self.clean_metrics.replace(f"{metric} 0", f"{metric} 6"), "controller-b")
        with self.assertRaisesRegex(ValueError, "decreased"):
            ANALYZER.analyze(self.directory)

    def test_vector_labels_and_series_are_validated(self):
        metric = "haptic_apply_rejected_total"
        for samples in (f'{metric}{{a="x",b="y"}} 0\n{metric}{{b="y",a="x"}} 0\n',
                        f'{metric}{{a="x",a="y"}} 0\n', f'{metric}{{a=x}} 0\n',
                        f'{metric}{{a="x" b="y"}} 0\n', f'{metric} 0\n'):
            with self.subTest(samples=samples):
                self.write_metrics("after", self.clean_metrics + samples)
                with self.assertRaises(ValueError):
                    ANALYZER.analyze(self.directory)

    def test_changed_restarted_or_unready_controllers_fail_closed(self):
        for field, value in (("uid", "replacement"), ("restartCount", 1), ("ready", False),
                             ("imageID", "new-image"), ("containerID", "new-container")):
            with self.subTest(field=field):
                identities = copy.deepcopy(self.identities)
                target = identities[0] if field == "uid" else identities[0]["containers"][0]
                target[field] = value
                self.write_identities("after", identities)
                with self.assertRaises(ValueError):
                    ANALYZER.analyze(self.directory)

    def test_missing_extra_or_duplicate_pods_fail_closed(self):
        for identities in ([], self.identities[:1], self.identities + self.identities[:1]):
            with self.subTest(identities=identities):
                self.write_identities("after", identities)
                with self.assertRaises(ValueError):
                    ANALYZER.analyze(self.directory)
        self.write_identities("after", self.identities)
        self.write_metrics("after", self.clean_metrics, "unexpected-controller")
        with self.assertRaisesRegex(ValueError, "exact controller fleet"):
            ANALYZER.analyze(self.directory)

    def test_snapshot_interval_must_be_positive_and_finite(self):
        for epoch in ("100", "99", "NaN", "Infinity", "-1"):
            with self.subTest(epoch=epoch):
                (self.directory / "after" / "epoch.txt").write_text(epoch)
                with self.assertRaises(ValueError):
                    ANALYZER.analyze(self.directory)

    def test_cli_distinguishes_measured_failure_from_invalid_evidence(self):
        output = self.directory / "outcomes.json"
        command = [sys.executable, str(SCRIPT), "--scenario-dir", str(self.directory), "--output", str(output)]
        self.write_metrics("after", self.clean_metrics.replace("haptic_reconciliation_errors_total 0",
                                                              "haptic_reconciliation_errors_total 1"))
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(json.loads(output.read_text())["pass"])
        output.unlink()
        self.write_metrics("after", "")
        result = subprocess.run(command, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(output.exists())

    def test_runner_attaches_lifecycle_gate_without_overwriting_other_failures(self):
        runner = SCRIPT.parent / "bench-gateway-api.sh"
        for scenario in ("probe", "scale", "routechange"):
            for prior_pass, errors in ((True, 0), (True, 1), (False, 0)):
                with self.subTest(scenario=scenario, prior_pass=prior_pass, errors=errors):
                    analysis = self.directory / "analysis.json"
                    analysis.write_text(json.dumps({
                        "scenario": scenario, "measurement_valid": True, "pass": prior_pass,
                        "upstream_program": {"pass": True},
                        "supervised_child_continuity": {"evidence_valid": True, "pass": True},
                        "haptic_scenario_quality": {"pass": prior_pass, "measurement_complete": True},
                    }))
                    self.write_metrics("after", self.clean_metrics.replace(
                        "haptic_reconciliation_errors_total 0", f"haptic_reconciliation_errors_total {errors}"))
                    result = subprocess.run([
                        "bash", "-c", 'source "$1"; attach_lifecycle_outcome_quality "$2"',
                        "bash", str(runner), str(self.directory),
                    ], capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    observed = json.loads(analysis.read_text())
                    self.assertTrue(observed["measurement_valid"])
                    self.assertEqual(observed["pass"], prior_pass and errors == 0)
                    self.assertEqual(observed["haptic_scenario_quality"]["pass"], observed["pass"])
                    self.assertEqual(observed["lifecycle_outcome_quality"]["pass"], errors == 0)
                    output = self.directory / "report"
                    (output / scenario).mkdir(parents=True, exist_ok=True)
                    report = output / scenario / "analysis.json"
                    report.write_text(json.dumps(observed))
                    summary_command = [
                        "bash", "-c", 'source "$1"; BENCH_OUTPUT_DIR="$2"; SCENARIOS=("$3"); write_runner_summary',
                        "bash", str(runner), str(output), scenario,
                    ]
                    result = subprocess.run(summary_command, capture_output=True, text=True)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    summary = json.loads((output / "runner-summary.json").read_text())
                    self.assertEqual(summary["measured_result"]["pass"], observed["pass"])
                    del observed["lifecycle_outcome_quality"]
                    report.write_text(json.dumps(observed))
                    result = subprocess.run(summary_command, capture_output=True, text=True)
                    self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
