#!/usr/bin/env python3
import json
from pathlib import Path
import subprocess

root = Path('.pilot/integration-startup')
root.mkdir(exist_ok=True)
for pod in json.loads(Path('.pilot/integration-pods.json').read_text())['items']:
    namespace = pod['metadata']['namespace']
    if not namespace.startswith('test-'):
        continue
    target = root / namespace
    target.mkdir(exist_ok=True)
    (target / 'pod.json').write_text(json.dumps(pod, indent=2))
    for container in ['haproxy', 'agent']:
        result = subprocess.run([
            'docker', 'exec', 'haproxy-test-control-plane', 'kubectl',
            '--kubeconfig=/etc/kubernetes/admin.conf', 'logs', '-n', namespace,
            pod['metadata']['name'], '-c', container, '--timestamps'],
            capture_output=True, text=True, check=False)
        (target / (container + '.log')).write_text(result.stdout + result.stderr)
