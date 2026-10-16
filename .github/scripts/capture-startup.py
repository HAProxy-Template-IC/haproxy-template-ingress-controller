"""Preserve initial controller validation before E2E tests replace pods."""

from concurrent.futures import ThreadPoolExecutor
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import time


ROOT = Path(".pilot/initial-startup")
KUBECTL = ["docker", "exec", "haptic-e2e-control-plane", "kubectl",
           "--kubeconfig=/etc/kubernetes/admin.conf", "-n", "haptic"]
SPEC = importlib.util.spec_from_file_location("verify_startup", Path(__file__).with_name("verify-startup.py"))
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)


def fetch_log(name, attempts=3):
    # The kubelet log stream can stall while both replicas run their startup validation tests.
    for attempt in range(1, attempts + 1):
        try:
            return subprocess.check_output(KUBECTL + ["logs", name, "--container=controller", "--prefix", "--tail=-1"],
                                           text=True, timeout=30)
        except subprocess.TimeoutExpired:
            if attempt == attempts:
                raise
            print(f"{name}: kubectl logs timed out (attempt {attempt}), retrying", flush=True)


def capture(pod):
    name = pod["metadata"]["name"]
    (ROOT / f"{name}-initial.json").write_text(json.dumps(pod, indent=2) + "\n")
    subprocess.run(KUBECTL + ["wait", f"pod/{name}", "--for=condition=Ready", "--timeout=300s"],
                   check=True, timeout=315)
    log = fetch_log(name)
    (ROOT / f"{name}.log").write_text(log)
    ready = json.loads(subprocess.check_output(KUBECTL + ["get", "pod", name, "-o", "json"], timeout=30))
    (ROOT / f"{name}-ready.json").write_text(json.dumps(ready, indent=2) + "\n")
    if ready["metadata"]["uid"] != pod["metadata"]["uid"]:
        raise ValueError(f"{name}: initial controller pod was replaced before verification")
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
