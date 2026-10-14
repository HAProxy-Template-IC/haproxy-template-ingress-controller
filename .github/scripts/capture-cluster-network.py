"""Capture cluster DNS and HTTP headers for a bounded diagnostic run."""

import json
from pathlib import Path
import subprocess
import time


while not Path('.pilot/test-finished').exists():
    result = subprocess.run(['docker', 'inspect', 'haptic-e2e-control-plane'],
                            capture_output=True, text=True, check=False)
    if result.returncode == 0:
        pid = json.loads(result.stdout)[0]['State']['Pid']
        if pid:
            capture = subprocess.Popen(['sudo', 'nsenter', '-t', str(pid), '-n',
                                        'tcpdump', '-Z', 'root', '-i', 'any', '-s', '256', '-U',
                                        '-C', '10', '-W', '3', '-w', str(Path('.pilot/network.pcap').resolve()),
                                        'port 53 or tcp port 80'], start_new_session=True)
            try:
                while not Path('.pilot/test-finished').exists() and capture.poll() is None:
                    time.sleep(1)
            finally:
                subprocess.run(['sudo', 'kill', '-INT', '--', f'-{capture.pid}'], check=False)
                capture.wait(timeout=10)
            break
    time.sleep(1)
