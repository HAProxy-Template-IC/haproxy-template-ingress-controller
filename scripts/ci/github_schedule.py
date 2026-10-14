"""Balance independent verification suites using recorded runner durations."""

import argparse
import json
from pathlib import Path

import yaml


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


def lanes(durations, count, *, early=(), chains=(), handoff_seconds=4):
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workflow", type=Path, default=Path(".github/workflows/ci-speed-proof.yml"))
    parser.add_argument("--weights", type=Path, default=Path(".github/job-duration-weights.json"))
    args = parser.parse_args()
    workflow = yaml.safe_load(args.workflow.read_text())
    selected = schedule(workflow, json.loads(args.weights.read_text()))
    args.workflow.write_text(yaml.safe_dump(workflow, sort_keys=False, width=120))
    print(f"Scheduled {sum(map(len, selected))} jobs in {len(selected)} lanes, including coverage aggregation")


if __name__ == "__main__":
    main()
