"""Dispatch nightly checks only when main differs from the previous nightly."""

import json
import os
import urllib.request

from .gitlab import GitLab, encoded


PIPELINE_NAME = "Nightly checks"


def previous_nightly(api, project, branch):
    for pipeline in api.all(f"projects/{project}/pipelines", ref=branch, name=PIPELINE_NAME,
                            order_by="id", sort="desc"):
        if pipeline["status"] not in {"canceled", "skipped"}:
            return pipeline
    return None


def trigger(api_url, project, branch, token):
    data = json.dumps({"ref": branch, "token": token,
                       "variables": {"SCHEDULE_KIND": "nightly"}}).encode()
    request = urllib.request.Request(f"{api_url}/projects/{project}/trigger/pipeline", data=data,
                                     headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=60) as response:
        return json.load(response)


def dispatch(api, env, start=trigger):
    if env.get("CI_PIPELINE_SOURCE") != "schedule" or env.get("SCHEDULE_KIND") != "nightly":
        raise ValueError("Nightly dispatch requires the nightly schedule")
    branch = env["CI_DEFAULT_BRANCH"]
    if env.get("CI_COMMIT_BRANCH") != branch:
        raise ValueError("The nightly schedule must target the default branch")
    project = env["CI_PROJECT_ID"]
    head = api.get(f"projects/{project}/repository/branches/{encoded(branch)}")["commit"]["id"]
    previous = previous_nightly(api, project, branch)
    if previous and previous["sha"] == head:
        print(f"No new commits on {branch}; nightly {previous['id']} already targets {head} ({previous['status']}).")
        return None
    pipeline = start(env["CI_API_V4_URL"], project, branch, env["CI_JOB_TOKEN"])
    print(f"Nightly checks started for {pipeline['sha']}: {pipeline['web_url']}")
    return pipeline


def main():
    dispatch(GitLab(os.environ["CI_API_V4_URL"]), os.environ)


if __name__ == "__main__":
    main()
