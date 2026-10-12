"""Reject incomplete first startup validation even if a later retry passes."""

import json
from pathlib import Path
import re


RESULT = re.compile(
    r'^\[pod/([^/]+)/controller\].*msg="Test run completed" component=test-runner '
    r'total=(\d+) passed=(\d+) failed=(\d+) skipped=(\d+) duration=(\S+)',
    re.MULTILINE,
)


def verify(log, pods):
    controllers = [pod for pod in pods["items"]
                   if pod["metadata"].get("labels", {}).get("app.kubernetes.io/component") == "controller"]
    if len(controllers) != 2:
        raise ValueError(f"Expected two controller replicas, found {len(controllers)}")
    first = {}
    for match in RESULT.finditer(log):
        name, total, passed, failed, skipped, duration = match.groups()
        first.setdefault(name, {"total": int(total), "passed": int(passed), "failed": int(failed),
                                "skipped": int(skipped), "duration": duration})
    for pod in controllers:
        name = pod["metadata"]["name"]
        result = first.get(name)
        if result is None or result["total"] <= 0 or result["passed"] != result["total"] or result["failed"]:
            raise ValueError(f"{name}: first startup validation was incomplete or failed: {result}")
        statuses = [s for s in pod["status"].get("containerStatuses", []) if s["name"] == "controller"]
        if len(statuses) != 1 or not statuses[0].get("ready") or statuses[0].get("restartCount") != 0:
            raise ValueError(f"{name}: controller is not ready without a restart")
    return {pod["metadata"]["name"]: first[pod["metadata"]["name"]] for pod in controllers}


def main():
    result = verify(Path(".pilot/current-controller-logs.txt").read_text(),
                    json.loads(Path(".pilot/pods.json").read_text()))
    Path(".pilot/first-startup-verdict.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps(result))


if __name__ == "__main__":
    main()
