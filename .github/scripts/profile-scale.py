#!/usr/bin/env python3
import concurrent.futures
import json
from pathlib import Path
import subprocess
import time
from urllib.error import HTTPError, URLError
from urllib.request import urlopen


ROOT = Path('.pilot/scale-profile')
ROOT.mkdir(parents=True, exist_ok=True)
FINISHED = Path('.pilot/test-finished')
KUBECTL = ['docker', 'exec', 'haptic-e2e-control-plane', 'kubectl',
           '--kubeconfig=/etc/kubernetes/admin.conf', '--request-timeout=55s']


def capture(index, pod):
    name = pod['metadata']['name']
    target = ROOT / name
    target.mkdir(exist_ok=True)
    (target / 'pod.json').write_text(json.dumps(pod, indent=2))
    debug_port = 18080 + index
    metrics_port = 19090 + index
    endpoints = [('metrics', 'metrics-before.txt'),
                 ('debug/pprof/profile?seconds=35', 'cpu.prof'),
                 ('debug/pprof/heap', 'heap.prof'),
                 ('debug/pprof/goroutine?debug=2', 'goroutines.txt'),
                 ('metrics', 'metrics-after.txt')]
    with (target / 'forward.log').open('w') as log:
        forward = subprocess.Popen([
            'kubectl', '--kubeconfig=/tmp/haproxy-e2e-kubeconfig', '-n', 'haptic',
            'port-forward', '--address=127.0.0.1', f'pod/{name}',
            f'{debug_port}:8080', f'{metrics_port}:9090'], stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 30
            while True:
                try:
                    with urlopen(f'http://127.0.0.1:{debug_port}/debug/pprof/heap', timeout=5) as response:
                        response.read()
                    break
                except (HTTPError, URLError, TimeoutError):
                    if time.monotonic() >= deadline or forward.poll() is not None:
                        raise
                    time.sleep(1)
            for endpoint, filename in endpoints:
                port = metrics_port if endpoint == 'metrics' else debug_port
                with urlopen(f'http://127.0.0.1:{port}/{endpoint}', timeout=55) as response:
                    (target / filename).write_bytes(response.read())
        finally:
            forward.terminate()
            forward.wait(timeout=10)


deadline = time.monotonic() + 900
while time.monotonic() < deadline and not FINISHED.exists():
    result = subprocess.run(KUBECTL + ['get', 'ingresses', '-A', '-o', 'json'],
                            capture_output=True, timeout=60, check=False)
    if result.returncode == 0:
        resources = json.loads(result.stdout)['items']
        if len(resources) >= 100:
            (ROOT / 'ingresses-at-capture.json').write_bytes(result.stdout)
            pods = json.loads(subprocess.check_output(KUBECTL + [
                'get', 'pods', '-n', 'haptic', '-l',
                'app.kubernetes.io/component=controller', '-o', 'json']))['items']
            with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
                futures = [executor.submit(capture, index, pod) for index, pod in enumerate(pods)]
                for future in futures:
                    future.result()
            break
    time.sleep(3)
