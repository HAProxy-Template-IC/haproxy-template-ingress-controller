#!/usr/bin/env python3
"""Measure HAPTIC's Dataplane API config-push path against a plain file write +
master-socket reload, for one prepared config tree.

Phases:
  dpapi  — container running HAProxy + dataplaneapi; measures the calls
           pkg/dataplane/orchestrator.go makes (version, raw push skip_reload,
           raw push force_reload, storage map replace, runtime map entry add,
           and the map read-back the aux comparator does every sync).
  plain  — same container without dataplaneapi; measures file write + master
           socket reload, `haproxy -dr -c -f`, and a bare reload.

usage: measure.py --workdir DIR --runs 5 --out results.json --phase dpapi|plain
"""
import argparse
import base64
import http.client
import json
import os
import re
import shutil
import socket
import subprocess
import time

CONT = os.environ.get("NAME", "spike-dpapi")
PORT = int(os.environ.get("PORT", "5555"))
AUTH = "Basic " + base64.b64encode(b"admin:admin").decode()
MASTER_SOCK = "haproxy-master.sock"
RUNTIME_MAP = "host.map"


def conn():
    c = http.client.HTTPConnection("127.0.0.1", PORT, timeout=600)
    return c


def request(c, method, path, body=None, ctype=None):
    headers = {"Authorization": AUTH}
    if ctype:
        headers["Content-Type"] = ctype
    t0 = time.perf_counter()
    c.request(method, path, body=body, headers=headers)
    resp = c.getresponse()
    data = resp.read()
    dt = (time.perf_counter() - t0) * 1000.0
    return dt, resp.status, dict(resp.getheaders()), data


def get_version(c):
    dt, status, _, data = request(c, "GET", "/v3/services/haproxy/configuration/version")
    if status != 200:
        raise RuntimeError(f"version: {status} {data[:200]!r}")
    return dt, int(data.decode().strip())


def master_cmd(workdir, cmd, read_timeout=600.0):
    """Send one command to the HAProxy master CLI, return (ms, reply)."""
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(read_timeout)
    # AF_UNIX paths cap at ~107 bytes and the scratchpad path alone is longer,
    # so connect from inside the directory.
    cwd = os.getcwd()
    os.chdir(workdir)
    try:
        s.connect(MASTER_SOCK)
    finally:
        os.chdir(cwd)
    t0 = time.perf_counter()
    s.sendall((cmd + "\n").encode())
    chunks = []
    while True:
        b = s.recv(65536)
        if not b:
            break
        chunks.append(b)
        # The master answers a reload with the worker's startup logs terminated
        # by "Success=1" / "Success=0"; stop as soon as the verdict is in.
        joined = b"".join(chunks)
        if b"Success=" in joined:
            break
    dt = (time.perf_counter() - t0) * 1000.0
    s.close()
    return dt, b"".join(chunks).decode(errors="replace")


def docker(*args, **kw):
    return subprocess.run(["docker", *args], capture_output=True, text=True, **kw)


def dpapi_pid():
    r = docker("exec", CONT, "pidof", "dataplaneapi")
    return r.stdout.strip().split()[0] if r.stdout.strip() else None


def cpu_seconds(pid):
    """utime+stime of a pid inside the container, in seconds."""
    if not pid:
        return None
    r = docker("exec", CONT, "cat", f"/proc/{pid}/stat")
    if r.returncode != 0:
        return None
    # comm may contain spaces/parens; fields after the closing paren.
    tail = r.stdout[r.stdout.rindex(")") + 2:].split()
    return (int(tail[11]) + int(tail[12])) / os.sysconf("SC_CLK_TCK")


def variant(text, run):
    """A per-run config change: HAPTIC never pushes a byte-identical config."""
    marker = f"# spike-run={run}\n"
    text = re.sub(r"(?m)^# spike-run=\d+\n", "", text)
    return marker + text


def stats(values):
    if not values:
        return None
    s = sorted(values)
    mid = len(s) // 2
    p50 = s[mid] if len(s) % 2 else (s[mid - 1] + s[mid]) / 2
    return {"p50": round(p50, 1), "max": round(s[-1], 1), "n": len(s),
            "all": [round(v, 1) for v in values]}


