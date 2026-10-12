#!/usr/bin/env python3
import concurrent.futures
import json
from pathlib import Path
import subprocess
import time


ROOT = Path('.pilot/scale-profile')
ROOT.mkdir(parents=True, exist_ok=True)
FINISHED = Path('.pilot/test-finished')
KUBECTL = ['docker', 'exec', 'haptic-e2e-control-plane', 'kubectl',
           '--kubeconfig=/etc/kubernetes/admin.conf', '--request-timeout=55s']


def capture(pod):
    name = pod['metadata']['name']
    target = ROOT / name
    target.mkdir(exist_ok=True)
    (target / 'pod.json').write_text(json.dumps(pod, indent=2))
    prefix = f'/api/v1/namespaces/haptic/pods/{name}'
    endpoints = [('metrics', 'metrics-before.txt'),
                 ('debug/pprof/profile?seconds=35', 'cpu.prof'),
                 ('debug/pprof/heap', 'heap.prof'),
                 ('debug/pprof/goroutine?debug=2', 'goroutines.txt'),
                 ('metrics', 'metrics-after.txt')]
    for endpoint, filename in endpoints:
        port = 9090 if endpoint == 'metrics' else 8080
        with (target / filename).open('wb') as output:
            subprocess.run(KUBECTL + ['get', '--raw', f'{prefix}:{port}/proxy/{endpoint}'],
                           stdout=output, check=True, timeout=60)


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
                list(executor.map(capture, pods))
            break
    time.sleep(3)
