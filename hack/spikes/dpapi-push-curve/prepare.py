#!/usr/bin/env python3
"""Assemble a /etc/haproxy tree for the spike container from a rendered config.

Rewrites the render-time absolute prefix (the validate temp dir the chart's
`default-path origin` points at) to /etc/haproxy, and drops in the Dataplane API
config, reload.sh and validate.sh exactly as charts/haptic/templates/
haproxy-deployment.yaml writes them.

usage: prepare.py SRC DEST [--dpapi-port 5555]
"""
import argparse
import os
import re
import shutil
import stat

DPAPI_YAML = """config_version: 2
name: haproxy-dataplaneapi
dataplaneapi:
  host: 0.0.0.0
  port: {port}
  disable_inotify: true
  user:
    - name: admin
      password: admin
      insecure: true
  userlist:
    userlist: ""
    userlist_file: ""
  transaction:
    transaction_dir: /var/lib/dataplaneapi/transactions
    backups_number: 10
    backups_dir: /var/lib/dataplaneapi/backups
  resources:
    maps_dir: maps
    ssl_certs_dir: certs
    general_storage_dir: general
haproxy:
  config_file: /etc/haproxy/haproxy.cfg
  haproxy_bin: /usr/local/sbin/haproxy
  master_worker_mode: false
  master_runtime: /etc/haproxy/haproxy-master.sock
  reload:
    reload_delay: {reload_delay}
    reload_cmd: /etc/haproxy/reload.sh
    restart_cmd: /etc/haproxy/reload.sh
    reload_strategy: custom
    validate_cmd: /etc/haproxy/validate.sh
log_targets:
  - log_to: stdout
    log_level: info
    log_format: text
    log_types:
    - access
    - app
"""

RELOAD_SH = """#!/bin/sh
echo reload|socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock
"""

VALIDATE_SH = """#!/bin/sh
exec /usr/local/sbin/haproxy -dr -c -f "$DATAPLANEAPI_TRANSACTION_FILE"
"""


TMP_PREFIX = r"/tmp/haproxy-validate-\d+/worker-\d+/test-\d+"


def rewrite_config(text: str) -> str:
    # `haptic validate` renders every aux path absolute under a per-test temp
    # dir. Production renders them RELATIVE to `default-path origin` — which is
    # what makes the Dataplane API address a runtime map as `maps/host.map`,
    # the same identifier HAProxy holds. Reproduce that: keep the two base
    # directives absolute, strip the prefix everywhere else.
    text = re.sub(rf"default-path origin {TMP_PREFIX}", "default-path origin /etc/haproxy", text)
    text = re.sub(rf"crt-base {TMP_PREFIX}/ssl", "crt-base /etc/haproxy/certs", text)
    text = re.sub(rf"{TMP_PREFIX}/", "", text)
    text = re.sub(TMP_PREFIX, "/etc/haproxy", text)
    # `daemon` + `-W -db` is contradictory noise in a container.
    text = re.sub(r"(?m)^\s*daemon\s*$\n", "", text)
    return text


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("src")
    ap.add_argument("dest")
    ap.add_argument("--dpapi-port", type=int, default=5555)
    ap.add_argument("--reload-delay", type=int, default=1,
                    help="dataplaneapi haproxy.reload.reload_delay (chart default: 1)")
    args = ap.parse_args()

    if os.path.exists(args.dest):
        shutil.rmtree(args.dest)
    os.makedirs(args.dest)
    for sub in ("maps", "certs", "general"):
        srcdir = os.path.join(args.src, sub)
        dstdir = os.path.join(args.dest, sub)
        os.makedirs(dstdir, exist_ok=True)
        if os.path.isdir(srcdir):
            for f in os.listdir(srcdir):
                shutil.copy(os.path.join(srcdir, f), dstdir)

    cfg = rewrite_config(open(os.path.join(args.src, "haproxy.cfg")).read())
    with open(os.path.join(args.dest, "haproxy.cfg"), "w") as f:
        f.write(cfg)

    with open(os.path.join(args.dest, "dataplaneapi.yaml"), "w") as f:
        f.write(DPAPI_YAML.format(port=args.dpapi_port, reload_delay=args.reload_delay))
    for name, body in (("reload.sh", RELOAD_SH), ("validate.sh", VALIDATE_SH)):
        p = os.path.join(args.dest, name)
        with open(p, "w") as f:
            f.write(body)
        os.chmod(p, os.stat(p).st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    print(args.dest)


if __name__ == "__main__":
    main()
