"""Capture diagnostic profiles while the unchanged scale workload is growing."""

import concurrent.futures
import json
from pathlib import Path
import subprocess
import time


def request(kubectl, endpoint, destination):
    result = subprocess.run(kubectl + ["get", "--raw", endpoint], capture_output=True, timeout=70, check=False)
    destination.write_bytes(result.stdout)
    destination.with_suffix(destination.suffix + ".exit").write_text(str(result.returncode) + "\n")
    destination.with_suffix(destination.suffix + ".stderr").write_bytes(result.stderr)


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
    for pod in pods:
        name = pod["metadata"]["name"]
        proxy = f"/api/v1/namespaces/haptic/pods/{name}"
        endpoints.extend([
            (proxy + ":8080/proxy/debug/pprof/profile?seconds=30", name + "-cpu.prof"),
            (proxy + ":9090/proxy/metrics", name + "-metrics.prom"),
        ])
    with concurrent.futures.ThreadPoolExecutor(max_workers=len(endpoints)) as executor:
        futures = [executor.submit(request, kubectl, endpoint, output / filename)
                   for endpoint, filename in endpoints]
        for future in futures:
            future.result()


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
