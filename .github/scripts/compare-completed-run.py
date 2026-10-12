"""Compare a complete successful workflow with the recorded GitLab baseline."""

import argparse
import datetime
import importlib.util
import json
from pathlib import Path


SPEC = importlib.util.spec_from_file_location("verify_run", Path(__file__).with_name("verify-run.py"))
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


def timestamp(value):
    result = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("Workflow timestamps must include a timezone")
    return result


def compare(run, jobs, manifest):
    if run["status"] != "completed" or run["conclusion"] != "success":
        raise ValueError("Speed proof requires a completed successful workflow")
    if run["run_attempt"] != 1:
        raise ValueError("Speed proof requires a fresh workflow, without rerunning failed jobs")
    checked = VERIFY.verify(run, jobs, manifest["required_jobs"] + ["verified"],
                            repository=manifest["repository"], sha=run["head_sha"], attempt=1)
    started = timestamp(run["created_at"])
    finished = max(timestamp(job["completed_at"]) for job in jobs)
    for job in jobs:
        if not started <= timestamp(job["started_at"]) <= timestamp(job["completed_at"]):
            raise ValueError("Job timestamps do not belong to the measured workflow interval")
    elapsed = (finished - started).total_seconds()
    baseline = manifest["baseline"]["wall_seconds"]
    if baseline <= 0:
        raise ValueError("GitLab baseline duration must be positive")
    return {
        "repository": run["repository"]["full_name"], "sha": run["head_sha"],
        "run_id": run["id"], "run_url": run["html_url"], "run_attempt": 1,
        "verified_jobs": len(checked), "created_at": started.isoformat(),
        "completed_at": finished.isoformat(), "github_wall_seconds": elapsed,
        "gitlab_baseline": manifest["baseline"], "seconds_saved": baseline - elapsed,
        "percent_faster": 100 * (baseline - elapsed) / baseline,
        "speed_requirement_passed": elapsed <= baseline,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run", type=Path, required=True)
    parser.add_argument("--jobs", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, default=Path(".github/ci-verification.json"))
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    report = compare(json.loads(args.run.read_text()), json.loads(args.jobs.read_text())["jobs"],
                     json.loads(args.manifest.read_text()))
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))
    raise SystemExit(0 if report["speed_requirement_passed"] else 1)


if __name__ == "__main__":
    main()
