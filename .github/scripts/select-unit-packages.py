#!/usr/bin/env python3
import os
from pathlib import Path

packages = Path('.pilot/unit-packages-all.txt').read_text().splitlines()
base = 'github.com/haproxy-haptic/HAPTIC/'
renderer = base + 'pkg/controller/renderer'
command = base + 'cmd/haptic'
assert packages and len(packages) == len(set(packages))
assert renderer in packages and command in packages
partitions = {
    'renderer': [renderer],
    'command': [command],
    'rest': [p for p in packages if p not in {renderer, command}],
}
flattened = [p for selection in partitions.values() for p in selection]
assert sorted(flattened) == sorted(packages)
selected = partitions[os.environ['SHARD_ID']]
assert selected
Path('.pilot/unit-packages-selected.txt').write_text('\n'.join(selected) + '\n')
