#!/usr/bin/env python3
import os
from pathlib import Path

packages = Path('.pilot/unit-packages-all.txt').read_text().splitlines()
renderer = 'gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer'
assert packages and len(packages) == len(set(packages))
assert renderer in packages
partitions = {'renderer': [renderer], 'rest': [p for p in packages if p != renderer]}
assert set(partitions['renderer']).isdisjoint(partitions['rest'])
assert sorted(partitions['renderer'] + partitions['rest']) == sorted(packages)
selected = partitions[os.environ['SHARD_ID']]
assert selected
Path('.pilot/unit-packages-selected.txt').write_text('\n'.join(selected) + '\n')
