#!/usr/bin/env python3
"""Compare this run's scale-metrics.json against the rolling median of recent
successful nightly-scale runs on main that ran on the same runner CPU model.

Exit 1 on a hard-corridor regression, a missing CPU model, or an API error.
"""

import json
import os
import statistics
import sys
import urllib.error
import urllib.parse
import urllib.request

JOB_NAME = "nightly-scale"
METRICS_FILE = "scale-metrics.json"
# Successful runs fetched from history; the same-model pool is cut from these.
MAX_RUNS = 14
MAX_PIPELINES = 60
WINDOW = 7
MIN_BASELINES = 3
WARN_FRACTION = 0.20
# Hard corridor (FAIL): how long a routine change takes to go live at scale,
# and the controller's memory footprint.
HARD = {"change_convergence_seconds_p95": 0.50, "controller_rss_bytes": 0.50}
# WARN only: context metrics that also move with legitimate changes.
SOFT = (
    "change_convergence_seconds_median",
    "seed_to_converged_seconds",
    "controller_container_cpu_seconds_delta",
    "haproxy_reloads_total_delta",
    "config_lines",
)


def get(url, headers=None):
    req = urllib.request.Request(url, headers=headers or {})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp), resp.headers.get("X-Next-Page", "")


def paged(url, params):
    page = "1"
    while page:
        body, page = get(url + "?" + urllib.parse.urlencode({**params, "page": page}, doseq=True))
        yield from body


def fetch_history(api, project, job_token, current_pipeline):
    """Newest-first metrics of successful nightly-scale jobs on main.

    Not `jobs/artifacts/main`: that only searches the latest successful main
    pipeline, usually a push pipeline without this job (#256). Listing is
    anonymous because CI_JOB_TOKEN can't list pipelines; the project is public.
    """
    base = f"{api}/projects/{project}"
    runs = []
    pipelines = paged(f"{base}/pipelines", {"ref": "main", "source": "schedule", "per_page": 30})
    for n, pipeline in enumerate(pipelines):
        if n >= MAX_PIPELINES or len(runs) >= MAX_RUNS:
            break
        if str(pipeline["id"]) == str(current_pipeline):
            continue
        for job in paged(f"{base}/pipelines/{pipeline['id']}/jobs", {"scope[]": "success", "per_page": 100}):
            if job["name"] != JOB_NAME:
                continue
            try:
                metrics, _ = get(f"{base}/jobs/{job['id']}/artifacts/{METRICS_FILE}",
                                 {"JOB-TOKEN": job_token} if job_token else None)
            except urllib.error.HTTPError as err:
                if err.code == 404:  # artifact expired
                    continue
                raise
            metrics["_job_id"] = job["id"]
            runs.append(metrics)
    return runs


def show(value):
    return f"{value:.0f}" if value >= 1000 else f"{value:g}"


def numeric(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool) and value > 0


def evaluate(cur, history):
    """Return (report lines, failed)."""
    model = cur.get("runner_cpu_model")
    if not model:
        return [f"TREND FAIL: {METRICS_FILE} has no runner_cpu_model; runs on different CPUs are not comparable."], True
    lines = [f"runner: {model} ({cur.get('runner_cpu_count', '?')} CPUs)"]
    others = sorted({r.get("runner_cpu_model", "unrecorded") for r in history} - {model})
    if others:
        lines.append(f"skipped baselines from other CPU models: {', '.join(others)}")
    pool = [r for r in history if r.get("runner_cpu_model") == model][:WINDOW]
    if len(pool) < MIN_BASELINES:
        lines.append(f"TREND UNGATED: {len(pool)} of {MIN_BASELINES} required baselines on this CPU model; "
                     "this run becomes one.")
        return lines, False
    lines.append(f"baseline: median of {len(pool)} runs (jobs {', '.join(str(r['_job_id']) for r in pool)})")

    failed = False
    for key in (*HARD, *SOFT):
        values = [r[key] for r in pool if numeric(r.get(key))]
        if len(values) < MIN_BASELINES or not numeric(cur.get(key)):
            lines.append(f"trend: {key}: no comparable baseline ({len(values)} values)")
            continue
        median = statistics.median(values)
        change = cur[key] / median - 1
        detail = f"{key}: median {show(median)} -> {show(cur[key])} ({change * 100:+.0f}%)"
        if key in HARD and change > HARD[key]:
            lines.append(f"TREND FAIL: {detail} > +{HARD[key] * 100:.0f}%")
            failed = True
        elif change > WARN_FRACTION:
            lines.append(f"TREND WARN: {detail}")
        else:
            lines.append(f"trend ok: {detail}")
    return lines, failed


def main():
    with open(METRICS_FILE) as f:
        cur = json.load(f)
    history = fetch_history(
        os.environ["CI_API_V4_URL"],
        os.environ["CI_PROJECT_ID"],
        os.environ.get("CI_JOB_TOKEN", ""),
        os.environ.get("CI_PIPELINE_ID", ""),
    )
    lines, failed = evaluate(cur, history)
    print("\n".join(lines))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
