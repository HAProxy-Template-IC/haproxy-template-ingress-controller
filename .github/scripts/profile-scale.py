#!/usr/bin/env python3
"""Profile both controllers and the node's containers in back-to-back windows until the test finishes."""

import concurrent.futures
import datetime
import http.client
import json
from pathlib import Path
import shutil
import subprocess
import time
from urllib.request import urlopen


ROOT = Path('.pilot/scale-profile')
FINISHED = Path('.pilot/test-finished')
WINDOW_SECONDS = 30
NODE = ['docker', 'exec', 'haptic-e2e-control-plane']
# A port-forward that drops mid-transfer raises IncompleteRead, which is not an OSError.
TRANSFER_ERRORS = (OSError, http.client.HTTPException)
KUBECONFIG = Path('/tmp/haproxy-e2e-kubeconfig')
KUBECTL = ['kubectl', f'--kubeconfig={KUBECONFIG}', '-n', 'haptic', '--request-timeout=55s']
ENDPOINTS = [
    ('metrics', 'metrics-before.txt'),
    (f'debug/pprof/profile?seconds={WINDOW_SECONDS}', 'cpu.prof'),
    ('debug/pprof/heap', 'heap.prof'),
    ('debug/pprof/mutex', 'mutex.prof'),
    ('debug/pprof/block', 'block.prof'),
    ('debug/pprof/goroutine?debug=1', 'goroutines.txt'),
    ('metrics', 'metrics-after.txt'),
]


def now():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


class Controller:
    def __init__(self, index, name):
        self.name = name
        self.debug_port = 18080 + index
        self.metrics_port = 19090 + index
        self.log = (ROOT / f'{name}-forward.log').open('a')
        self.forward = None

    def connect(self):
        if self.forward is not None and self.forward.poll() is None:
            return
        self.forward = subprocess.Popen(KUBECTL + [
            'port-forward', '--address=127.0.0.1', f'pod/{self.name}',
            f'{self.debug_port}:8080', f'{self.metrics_port}:9090'], stdout=self.log, stderr=self.log)
        deadline = time.monotonic() + 30
        while True:
            try:
                with urlopen(f'http://127.0.0.1:{self.debug_port}/debug/pprof/', timeout=5) as response:
                    response.read()
                return
            except TRANSFER_ERRORS:
                if time.monotonic() >= deadline or self.forward.poll() is not None:
                    raise
                time.sleep(1)

    def capture(self, window):
        target = window / self.name
        target.mkdir()
        errors = []
        try:
            self.connect()
        except TRANSFER_ERRORS as error:
            (target / 'errors.txt').write_text(f'connect: {error}\n')
            return
        for endpoint, filename in ENDPOINTS:
            port = self.metrics_port if endpoint == 'metrics' else self.debug_port
            try:
                with urlopen(f'http://127.0.0.1:{port}/{endpoint}', timeout=WINDOW_SECONDS + 25) as response:
                    (target / filename).write_bytes(response.read())
            except TRANSFER_ERRORS as error:
                errors.append(f'{endpoint}: {error}')
        if errors:
            (target / 'errors.txt').write_text('\n'.join(errors) + '\n')

    def close(self):
        if self.forward is not None:
            self.forward.terminate()
            try:
                self.forward.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.forward.kill()
        self.log.close()


def container_stats(path):
    try:
        with path.open('wb') as output:
            subprocess.run(NODE + ['crictl', 'stats', '-o', 'json'], stdout=output, stderr=subprocess.DEVNULL,
                           timeout=30, check=False)
    except (OSError, subprocess.SubprocessError) as error:
        path.with_suffix('.error.txt').write_text(f'{error}\n')


def controller_names():
    try:
        result = subprocess.run(KUBECTL + ['get', 'pods', '-l', 'app.kubernetes.io/component=controller', '-o', 'json'],
                                capture_output=True, timeout=60, check=True)
        pods = json.loads(result.stdout)['items']
    except (OSError, subprocess.SubprocessError, ValueError):
        return []
    return sorted(pod['metadata']['name'] for pod in pods if pod['status'].get('phase') == 'Running')


def main():
    shutil.rmtree(ROOT, ignore_errors=True)
    ROOT.mkdir(parents=True)
    FINISHED.unlink(missing_ok=True)
    deadline = time.monotonic() + 900
    names = []
    while len(names) < 2 and time.monotonic() < deadline and not FINISHED.exists():
        names = controller_names() if KUBECONFIG.exists() else []
        time.sleep(3)
    controllers = [Controller(index, name) for index, name in enumerate(names)]
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(controllers) or 1) as executor:
            index = 0
            while controllers and not FINISHED.exists():
                window = ROOT / f'window-{index:03d}'
                window.mkdir()
                started = now()
                container_stats(window / 'containers-start.json')
                for future in [executor.submit(controller.capture, window) for controller in controllers]:
                    future.result()
                container_stats(window / 'containers-end.json')
                (window / 'window.json').write_text(json.dumps({'start': started, 'end': now()}) + '\n')
                index += 1
    finally:
        for controller in controllers:
            controller.close()


if __name__ == '__main__':
    main()
