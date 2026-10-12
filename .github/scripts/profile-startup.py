#!/usr/bin/env python3
import concurrent.futures
import json
import os
from pathlib import Path
import subprocess
import time
from urllib.error import HTTPError, URLError
from urllib.request import urlopen


ROOT = Path('.pilot/startup')
ROOT.mkdir(parents=True, exist_ok=True)
NODE = ['docker', 'exec', 'haptic-e2e-control-plane']
KUBECTL = NODE + ['kubectl', '--kubeconfig=/etc/kubernetes/admin.conf', '-n', 'haptic']


def capture(pod, index):
    name = pod['metadata']['name']
    target = ROOT / name
    target.mkdir(exist_ok=True)
    (target / 'pod.json').write_text(json.dumps(pod, indent=2))
    with (target / 'controller.log').open('w') as log:
        stream = subprocess.Popen(KUBECTL + ['logs', '-f', name, '-c', 'controller'], stdout=log, stderr=subprocess.STDOUT)
        forward_port = 18080 + index
        node = json.loads(subprocess.check_output(['docker', 'inspect', 'haptic-e2e-control-plane']))[0]
        node_ip = node['NetworkSettings']['Networks']['kind']['IPAddress']
        with (target / 'capture-errors.log').open('w') as errors:
            forward = subprocess.Popen(KUBECTL + ['port-forward', '--address=0.0.0.0', f'pod/{name}', f'{forward_port}:8080'], stdout=errors, stderr=subprocess.STDOUT)
            try:
                prefix = f'http://{node_ip}:{forward_port}/debug/pprof/'
                deadline = time.monotonic() + 60
                while time.monotonic() < deadline:
                    try:
                        with urlopen(prefix + 'heap', timeout=5) as response:
                            response.read()
                        break
                    except (HTTPError, URLError, TimeoutError) as error:
                        errors.write(str(error) + '\n')
                        errors.flush()
                        time.sleep(2)
                else:
                    raise RuntimeError(f'{name}: profiling port-forward did not become available')
                for sample in range(4):
                    with (target / f'containers-{sample}.json').open('wb') as output:
                        subprocess.run(NODE + ['crictl', 'stats', '-o', 'json'], stdout=output, stderr=errors, timeout=15, check=False)
                    if os.environ.get('PILOT_CAPTURE_PROFILES', '1') == '0':
                        time.sleep(35)
                        continue
                    for endpoint, filename in [('profile?seconds=35', f'cpu-{sample}.prof'), ('heap', f'heap-{sample}.prof')]:
                        with urlopen(prefix + endpoint, timeout=45) as response:
                            (target / filename).write_bytes(response.read())
            finally:
                forward.terminate()
                forward.wait(timeout=10)
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
                    pending.append(executor.submit(capture, pod, len(seen) - 1))
        time.sleep(3)
    for future in pending:
        future.result()
