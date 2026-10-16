"""Balance independent verification suites using recorded runner durations."""

import argparse
import datetime
import json
import os
import re
from collections import defaultdict
from pathlib import Path

import yaml

from .github import GitHub, jobs_for_run

JOB_PREFIX = "full / "
HANDOFF_SECONDS = 4
MIN_RECORDED_RUNS = 5


def balance_groups(groups, durations, count, early, handoff_seconds):
    costs = [sum(durations[name] + handoff_seconds for name in group) for group in groups]
    result = [[] for _ in range(min(count, len(groups)))]
    totals = [0] * len(result)
    for index, cost in enumerate(costs):
        lane = min(range(len(result)), key=lambda item: (totals[item], item))
        result[lane].append(index)
        totals[lane] += cost

    def fits(lane):
        return sum(groups[index][0] in early for index in lane) <= 1

    while True:
        score = sorted(totals, reverse=True)
        choice = None
        for left, first in enumerate(result):
            for right, second in enumerate(result):
                if left == right:
                    continue
                for give in first:
                    for take in [None] + second:
                        if take is None and len(first) == 1:
                            continue
                        after_left = [index for index in first if index != give]
                        after_right = [index for index in second if index != take] + [give]
                        if take is not None:
                            after_left.append(take)
                        if not fits(after_left) or not fits(after_right):
                            continue
                        delta = costs[give] - (costs[take] if take is not None else 0)
                        trial = totals.copy()
                        trial[left] -= delta
                        trial[right] += delta
                        candidate = sorted(trial, reverse=True)
                        if candidate < score:
                            score = candidate
                            choice = left, right, after_left, after_right, trial
        if choice is None:
            break
        left, right, result[left], result[right], totals = choice
    return [[name for index in sorted(lane, key=lambda item: (
        groups[item][0] not in early, -costs[item], groups[item])) for name in groups[index]]
        for lane in result]


def lanes(durations, count, *, early=(), chains=(), handoff_seconds=HANDOFF_SECONDS):
    if count < 1 or not durations or any(value <= 0 for value in durations.values()):
        raise ValueError("Scheduling requires positive durations and runner capacity")
    if not set(early) <= set(durations) or len(early) > count:
        raise ValueError("Early suites must fit the available runner capacity")
    chained = [name for chain in chains for name in chain]
    if (any(len(chain) < 2 for chain in chains) or len(chained) != len(set(chained))
            or not set(chained) <= set(durations)):
        raise ValueError("Cache reuse chains must contain distinct scheduled suites")
    if set(early) & {name for chain in chains for name in chain[1:]}:
        raise ValueError("Early suites cannot wait for another suite")
    groups = [list(chain) for chain in chains] + [[name] for name in durations if name not in chained]
    ordered = sorted(groups, key=lambda group: (
        group[0] not in early, -sum(durations[name] + handoff_seconds for name in group), group))
    return balance_groups(ordered, durations, count, early, handoff_seconds)


def schedule(workflow, weights):
    jobs = workflow["jobs"]
    suites = {job["name"]: key for key, job in jobs.items() if "CI_SUITE" in job.get("env", {})}
    aggregation = jobs["coverage"]["name"]
    suites[aggregation] = "coverage"
    durations = {name: row["seconds"] for name, row in weights["jobs"].items()}
    if set(suites) != set(durations):
        raise ValueError("Every suite needs exactly one recorded duration")
    coverage_inputs = jobs["coverage"]["needs"]
    coverage = {jobs[key]["name"] for key in coverage_inputs}
    last_producer = max(coverage, key=lambda name: (durations[name], name))
    chains = weights["cache_reuse_chains"] + [[last_producer, aggregation]]
    selected = lanes(durations, weights["runner_limit"],
                     early=coverage | set(weights["early_jobs"]), chains=chains)
    for lane in selected:
        for index, name in enumerate(lane):
            job = jobs[suites[name]]
            job.pop("needs", None)
            if index:
                job["needs"] = suites[lane[index - 1]]
    jobs["coverage"]["needs"] = coverage_inputs
    ordered = [suites[name] for depth in range(max(map(len, selected)))
               for lane in selected for name in lane[depth:depth + 1]]
    ordered += [key for key in jobs if key not in ordered]
    workflow["jobs"] = {key: jobs[key] for key in ordered}
    jobs["verified"]["needs"] = [key for key in ordered if key != "verified"]
    return selected


def record(weights, runs, prefix=JOB_PREFIX):
    """Weight each job by its upper quartile, not median: a lane runs several jobs, so medians under-plan it."""
    if len(runs) < MIN_RECORDED_RUNS:
        raise ValueError(f"Record at least {MIN_RECORDED_RUNS} successful runs; fewer make the upper quartile their maximum")
    samples = defaultdict(list)
    for run, jobs in runs:
        if run["conclusion"] != "success":
            raise ValueError(f"Run {run['id']} did not succeed; its durations cannot weight the schedule")
        for job in jobs:
            if job["conclusion"] == "success" and job["name"].startswith(prefix):
                started, completed = (datetime.datetime.fromisoformat(job[key])
                                      for key in ("started_at", "completed_at"))
                samples[job["name"].removeprefix(prefix)].append((completed - started).total_seconds())
    for name in {name for name in weights["jobs"].keys() & samples.keys() if len(samples[name]) >= MIN_RECORDED_RUNS}:
        weights["jobs"][name] = {"seconds": round(sorted(samples[name])[len(samples[name]) * 3 // 4])}
    weights["source_runs"] = [run["id"] for run, _ in runs]
    return weights


def longest_lane_seconds(selected, durations, handoff_seconds=HANDOFF_SECONDS):
    return max(sum(durations[name] + handoff_seconds for name in lane) for lane in selected)


PINNED_ACTION = re.compile(r"(uses: (\S+@[0-9a-f]{40}))( # \S+)?$", re.M)


def keep_version_comments(original, dumped):
    """Re-attach the version comments Renovate tracks SHA-pinned actions by; YAML dumping drops them."""
    versions = {ref: comment for _, ref, comment in PINNED_ACTION.findall(original) if comment}
    return PINNED_ACTION.sub(lambda match: match[1] + versions.get(match[2], ""), dumped)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workflow", type=Path, default=Path(".github/workflows/ci-speed-proof.yml"))
    parser.add_argument("--weights", type=Path, default=Path(".github/job-duration-weights.json"))
    parser.add_argument("--record", nargs="+", type=int, metavar="RUN_ID",
                        help="re-measure the weights from these successful CI runs first")
    args = parser.parse_args()
    weights = json.loads(args.weights.read_text())
    if args.record:
        api = GitHub(os.environ["GH_TOKEN"])
        repository = json.loads(Path(".github/ci-verification.json").read_text())["repository"]
        runs = [(run, jobs_for_run(api, repository, run))
                for run in (api.get(f"repos/{repository}/actions/runs/{run_id}") for run_id in args.record)]
        weights = record(weights, runs)
    text = args.workflow.read_text()
    workflow = yaml.safe_load(text)
    selected = schedule(workflow, weights)
    if args.record:
        args.weights.write_text(json.dumps(weights, indent=2) + "\n")
    args.workflow.write_text(keep_version_comments(text, yaml.safe_dump(workflow, sort_keys=False, width=120)))
    longest = longest_lane_seconds(selected, {name: row["seconds"] for name, row in weights["jobs"].items()})
    print(f"Scheduled {sum(map(len, selected))} jobs in {len(selected)} lanes, including coverage aggregation; "
          f"longest lane {longest // 60}m{longest % 60:02d}s")


if __name__ == "__main__":
    main()
