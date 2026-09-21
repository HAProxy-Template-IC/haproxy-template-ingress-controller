"""Regressions for incomplete and misleading conformance reports."""

import importlib.util
from pathlib import Path
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "validate-conformance-report.py"
SPEC = importlib.util.spec_from_file_location("conformance_report", SCRIPT)
REPORT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(REPORT)


def complete_report():
    return {
        "kind": "ConformanceReport",
        "implementation": {
            "project": "haptic",
            "organization": "haproxy-haptic",
            "version": "dev",
        },
        "gatewayAPIVersion": "v1.6.2",
        "gatewayAPIChannel": "standard",
        "profiles": [
            {
                "name": name,
                "core": {
                    "result": "success",
                    "statistics": {"Passed": 10, "Skipped": 0, "Failed": 0},
                },
            }
            for name in sorted(REPORT.PROFILES)
        ],
    }


class ConformanceReportTest(unittest.TestCase):
    def test_complete_core_profiles(self):
        result = REPORT.validate_report(complete_report(), "dev", "v1.6.2")
        self.assertEqual(set(result), REPORT.PROFILES)

    def test_partial_extended_support_remains_visible(self):
        report = complete_report()
        report["profiles"][0]["extended"] = {
            "result": "partial",
            "statistics": {"Passed": 20, "Skipped": 1, "Failed": 0},
            "skippedTests": ["BackendTLSPolicySANValidation"],
        }
        result = REPORT.validate_report(report, "dev", "v1.6.2")
        extended = result[report["profiles"][0]["name"]]["extended"]
        self.assertEqual(extended["result"], "partial")
        self.assertEqual(extended["skipped_tests"], ["BackendTLSPolicySANValidation"])

    def test_rejects_mismatched_versions(self):
        for implementation, gateway in [("other", "v1.6.2"), ("dev", "v1.5.0")]:
            with self.subTest(implementation=implementation, gateway=gateway):
                with self.assertRaises(ValueError):
                    REPORT.validate_report(complete_report(), implementation, gateway)

    def test_rejects_missing_and_duplicate_profiles(self):
        for duplicate in (False, True):
            report = complete_report()
            report["profiles"].pop()
            if duplicate:
                report["profiles"].append(report["profiles"][0])
            with self.subTest(duplicate=duplicate), self.assertRaises(ValueError):
                REPORT.validate_report(report, "dev", "v1.6.2")

    def test_rejects_zero_skipped_and_failed_core_coverage(self):
        for counts in (
            {"Passed": 0, "Skipped": 0, "Failed": 0},
            {"Passed": 10, "Skipped": 1, "Failed": 0},
            {"Passed": 10, "Skipped": 0, "Failed": 1},
        ):
            report = complete_report()
            report["profiles"][0]["core"]["statistics"] = counts
            with self.subTest(counts=counts), self.assertRaises(ValueError):
                REPORT.validate_report(report, "dev", "v1.6.2")

    def test_rejects_failed_extended_tests(self):
        report = complete_report()
        report["profiles"][0]["extended"] = {
            "result": "failure",
            "statistics": {"Passed": 20, "Skipped": 0, "Failed": 1},
            "failedTests": ["HTTPRouteHeaderMatching"],
        }
        with self.assertRaises(ValueError):
            REPORT.validate_report(report, "dev", "v1.6.2")


if __name__ == "__main__":
    unittest.main()