def phase_dpapi(workdir, runs, results):
    cfg_path = os.path.join(workdir, "haproxy.cfg")
    base_cfg = open(cfg_path).read()
    map_path = os.path.join(workdir, "maps", RUNTIME_MAP)
    base_map = open(map_path).read()
    c = conn()
    pid = dpapi_pid()

    acc = {k: [] for k in ("get_version", "push_skip_reload", "push_force_reload",
                           "reload_complete", "map_read_all", "map_storage_put",
                           "map_runtime_add")}
    cpu = {"push_skip_reload": [], "push_force_reload": []}
    meta = {}

    for run in range(runs):
        cfg = variant(base_cfg, run)

        # (a) raw push, skip_reload — the write+validate half of every sync.
        dt_v, version = get_version(c)
        acc["get_version"].append(dt_v)
        cpu0 = cpu_seconds(pid)
        dt, status, _, data = request(
            c, "POST",
            f"/v3/services/haproxy/configuration/raw?skip_reload=true&version={version}",
            body=cfg.encode(), ctype="text/plain")
        cpu1 = cpu_seconds(pid)
        if status not in (200, 201, 202, 204):
            raise RuntimeError(f"push skip_reload: {status} {data[:300]!r}")
        acc["push_skip_reload"].append(dt)
        if cpu0 is not None:
            cpu["push_skip_reload"].append(cpu1 - cpu0)

        # (a) raw push, force_reload — what PushRawConfiguration sends.
        dt_v, version = get_version(c)
        acc["get_version"].append(dt_v)
        cfg = variant(base_cfg, 1000 + run)
        cpu0 = cpu_seconds(pid)
        t_start = time.perf_counter()
        dt, status, headers, data = request(
            c, "POST",
            f"/v3/services/haproxy/configuration/raw?force_reload=true&version={version}",
            body=cfg.encode(), ctype="text/plain")
        cpu1 = cpu_seconds(pid)
        if status not in (200, 201, 202, 204):
            raise RuntimeError(f"push force_reload: {status} {data[:300]!r}")
        acc["push_force_reload"].append(dt)
        if cpu0 is not None:
            cpu["push_force_reload"].append(cpu1 - cpu0)
        reload_id = headers.get("Reload-ID", "")
        meta["force_reload_status"] = status
        # Wall time until the reload the push triggered is actually done.
        if reload_id:
            while True:
                _, st, _, body = request(c, "GET", f"/v3/services/haproxy/reloads/{reload_id}")
                if st == 200 and json.loads(body).get("status") != "in_progress":
                    break
                time.sleep(0.02)
        acc["reload_complete"].append((time.perf_counter() - t_start) * 1000.0)

        # (b) the map read-back auxiliaryfiles.Compare does on every sync.
        t0 = time.perf_counter()
        _, st, _, body = request(c, "GET", "/v3/services/haproxy/storage/maps")
        names = [m["storage_name"] for m in json.loads(body)]
        nbytes = 0
        for name in names:
            _, st, _, b = request(c, "GET", f"/v3/services/haproxy/storage/maps/{name}")
            nbytes += len(b)
        acc["map_read_all"].append((time.perf_counter() - t0) * 1000.0)
        meta["maps_count"] = len(names)
        meta["maps_read_bytes"] = nbytes

        # (b) storage replace of one changed map file.
        new_map = base_map + f"spike-{run}.example.com gtw_default_bench-route-0_bench-svc-0_8080\n"
        dt, status, _, data = request(
            c, "PUT",
            f"/v3/services/haproxy/storage/maps/{RUNTIME_MAP}?skip_reload=true",
            body=new_map.encode(), ctype="text/plain")
        if status not in (200, 201, 202, 204):
            raise RuntimeError(f"map storage put: {status} {data[:300]!r}")
        acc["map_storage_put"].append(dt)

        # (b) runtime map entry add.
        entry = json.dumps({"key": f"spike-rt-{run}.example.com",
                            "value": "gtw_default_bench-route-0_bench-svc-0_8080"})
        dt, status, _, data = request(
            c, "POST", f"/v3/services/haproxy/runtime/maps/{RUNTIME_MAP}/entries",
            body=entry.encode(), ctype="application/json")
        if status not in (200, 201, 202, 204):
            raise RuntimeError(f"runtime map add: {status} {data[:300]!r}")
        acc["map_runtime_add"].append(dt)

    results["dpapi"] = {k: stats(v) for k, v in acc.items()}
    results["dpapi_cpu_seconds"] = {k: stats([x * 1000 for x in v]) for k, v in cpu.items() if v}
    results["dpapi_meta"] = meta


