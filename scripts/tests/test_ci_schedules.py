"""Verify complete scheduled job inventories and their dependency closure."""

import json
from pathlib import Path
import re
import unittest

from scripts.ci.configuration import expand, instances, job, load
from scripts.ci.nightly import PIPELINE_NAME
from scripts.tests.test_ci_chart_rules import glob_matches


ROOT = Path(__file__).resolve().parents[2]
NIGHTLY = {"nightly-gwapi-matrix", "nightly-gateway-churn", "nightly-scale", "nightly-gwapi-canary"}
NIGHTLY_BUILDS = {"compute-ci-image-tag", "build-ci", "build-snapshot", "prep-spoa-plugins", "build-spoa-image-snapshot"}
RESERVED = {"include", "stages", "default", "workflow", "variables"}


def condition(expression, env):
    def value(token):
        return env.get(token[1:], "") if token.startswith("$") else json.loads(token)

    def compare(match):
        left, operator, right = match.groups()
        if operator in {"=~", "!~"}:
            matched = re.search(right[1:-1].replace(r"\/", "/"), value(left)) is not None
            return str(matched if operator == "=~" else not matched)
        equal = value(left) == value(right)
        return str(equal if operator == "==" else not equal)

    expression = re.sub(r'(\$\w+)\s*(==|!=|=~|!~)\s*(\$\w+|"[^"]*"|/(?:\\.|[^/])*/)', compare, expression)
    expression = re.sub(r'\$(\w+)', lambda match: str(bool(env.get(match[1], ""))), expression)
    expression = expression.replace("&&", "and").replace("||", "or").strip()
    if not re.fullmatch(r"(?:True|False|and|or|\s|[()])+", expression):
        raise ValueError(f"Unsupported CI condition: {expression}")
    return eval(expression, {"__builtins__": {}}, {})


def selected_rules(rules, config, env, paths, failed=False):
    for rule in expand(rules, config):
        if "if" in rule and not condition(rule["if"], env):
            continue
        if "changes" in rule and env["CI_PIPELINE_SOURCE"] in {"push", "merge_request_event"}:
            changes = rule["changes"]
            if isinstance(changes, dict):
                matches = any(re.search(changes["regexp"].replace(r"\z", r"\Z"), path) for path in paths)
            else:
                matches = any(glob_matches(pattern, path) for pattern in changes for path in paths)
            if not matches:
                continue
        when = rule.get("when", "on_success")
        return when not in {"never", "manual"} and (when != "on_failure" or failed)
    return False


def selected_jobs(config, env, paths=(".gitlab-ci.yml",), failed=False):
    return {name for name in config if name not in RESERVED and not name.startswith(".")
            and selected_rules(job(config, name).get("rules", []), config, env, paths, failed)}


class ScheduleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = load(ROOT / ".gitlab-ci.yml")
        cls.policy = json.loads((ROOT / ".gitlab/ci/verification-policy.json").read_text())

    def environment(self, **overrides):
        return {"CI_PIPELINE_SOURCE": "schedule", "CI_DEFAULT_BRANCH": "main", "CI_COMMIT_BRANCH": "main",
                "SCHEDULE_KIND": "nightly", "REUSE_TRAIN_VERIFICATION": "true", **overrides}

    def test_dispatched_pipeline_name_matches_the_history_query(self):
        env = self.environment(CI_PIPELINE_SOURCE="pipeline")
        for rule in self.config["workflow"]["rules"]:
            if condition(rule["if"], env):
                self.assertEqual(rule["variables"]["HAPTIC_PIPELINE_NAME"], PIPELINE_NAME)
                break
        else:
            self.fail("nightly downstream pipeline is excluded by workflow")

    def test_daily_schedule_only_dispatches(self):
        env = self.environment()
        self.assertTrue(selected_rules(self.config["workflow"]["rules"], self.config, env, ()))
        self.assertEqual(selected_jobs(self.config, env), {"nightly-dispatch"})

    def test_failed_dispatch_reports_without_building_images(self):
        self.assertEqual(selected_jobs(self.config, self.environment(), failed=True),
                         {"nightly-dispatch", "report-nightly-dispatch-failure"})
        reporter = job(self.config, "report-nightly-dispatch-failure")
        self.assertEqual(reporter["image"], job(self.config, "nightly-dispatch")["image"])
        self.assertEqual(reporter["stage"], ".post")
        self.assertEqual(reporter["script"], ["apk add --no-cache bash python3", "./scripts/ci-report-failure.sh"])

    def test_nightly_downstream_and_explicit_runs_have_only_focused_tests_and_builds(self):
        expected = NIGHTLY | NIGHTLY_BUILDS | {"report-pipeline-failure"}
        for source in ("pipeline", "api", "web"):
            with self.subTest(source=source):
                self.assertEqual(selected_jobs(self.config, self.environment(CI_PIPELINE_SOURCE=source)), expected)

    def test_weekly_heartbeat_has_every_full_verification_job(self):
        for source in ("schedule", "api", "web"):
            env = self.environment(CI_PIPELINE_SOURCE=source, SCHEDULE_KIND="weekly")
            selected = selected_jobs(self.config, env)
            with self.subTest(source=source):
                self.assertFalse(set(self.policy["full_jobs"]) - selected)
                self.assertTrue(NIGHTLY <= selected)
                self.assertNotIn("nightly-dispatch", selected)
                self.assertFalse({"trigger-pages", "create-release-tag", "publish-main-snapshot", "publish-chart-snapshot"} & selected)

    def test_schedules_do_not_publish_even_when_publication_is_requested(self):
        for kind in ("nightly", "weekly"):
            env = self.environment(CI_PIPELINE_SOURCE="api", SCHEDULE_KIND=kind, PUBLISH_MAIN_SNAPSHOT="true")
            selected = selected_jobs(self.config, env)
            self.assertFalse({"trigger-pages", "create-release-tag", "publish-main-snapshot", "publish-chart-snapshot", "build-spoa-image-main"} & selected)

    def test_train_cannot_be_reduced_by_schedule_or_reuse_variables(self):
        for kind in ("", "nightly", "weekly"):
            env = self.environment(CI_PIPELINE_SOURCE="merge_request_event", CI_COMMIT_BRANCH="",
                                   CI_MERGE_REQUEST_EVENT_TYPE="merge_train", SCHEDULE_KIND=kind)
            required = set(self.policy["common_jobs"] + self.policy["full_jobs"])
            self.assertFalse(required - selected_jobs(self.config, env))

    def test_post_merge_reuse_still_omits_the_test_matrix(self):
        env = self.environment(CI_PIPELINE_SOURCE="push", SCHEDULE_KIND="")
        selected = selected_jobs(self.config, env)
        self.assertIn("trigger-pages", selected)
        self.assertNotIn("test", selected)
        self.assertFalse({name for name in self.policy["full_jobs"] if name.startswith("test-")} & selected)

    def test_mirror_schedule_stays_isolated(self):
        self.assertEqual(selected_jobs(self.config, self.environment(MIRROR_IMAGES="true")), {"mirror-ingress-conformance-image"})

    def test_all_automatic_dependencies_are_present(self):
        for source, kind in (("schedule", "nightly"), ("pipeline", "nightly"), ("api", "nightly"),
                             ("web", "nightly"), ("schedule", "weekly"), ("api", "weekly")):
            selected = selected_jobs(self.config, self.environment(CI_PIPELINE_SOURCE=source, SCHEDULE_KIND=kind))
            available = selected | {instance for name in selected for instance in instances(self.config, name)}
            for name in selected:
                for need in expand(job(self.config, name).get("needs", []), self.config):
                    if isinstance(need, dict) and need.get("optional"):
                        continue
                    dependency = need if isinstance(need, str) else need["job"]
                    with self.subTest(source=source, kind=kind, job=name, dependency=dependency):
                        self.assertIn(dependency, available)


if __name__ == "__main__":
    unittest.main()
