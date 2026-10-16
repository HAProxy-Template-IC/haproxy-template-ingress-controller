"""Preserve initial controller validation before E2E tests replace pods."""

from concurrent.futures import ThreadPoolExecutor
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import time


ROOT = Path(".pilot/initial-startup")
NAMESPACE = "haptic"
KUBECTL = ["docker", "exec", "haptic-e2e-control-plane", "kubectl",
           "--kubeconfig=/etc/kubernetes/admin.conf", "-n", NAMESPACE]
SPEC = importlib.util.spec_from_file_location("verify_startup", Path(__file__).with_name("verify-startup.py"))
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


def kubectl_prefixed(name, cri_log):
    lines, partial = [], []
    for record in cri_log.split("\n")[:-1]:
        _, stream, tag, content = record.split(" ", 3)
        if stream not in ("stdout", "stderr") or tag not in ("P", "F"):
            raise ValueError(f"{name}: malformed CRI log record: {record[:200]}")
        partial.append(content)
        if tag == "F":
            lines.append(f"[pod/{name}/controller] {''.join(partial)}\n")
            partial = []
    return "".join(lines)


def fetch_log(pod):
    # Read the node's CRI log file: `kubectl logs` through apiserver and kubelet stalled for minutes under load.
    name, uid = pod["metadata"]["name"], pod["metadata"]["uid"]
    path = f"/var/log/pods/{NAMESPACE}_{name}_{uid}/controller/0.log"
    cri_log = subprocess.check_output(["docker", "exec", pod["spec"]["nodeName"], "cat", path], text=True, timeout=30)
    return kubectl_prefixed(name, cri_log)


def capture(pod):
    name = pod["metadata"]["name"]
    (ROOT / f"{name}-initial.json").write_text(json.dumps(pod, indent=2) + "\n")
    subprocess.run(KUBECTL + ["wait", f"pod/{name}", "--for=condition=Ready", "--timeout=300s"],
                   check=True, timeout=315)
    ready = json.loads(subprocess.check_output(KUBECTL + ["get", "pod", name, "-o", "json"], timeout=30))
    (ROOT / f"{name}-ready.json").write_text(json.dumps(ready, indent=2) + "\n")
    if ready["metadata"]["uid"] != pod["metadata"]["uid"]:
        raise ValueError(f"{name}: initial controller pod was replaced before verification")
    log = fetch_log(ready)
    (ROOT / f"{name}.log").write_text(log)
    return log, ready


def main():
    ROOT.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    pending = {}
    with ThreadPoolExecutor(max_workers=2) as executor:
        while len(pending) < 2 and time.monotonic() - started < 600:
            result = subprocess.run(KUBECTL + ["get", "pods", "-l",
                                    "app.kubernetes.io/component=controller", "-o", "json"],
                                    capture_output=True, timeout=15, check=False)
            if result.returncode == 0:
                for pod in json.loads(result.stdout)["items"]:
                    name = pod["metadata"]["name"]
                    if name not in pending:
                        pending[name] = executor.submit(capture, pod)
            if Path(".pilot/test-finished").exists():
                break
            if len(pending) < 2:
                time.sleep(2)
        captured = [future.result() for future in pending.values()]
    logs = "".join(log for log, _ in captured)
    pods = {"items": [pod for _, pod in captured]}
    (ROOT / "controller-logs.txt").write_text(logs)
    (ROOT / "pods.json").write_text(json.dumps(pods, indent=2) + "\n")
    verdict = VERIFY.verify(logs, pods)
    expected = {"GOMAXPROCS": os.environ["PILOT_GOMAXPROCS"], "GOGC": os.environ["PILOT_GOGC"]}
    if os.environ.get("PILOT_VALIDATION_TMPFS") == "1":
        expected["TMPDIR"] = "/v"
    VERIFY.verify_environment(pods, expected)
    (ROOT / "verified-environment.json").write_text(json.dumps(expected, indent=2) + "\n")
    Path(".pilot/first-startup-verdict.json").write_text(json.dumps(verdict, indent=2) + "\n")
    print(json.dumps(verdict))


if __name__ == "__main__":
    main()
