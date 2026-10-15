"""Compare a scale run's metrics against the rolling median of recent successful
runs on the same runner CPU model; scripts/ci/github_scale.py collects the history.
"""

import statistics

METRICS_FILE = "scale-metrics.json"
# Successful runs fetched from history; the same-model pool is cut from these.
MAX_RUNS = 14
MAX_PIPELINES = 60
WINDOW = 7
MIN_BASELINES = 3
WARN_FRACTION = 0.20
# Hard corridor (FAIL): how long a routine change takes to route at scale,
# how long the HAProxyCfg takes to show it, and the controller's memory.
HARD = {
    "change_convergence_seconds_p95": 0.50,
    "haproxycfg_publication_seconds_p95": 0.50,
    "controller_rss_bytes": 0.50,
}
# WARN only: context metrics that also move with legitimate changes.
SOFT = (
    "change_convergence_seconds_median",
    "haproxycfg_publication_seconds_median",
    "seed_to_converged_seconds",
    "controller_container_cpu_seconds_delta",
    "haproxy_reloads_total_delta",
    "config_lines",
)


# Metrics whose meaning changed under the same key, with the
# scale_metrics_version that introduced the current meaning. Older runs
# measured something else and are not baselines for them.
REDEFINED = {
    "change_convergence_seconds_p95": 2,
    "change_convergence_seconds_median": 2,
}
# Metrics that older runs recorded, with the same meaning, under another key.
FORMER_KEYS = {
    "haproxycfg_publication_seconds_p95": "change_marker_seconds_p95",
    "haproxycfg_publication_seconds_median": "change_marker_seconds_median",
}


def baseline_value(run, key):
    if run.get("scale_metrics_version", 1) < REDEFINED.get(key, 1):
        return None
    return run.get(key, run.get(FORMER_KEYS.get(key)))


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
                     "comparison waits for matching history.")
        return lines, False
    lines.append(f"baseline: median of {len(pool)} runs (jobs {', '.join(str(r['_job_id']) for r in pool)})")

    failed = False
    for key in (*HARD, *SOFT):
        values = [v for v in (baseline_value(r, key) for r in pool) if numeric(v)]
        if len(values) < MIN_BASELINES or not numeric(cur.get(key)):
            prefix = "TREND UNGATED" if key in HARD else "trend"
            lines.append(f"{prefix}: {key}: no comparable baseline ({len(values)} values)")
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

