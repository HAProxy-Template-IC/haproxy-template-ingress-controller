#!/usr/bin/env python3
import concurrent.futures
import json
from pathlib import Path
import subprocess
import time


ROOT = Path('.pilot/startup')
ROOT.mkdir(parents=True, exist_ok=True)
NODE = ['docker', 'exec', 'haptic-e2e-control-plane']
KUBECTL = NODE + ['kubectl', '--kubeconfig=/etc/kubernetes/admin.conf', '-n', 'haptic']


def capture(pod):
    name = pod['metadata']['name']
    address = pod['status']['podIP']
    target = ROOT / name
    target.mkdir(exist_ok=True)
    (target / 'pod.json').write_text(json.dumps(pod, indent=2))
    with (target / 'controller.log').open('w') as log:
        stream = subprocess.Popen(KUBECTL + ['logs', '-f', name, '-c', 'controller'], stdout=log, stderr=subprocess.STDOUT)
        try:
            for index in range(3):
                with (target / f'containers-{index}.json').open('wb') as output:
                    subprocess.run(NODE + ['crictl', 'stats', '-o', 'json'], stdout=output, stderr=subprocess.DEVNULL, timeout=15, check=False)
                for endpoint, filename in [('profile?seconds=35', f'cpu-{index}.prof'), ('heap', f'heap-{index}.prof')]:
                    with (target / filename).open('wb') as output:
                        subprocess.run(NODE + ['curl', '--fail', '--silent', '--show-error', '--max-time', '45', f'http://{address}:8080/debug/pprof/{endpoint}'], stdout=output, stderr=subprocess.DEVNULL, timeout=50, check=False)
        finally:
            stream.terminate()
            stream.wait(timeout=10)


seen = set()
deadline = time.monotonic() + 600
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
    pending = []
    while time.monotonic() < deadline and len(seen) < 2:
        result = subprocess.run(KUBECTL + ['get', 'pods', '-l', 'app.kubernetes.io/component=controller', '-o', 'json'], capture_output=True, timeout=15, check=False)
        if result.returncode == 0:
            for pod in json.loads(result.stdout)['items']:
                name = pod['metadata']['name']
                statuses = pod.get('status', {}).get('containerStatuses', [])
                running = any(status['name'] == 'controller' and 'running' in status.get('state', {}) for status in statuses)
                if name not in seen and running and pod['status'].get('podIP'):
                    seen.add(name)
                    pending.append(executor.submit(capture, pod))
        time.sleep(3)
    for future in pending:
        future.result()
