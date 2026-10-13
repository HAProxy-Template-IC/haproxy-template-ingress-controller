"""Balance independent verification suites using recorded runner durations."""

import argparse
import json
from pathlib import Path

import yaml


def lanes(durations, count, *, coverage=(), coverage_seconds=0, handoff_seconds=4):
    if count < 1 or not durations or any(value <= 0 for value in durations.values()):
        raise ValueError("Scheduling requires positive durations and runner capacity")
    if not set(coverage) <= set(durations):
        raise ValueError("Coverage producers must be scheduled suites")
    result = [[] for _ in range(min(count, len(durations)))]
    totals = [0] * len(result)
    ordered = sorted(durations, key=lambda name: (
        -(durations[name] + (coverage_seconds if name in coverage else 0)), name))
    for name in ordered:
        index = min(range(len(result)), key=lambda item: (totals[item], item))
        result[index].append(name)
        totals[index] += durations[name] + handoff_seconds
    return result


def schedule(workflow, weights):
    jobs = workflow["jobs"]
    suites = {job["name"]: key for key, job in jobs.items() if "CI_SUITE" in job.get("env", {})}
    durations = {name: row["seconds"] for name, row in weights["jobs"].items()}
    if set(suites) != set(durations):
        raise ValueError("Every suite needs exactly one recorded duration")
    coverage = {jobs[key]["name"] for key in jobs["coverage"]["needs"]}
    selected = lanes(durations, weights["runner_limit"] - 1, coverage=coverage,
                     coverage_seconds=weights["coverage_seconds"])
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
