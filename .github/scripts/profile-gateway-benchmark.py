"""Capture diagnostic profiles while the unchanged scale workload is growing."""

import concurrent.futures
import json
from pathlib import Path
import re
import subprocess
import time


def request(command, destination):
    result = subprocess.run(command, capture_output=True, timeout=70, check=False)
    destination.write_bytes(result.stdout)
    destination.with_suffix(destination.suffix + ".exit").write_text(str(result.returncode) + "\n")
    destination.with_suffix(destination.suffix + ".stderr").write_bytes(result.stderr)
    if result.returncode:
        raise RuntimeError(f"Diagnostic request failed: {destination.name}, exit {result.returncode}")


def capture_controller(kubectl, name, output):
    log = output / (name + "-port-forward.log")
    with log.open("wb") as handle:
        process = subprocess.Popen(kubectl + [
            "port-forward", "--address=127.0.0.1", "-n", "haptic", f"pod/{name}", ":8080",
        ], stdout=handle, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError(f"Diagnostic port-forward exited for {name}")
                match = re.search(r"Forwarding from 127\.0\.0\.1:(\d+) -> 8080", log.read_text())
                if match:
                    request(["curl", "--fail", "--silent", "--show-error", "--max-time", "60",
                             f"http://127.0.0.1:{match[1]}/debug/pprof/profile?seconds=30"],
                            output / (name + "-cpu.prof"))
                    return
                time.sleep(0.1)
            raise RuntimeError(f"Diagnostic port-forward was not ready for {name}")
        finally:
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


def capture(kubectl, root, count):
    output = root / "diagnostic-profiles" / f"routes-{count}"
    output.mkdir(parents=True)
    (output / "capture.json").write_text(json.dumps({
        "route_count": count, "started_at_epoch": time.time(), "purpose": "diagnostic-only",
    }) + "\n")
    pods = json.loads(subprocess.check_output(kubectl + [
        "get", "pods", "-n", "haptic", "-l", "app.kubernetes.io/component=controller", "-o", "json",
    ], timeout=60))["items"]
    endpoints = [("/debug/pprof/profile?seconds=30", "apiserver-cpu.prof"),
                 ("/metrics", "apiserver-metrics.prom")]
    if len(pods) != 2:
        raise RuntimeError(f"Expected both benchmark controllers, found {len(pods)}")
    for pod in pods:
        name = pod["metadata"]["name"]
        endpoints.append((f"/api/v1/namespaces/haptic/pods/{name}:9090/proxy/metrics", name + "-metrics.prom"))
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(endpoints) + len(pods)) as executor:
        futures = [executor.submit(request, kubectl + ["get", "--raw", endpoint], output / filename)
                   for endpoint, filename in endpoints]
        futures.extend(executor.submit(capture_controller, kubectl, pod["metadata"]["name"], output)
                       for pod in pods)
        for future in futures:
            future.result()
    (output / "complete.json").write_text(json.dumps({"profiles": 3, "pass": True}) + "\n")


def main():
    thresholds = iter((1000, 2500))
    threshold = next(thresholds)
    deadline = time.monotonic() + 3600
    while time.monotonic() < deadline:
        roots = list(Path("/tmp").glob("haptic-gateway-api-bench.*/result"))
        kubeconfigs = list(Path("/tmp").glob("haptic-gwbench-*.kubeconfig"))
        if len(roots) == 1 and len(kubeconfigs) == 1 and (roots[0] / "scale/route-counts.txt").exists():
            lines = (roots[0] / "scale/route-counts.txt").read_text().splitlines()
            if lines:
                count = int(lines[-1].split()[1])
                if count >= threshold:
                    kubectl = ["kubectl", "--kubeconfig", str(kubeconfigs[0]), "--request-timeout=60s"]
                    capture(kubectl, roots[0], count)
                    threshold = next(thresholds, None)
                    if threshold is None:
                        return
        time.sleep(5)
    raise RuntimeError("Scale workload did not reach both profiling thresholds")


if __name__ == "__main__":
    main()
