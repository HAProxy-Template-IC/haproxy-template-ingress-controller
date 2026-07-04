#!/usr/bin/env bash
# ci-report-failure.sh — turn a failed main/nightly pipeline into a tracked issue.
#
# Runs in the .post stage (when: on_failure) of default-branch and scheduled
# pipelines. Failed jobs are fingerprinted by their sorted base names; an open
# issue carrying the same fingerprint gets a comment (occurrence count grows,
# artifacts stay linked), otherwise a new `needs-triage` + `ci-failure` issue
# is filed. That drops CI failures into the same triage funnel as user-filed
# issues instead of leaving them as red Xs nobody owns.
#
# Requires HAPTIC_CI_ISSUE_TOKEN (project access token, api scope) as a
# protected CI/CD variable. Degrades to a warning when absent so forks and
# local runs never hard-fail on the reporter itself.
set -euo pipefail

if [ -z "${HAPTIC_CI_ISSUE_TOKEN:-}" ]; then
  echo "ci-report-failure: HAPTIC_CI_ISSUE_TOKEN not set — skipping issue filing." >&2
  exit 0
fi

python3 - <<'PY'
import hashlib
import json
import os
import re
import sys
import urllib.parse
import urllib.request

api = os.environ["CI_API_V4_URL"]
project = os.environ["CI_PROJECT_ID"]
pipeline = os.environ["CI_PIPELINE_ID"]
pipeline_url = os.environ.get("CI_PIPELINE_URL", "")
source = os.environ.get("CI_PIPELINE_SOURCE", "unknown")
token = os.environ["HAPTIC_CI_ISSUE_TOKEN"]

def call(method, path, body=None, params=None):
    url = f"{api}/projects/{project}/{path}"
    if params:
        url += "?" + urllib.parse.urlencode(params, doseq=True)
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("PRIVATE-TOKEN", token)
    if data:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)

# Collect failed jobs of this pipeline (the reporter itself runs in .post,
# after every other job has finished). allow_failure jobs (the canary) are
# reported too but flagged — they fail the fingerprint into a separate bucket
# so canary breakage never masks a product failure.
jobs = call("GET", f"pipelines/{pipeline}/jobs", params={"scope[]": "failed", "per_page": 100})
if not jobs:
    print("ci-report-failure: no failed jobs found; nothing to report.")
    sys.exit(0)

def base_name(name):
    # Strip shard ("job 3/4") and matrix ("job: [v1.1.0]") suffixes so the
    # fingerprint is stable across which shard happened to fail.
    return re.sub(r"(\s+\d+/\d+|:\s*\[.*\])$", "", name).strip()

hard = sorted({base_name(j["name"]) for j in jobs if not j.get("allow_failure")})
soft = sorted({base_name(j["name"]) for j in jobs if j.get("allow_failure")})
names = hard or soft
kind = "failure" if hard else "canary"
fingerprint = hashlib.sha256(("|".join(names) + "|" + kind).encode()).hexdigest()[:16]
marker = f"ci-fingerprint:{fingerprint}"

job_lines = "\n".join(
    f"- [`{j['name']}`]({j['web_url']}) — {j.get('stage', '?')}"
    + (" *(allowed to fail)*" if j.get("allow_failure") else "")
    for j in jobs
)
occurrence = (
    f"Pipeline [{pipeline}]({pipeline_url}) (source: `{source}`) failed:\n\n"
    f"{job_lines}\n\n"
    "Job pages carry the `debug-logs/` diagnostics artifacts (controller, "
    "HAProxy, DPA, events, network)."
)

# List open ci-failure issues and match the exact marker client-side —
# GitLab's full-text `search` tokenizes and lags on fresh issues, which
# would either spam duplicates or miss the match.
candidates = call("GET", "issues", params={
    "state": "opened", "labels": "ci-failure", "per_page": 100,
})
existing = [i for i in candidates if marker in (i.get("description") or "")]
if existing:
    iid = existing[0]["iid"]
    call("POST", f"issues/{iid}/notes", body={"body": f"Recurred.\n\n{occurrence}"})
    print(f"ci-report-failure: recurrence noted on existing issue #{iid} ({marker})")
else:
    title_kind = "Canary failure" if kind == "canary" else "CI failure on main"
    issue = call("POST", "issues", body={
        "title": f"{title_kind}: {', '.join(names)}",
        "labels": "needs-triage,ci-failure",
        "description": (
            f"Automated report for a failed {source} pipeline on the default branch.\n\n"
            f"{occurrence}\n\n"
            "Per the no-flakiness policy: investigate to root cause; never retry as a workaround.\n\n"
            f"<!-- {marker} -->\n`{marker}`"
        ),
    })
    print(f"ci-report-failure: filed issue #{issue['iid']} ({marker})")
PY