def phase_plain(workdir, srcdir, runs, results):
    # Read the pristine prepared tree, not the workdir: a preceding dpapi phase
    # leaves the client-native re-serialisation of the config on disk.
    base_cfg = open(os.path.join(srcdir, "haproxy.cfg")).read()
    maps_src = os.path.join(srcdir, "maps")
    map_names = sorted(os.listdir(maps_src))
    acc = {k: [] for k in ("write_files", "reload_after_write", "write_plus_reload",
                           "validate_only", "reload_only", "docker_exec_baseline")}

    for run in range(runs):
        cfg = variant(base_cfg, 2000 + run)

        # (c) write config + every map, then reload over the master socket.
        t0 = time.perf_counter()
        tmp = os.path.join(workdir, ".haproxy.cfg.tmp")
        with open(tmp, "w") as f:
            f.write(cfg)
        os.replace(tmp, os.path.join(workdir, "haproxy.cfg"))
        for name in map_names:
            dst = os.path.join(workdir, "maps", name)
            tmpm = dst + ".tmp"
            shutil.copyfile(os.path.join(maps_src, name), tmpm)
            os.replace(tmpm, dst)
        t_written = time.perf_counter()
        dt_reload, reply = master_cmd(workdir, "reload")
        t_end = time.perf_counter()
        if "Success=1" not in reply:
            raise RuntimeError(f"reload failed: {reply[-400:]}")
        acc["write_files"].append((t_written - t0) * 1000.0)
        acc["reload_after_write"].append(dt_reload)
        acc["write_plus_reload"].append((t_end - t0) * 1000.0)

        # (d) the validation the Dataplane API runs inside every push.
        t0 = time.perf_counter()
        r = docker("exec", CONT, "/usr/local/sbin/haproxy", "-dr", "-c", "-f",
                   "/etc/haproxy/haproxy.cfg")
        acc["validate_only"].append((time.perf_counter() - t0) * 1000.0)
        if r.returncode != 0:
            raise RuntimeError(f"haproxy -c failed: {r.stderr[-400:]}")
        t0 = time.perf_counter()
        docker("exec", CONT, "/bin/true")
        acc["docker_exec_baseline"].append((time.perf_counter() - t0) * 1000.0)

        # (e) reload with nothing changed.
        dt, reply = master_cmd(workdir, "reload")
        if "Success=1" not in reply:
            raise RuntimeError(f"bare reload failed: {reply[-400:]}")
        acc["reload_only"].append(dt)

    results["plain"] = {k: stats(v) for k, v in acc.items()}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--workdir", required=True)
    ap.add_argument("--srcdir", help="rendered source tree (for the plain-write phase)")
    ap.add_argument("--runs", type=int, default=5)
    ap.add_argument("--phase", choices=["dpapi", "plain"], required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    results = {}
    if os.path.exists(args.out):
        results = json.load(open(args.out))
    if args.phase == "dpapi":
        phase_dpapi(args.workdir, args.runs, results)
    else:
        phase_plain(args.workdir, args.srcdir or args.workdir, args.runs, results)
    cfg = os.path.join(args.workdir, "haproxy.cfg")
    results["config"] = {
        "bytes": os.path.getsize(cfg),
        "lines": sum(1 for _ in open(cfg)),
        "maps_bytes": sum(os.path.getsize(os.path.join(args.workdir, "maps", f))
                          for f in os.listdir(os.path.join(args.workdir, "maps"))),
        "maps_files": len(os.listdir(os.path.join(args.workdir, "maps"))),
    }
    with open(args.out, "w") as f:
        json.dump(results, f, indent=2)
    print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
