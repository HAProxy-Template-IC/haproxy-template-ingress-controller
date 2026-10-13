"""Balance independent verification suites using recorded runner durations."""

import argparse
import json
from pathlib import Path

import yaml


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
    result = [[] for _ in range(min(count, len(groups)))]
    totals = [0] * len(result)
    ordered = sorted(groups, key=lambda group: (
        group[0] not in early, -sum(durations[name] + handoff_seconds for name in group), group))
    for group in ordered:
        index = min(range(len(result)), key=lambda item: (totals[item], item))
        result[index].extend(group)
        totals[index] += sum(durations[name] + handoff_seconds for name in group)
    return result


def schedule(workflow, weights):
    jobs = workflow["jobs"]
    suites = {job["name"]: key for key, job in jobs.items() if "CI_SUITE" in job.get("env", {})}
    durations = {name: row["seconds"] for name, row in weights["jobs"].items()}
    if set(suites) != set(durations):
        raise ValueError("Every suite needs exactly one recorded duration")
    coverage = {jobs[key]["name"] for key in jobs["coverage"]["needs"]}
    selected = lanes(durations, weights["runner_limit"] - 1,
                     early=coverage | set(weights["early_jobs"]), chains=weights["cache_reuse_chains"])
    for lane in selected:
        for index, name in enumerate(lane):
            job = jobs[suites[name]]
            job.pop("needs", None)
            if index:
                job["needs"] = suites[lane[index - 1]]
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
    print(f"Scheduled {sum(map(len, selected))} suites in {len(selected)} lanes, with one coverage runner reserved")


if __name__ == "__main__":
    main()
