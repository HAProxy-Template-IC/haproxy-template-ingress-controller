"""Run the existing scale regression gate with GitHub workflow history."""

import importlib
import json
import os
from pathlib import Path

from .github import GitHub
from .github_history import successful_job_metrics


TREND = importlib.import_module("scripts.scale-trend")


def main():
    current = json.loads(Path(TREND.METRICS_FILE).read_text())
    history = successful_job_metrics(
        GitHub(os.environ["GH_TOKEN"]), os.environ["GITHUB_REPOSITORY"], os.environ["GITHUB_RUN_ID"],
        workflow="nightly.yml", branch="main", job_name=TREND.JOB_NAME,
        artifact_name="nightly-scale-metrics", metrics_name=TREND.METRICS_FILE,
        max_runs=TREND.MAX_RUNS, max_workflows=TREND.MAX_PIPELINES)
    lines, failed = TREND.evaluate(current, history)
    print("\n".join(lines))
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
