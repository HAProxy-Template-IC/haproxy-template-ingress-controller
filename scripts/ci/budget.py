"""Report available compute minutes after active work and release reserves."""

import argparse
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import json
import math
import sys

from .gitlab import GitLab, encoded


ACTIVE = ("created", "waiting_for_resource", "preparing", "waiting_for_callback",
          "pending", "running", "canceling", "manual", "scheduled")
RUNNER_FACTORS = {"small": 1, "medium": 2, "large": 3, "xlarge": 6, "2xlarge": 12}


def cost_factor(project, namespace, job):
    if namespace.get("plan") == "opensource" and project.get("visibility") == "public":
        return 0.5
    tags = job.get("tag_list", [])
    factors = [factor for size, factor in RUNNER_FACTORS.items()
               if f"saas-linux-{size}-amd64" in tags]
    if len(factors) != 1:
        raise ValueError(f"Unknown billing factor for {project['path_with_namespace']}: {job['name']}")
    return factors[0]


def billed_minutes(project, namespace, jobs):
    return sum((job.get("duration") or 0) / 60 * cost_factor(project, namespace, job)
               for job in jobs if job.get("duration"))


def remaining_minutes(group, namespace):
    usage = namespace["ci_minutes_usage"]
    included = group["shared_runners_minutes_limit"]
    purchased = group["extra_shared_runners_minutes_limit"]
    if included <= 0:
        raise ValueError("A finite monthly compute allowance is required for budget accounting")
    return max(0, included - usage["monthly_minutes_used"]) + max(0, purchased - usage["purchased_minutes_used"])


def forecast_jobs(project, namespace, jobs, history):
    total = 0
    for job in jobs:
        if job["status"] in {"skipped", "canceled"}:
            total += (job.get("duration") or 0) / 60 * cost_factor(project, namespace, job)
            continue
        if job["status"] == "manual" and job.get("allow_failure"):
            continue
        observed = job.get("duration") or 0
        if job["status"] in {"success", "failed"}:
            seconds = observed
        else:
            samples = history.get(job["name"], [])
            seconds = max(observed, max(samples) * 1.25 if samples else project["build_timeout"])
        total += seconds / 60 * cost_factor(project, namespace, job)
    return total


def pipeline_jobs(api, project_id, pipeline_id):
    return list(api.all(f"projects/{project_id}/pipelines/{pipeline_id}/jobs", include_retried="true"))


def inspect_project(api, project, namespace, samples):
    project_id = project["id"]
    project = api.get(f"projects/{project_id}")
    if project["builds_access_level"] == "disabled":
        return {"project": project["path_with_namespace"], "ci_disabled": True,
                "recent_pipeline_minutes": [], "reservations": []}
    history = {}
    completed = api.get(f"projects/{project_id}/pipelines", status="success", per_page=samples)
    costs = []
    for pipeline in completed:
        jobs = pipeline_jobs(api, project_id, pipeline["id"])
        costs.append(billed_minutes(project, namespace, jobs))
        for job in jobs:
            if job.get("duration"):
                history.setdefault(job["name"], []).append(job["duration"])
    active = {}
    for status in ACTIVE:
        for source in (None, "parent_pipeline"):
            params = {"status": status}
            if source:
                params["source"] = source
            for pipeline in api.all(f"projects/{project_id}/pipelines", **params):
                active[pipeline["id"]] = pipeline
    reservations = []
    for pipeline in active.values():
        jobs = pipeline_jobs(api, project_id, pipeline["id"])
        if not jobs and pipeline["source"] != "external":
            raise ValueError(f"Pipeline {pipeline['web_url']} has no jobs available for forecasting")
        reservation = forecast_jobs(project, namespace, jobs, history)
        reservations.append({"project": project["path_with_namespace"], "pipeline": pipeline["id"],
                             "status": pipeline["status"], "minutes": math.ceil(reservation),
                             "url": pipeline["web_url"]})
    return {"project": project["path_with_namespace"], "recent_pipeline_minutes": costs,
            "reservations": reservations}


def report(group, namespace, projects, candidate_minutes, release_reserve, recovery_reserve):
    reservations = [row for project in projects for row in project["reservations"]]
    committed = sum(row["minutes"] for row in reservations)
    remaining = remaining_minutes(group, namespace)
    available = remaining - committed - release_reserve - recovery_reserve
    return {"measured_at": datetime.now(timezone.utc).isoformat(),
            "remaining_minutes": remaining, "active_pipeline_reservations": reservations,
            "reserved_active_minutes": committed, "release_reserve_minutes": release_reserve,
            "recovery_reserve_minutes": recovery_reserve, "available_for_candidate_minutes": available,
            "candidate_minutes": candidate_minutes, "fits": candidate_minutes <= available,
            "history": projects,
            "accounting": "Active pipelines reserve their whole projected cost, including already billed work, to cover reporting lag. This report does not allocate funds or start pipelines."}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--namespace", default="haproxy-haptic")
    parser.add_argument("--candidate-minutes", type=float, required=True)
    parser.add_argument("--release-reserve", type=float, default=600)
    parser.add_argument("--recovery-reserve", type=float, default=600)
    parser.add_argument("--samples", type=int, default=3)
    args = parser.parse_args()
    if any(not math.isfinite(value) or value < 0 for value in
           [args.candidate_minutes, args.release_reserve, args.recovery_reserve]) or not 1 <= args.samples <= 100:
        parser.error("minutes must be finite and nonnegative; samples must be between 1 and 100")
    api = GitLab()
    namespace = api.get(f"namespaces/{encoded(args.namespace)}")
    group = api.get(f"groups/{namespace['id']}")
    projects = list(api.all(f"groups/{namespace['id']}/projects", include_subgroups="true", with_shared="false"))
    with ThreadPoolExecutor(max_workers=6) as executor:
        inspected = list(executor.map(lambda p: inspect_project(api, p, namespace, args.samples), projects))
    result = report(group, namespace, inspected, args.candidate_minutes, args.release_reserve, args.recovery_reserve)
    print(json.dumps(result, indent=2))
    return 0 if result["fits"] else 2


if __name__ == "__main__":
    sys.exit(main())
