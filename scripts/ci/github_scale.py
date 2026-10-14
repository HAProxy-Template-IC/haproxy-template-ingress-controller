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
    api = GitHub(os.environ["GH_TOKEN"])
    repository = os.environ["GITHUB_REPOSITORY"]
    run_id = os.environ["GITHUB_RUN_ID"]
    run = api.get(f"repos/{repository}/actions/runs/{run_id}")
    if run["id"] != int(run_id) or run["head_sha"] != os.environ["GITHUB_SHA"]:
        raise ValueError("Scale history workflow does not match the current revision")
    history = successful_job_metrics(
        api, repository, run_id,
        workflow=str(run["workflow_id"]), branch="main",
        job_name=os.environ.get("SCALE_JOB_NAME", TREND.JOB_NAME),
        artifact_name="nightly-scale-metrics", metrics_name=TREND.METRICS_FILE,
        max_runs=TREND.MAX_RUNS, max_workflows=TREND.MAX_PIPELINES,
        events=("push", "schedule", "workflow_dispatch"))
    lines, failed = TREND.evaluate(current, history)
    print("\n".join(lines))
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
