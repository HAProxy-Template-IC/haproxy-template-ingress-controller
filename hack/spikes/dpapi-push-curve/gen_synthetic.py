#!/usr/bin/env python3
"""Generate a synthetic HAPTIC-shaped haproxy.cfg + map files for N routes.

Shape mirrors what the bundled chart renders: one shared frontend that resolves
the backend through map lookups into txn vars, N HTTP backends with 5 servers
each (guid + default-server check), and ~25 map files with N entries each.

usage: gen_synthetic.py N OUTDIR
"""
import os
import sys

MAPS = [
    "host.map", "path-exact.map", "path-prefix.map", "path-regex.map",
    "method.map", "header-exact.map", "header-regex.map", "query-exact.map",
    "query-regex.map", "redirect.map", "ssl-passthrough.map", "tls-mode.map",
    "backend-proto.map", "backend-timeout.map", "rate-limit.map",
    "waf-policy.map", "cors-policy.map", "auth-policy.map", "cache-policy.map",
    "compression.map", "canary-weight.map", "sticky-mode.map",
    "request-headers.map", "response-headers.map", "route-priority.map",
]


def gen_config(n: int, marker: str) -> str:
    out = []
    out.append(f"""# _version=1
# haptic synthetic config, {n} routes, marker={marker}
global
  log stdout format raw local0 info
  master-worker
  stats socket /etc/haproxy/haproxy.sock mode 660 level admin expose-fd listeners
  stats timeout 30s
  maxconn 100000
  nbthread 4
  hard-stop-after 30s
  default-path origin /etc/haproxy
  tune.ssl.default-dh-param 2048
  tune.bufsize 32768

defaults
  log global
  mode http
  option httplog
  option http-keep-alive
  option redispatch
  timeout connect 5s
  timeout client 50s
  timeout server 50s
  timeout http-request 10s
  timeout http-keep-alive 60s
  timeout queue 30s
  retries 3

frontend http
  bind :8080
  # route resolution via maps (same shape as the chart's frontend)
  http-request set-var(txn.host) req.hdr(host),field(1,:),lower
  http-request set-var(txn.path) path
  http-request set-var(txn.host_backend) var(txn.host),map_str(maps/host.map)
  http-request set-var(txn.path_backend) var(txn.path),map_beg(maps/path-prefix.map)
  http-request set-var(txn.exact_backend) var(txn.path),map_str(maps/path-exact.map)
  http-request set-var(txn.backend_name) var(txn.exact_backend)
  http-request set-var(txn.backend_name) var(txn.path_backend) if !{{ var(txn.path_backend) -m len 0 }} {{ var(txn.backend_name) -m len 0 }}
  http-request set-var(txn.backend_name) var(txn.host_backend) if {{ var(txn.backend_name) -m len 0 }}
  http-request set-var(txn.waf) var(txn.host),map_str(maps/waf-policy.map)
  http-request set-var(txn.rl) var(txn.host),map_str(maps/rate-limit.map)
  http-request set-header X-Request-Id %[uuid()]
  http-response set-header X-Served-By %[var(txn.backend_name)]
  use_backend %[var(txn.backend_name)] if {{ var(txn.backend_name) -m found }}
  default_backend haptic-default
""")
    for i in range(n):
        out.append(f"""backend be-app-{i}-ns-{i % 40}-8080
  mode http
  balance roundrobin
  option httpchk GET /healthz
  http-check expect status 200
  default-server check inter 5s fall 3 rise 2 slowstart 10s maxconn 512
  http-request set-header X-Backend be-app-{i}
  http-response set-header X-Route app-{i}.example.com""")
        for s in range(5):
            out.append(
                f"  server srv-{s} 10.{(i // 250) % 250}.{(i % 250)}.{s + 10}:8080"
                f" id {i * 10 + s + 100} weight 128"
                f" guid haptic.app-{i}-ns-{i % 40}.srv-{s}"
            )
        out.append("")
    out.append("""backend haptic-default
  mode http
  http-request return status 404 content-type "text/plain" string "no route"
""")
    return "\n".join(out)


def gen_map(name: str, n: int, marker: str) -> str:
    lines = [f"# {name} marker={marker}"]
    for i in range(n):
        if "path" in name:
            lines.append(f"/api/v1/app-{i}/resource be-app-{i}-ns-{i % 40}-8080")
        elif name in ("method.map", "tls-mode.map", "compression.map"):
            lines.append(f"app-{i}.example.com {'GET' if i % 2 else 'POST'}")
        else:
            lines.append(f"app-{i}.example.com be-app-{i}-ns-{i % 40}-8080")
    return "\n".join(lines) + "\n"


def main():
    n = int(sys.argv[1])
    outdir = sys.argv[2]
    marker = sys.argv[3] if len(sys.argv) > 3 else "base"
    os.makedirs(os.path.join(outdir, "maps"), exist_ok=True)
    with open(os.path.join(outdir, "haproxy.cfg"), "w") as f:
        f.write(gen_config(n, marker))
    for m in MAPS:
        with open(os.path.join(outdir, "maps", m), "w") as f:
            f.write(gen_map(m, n, marker))
    print(outdir)


if __name__ == "__main__":
    main()
