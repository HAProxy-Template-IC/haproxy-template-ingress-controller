"""Sample VM memory and filesystem capacity during the runner pilot."""

import json
import os
from pathlib import Path
import shutil
import sys
import time


with open(sys.argv[1], "a", encoding="utf-8", buffering=1) as output:
    while True:
        memory = {}
        for line in Path("/proc/meminfo").read_text().splitlines():
            key, value = line.split(":", 1)
            memory[key] = int(value.split()[0]) * 1024
        disk = shutil.disk_usage(".")
        docker_disk = shutil.disk_usage("/var/lib/docker")
        output.write(json.dumps({
            "time": time.time(),
            "memory_total": memory["MemTotal"],
            "memory_available": memory["MemAvailable"],
            "swap_used": memory["SwapTotal"] - memory["SwapFree"],
            "disk_free": disk.free,
            "docker_disk_free": docker_disk.free,
            "load": os.getloadavg(),
        }) + "\n")
        time.sleep(5)
