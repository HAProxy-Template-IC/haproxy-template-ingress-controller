#!/usr/bin/env python3
"""Aggregate every raw benchmark artifact into the medians used by RESULTS.md."""
import glob
import json
import os
import re
import statistics as st

SPIKE = os.path.dirname(os.path.abspath(__file__))
RAW = os.path.join(SPIKE, "raw")
KINDS = ["httproute", "ingress"]
STEPS = [300, 1000, 1500, 3000]
STEPS_A = [300, 1000, 3000]


def med(xs):
    return st.median(xs) if xs else float("nan")


def parse_table(path):
    """Return {test_name: {row_label: [ms, ...]}} from a benchmark table."""
    lines = open(path).read().split("\n")
    hdr = None
    for i, ln in enumerate(lines):
        if ln.startswith("File ") and "It1" in ln:
            hdr = lines[i - 1]
            break
    if hdr is None:
        return {}
    tests = [c.strip() for c in hdr.split("|")[1:] if c.strip()]
    out = {t: {} for t in tests}
    for ln in lines:
        if "|" not in ln or ln.startswith("File ") or set(ln) <= set("-|"):
            continue
        cols = ln.split("|")
        label = cols[0].strip()
        if not label or label == "File":
            continue
        vals = cols[1:-1]
        if len(vals) != len(tests):
            continue
        ok = True
        parsed = []
        for v in vals:
            nums = []
            for tok in v.split():
                try:
                    nums.append(float(tok))
                except ValueError:
                    ok = False
            parsed.append(nums)
        if not ok or not any(parsed):
            continue
        for t, nums in zip(tests, parsed):
            out[t].setdefault(label, []).extend(nums)
    return out


def collect(pattern):
    """Merge parse_table over every file matching pattern."""
    agg = {}
    for f in sorted(glob.glob(os.path.join(RAW, pattern))):
        for test, rows in parse_table(f).items():
            d = agg.setdefault(test, {})
            for lbl, vals in rows.items():
                d.setdefault(lbl, []).extend(vals)
    return agg


def maps_total(rows):
    """Median of the per-iteration sum over all map: rows."""
    map_rows = [v for k, v in rows.items() if k.startswith("map:")]
    if not map_rows:
        return float("nan")
    n = min(len(v) for v in map_rows)
    return med([sum(v[i] for v in map_rows) for i in range(n)])


def hc_times(kind, n):
    p = os.path.join(RAW, f"hc-{kind}-{n}.txt")
    if not os.path.exists(p):
        return []
    return [int(m) for m in re.findall(r"ms=(\d+) exit=0", open(p).read())]


def rss(pattern):
    vals = []
    for f in sorted(glob.glob(os.path.join(RAW, pattern))):
        m = re.search(r"maxrss_kb=(\d+)", open(f).read())
        if m:
            vals.append(int(m.group(1)) / 1024.0)
    return vals


def val_secs(pattern):
    vals = []
    for f in sorted(glob.glob(os.path.join(RAW, pattern))):
        m = re.search(r"^Tests:.*\(([\d.]+)s\)", open(f).read(), re.M)
        if m:
            vals.append(float(m.group(1)) * 1000)
    return vals


print("=" * 100)
print("RUN A - combined config (what scripts/test-benchmark.sh --steps 300,1000,3000 runs), 9 samples")
print("=" * 100)
runA = {}
for kind in KINDS:
    agg = collect(f"runA-{kind}-r*.txt")
    for n in STEPS_A:
        t = f"{kind}-{n}"
        rows = agg.get(t, {})
        runA[(kind, n)] = {
            "total": med(rows.get("TOTAL", [])),
            "cfg": med(rows.get("haproxy.cfg", [])),
            "maps": maps_total(rows),
            "n": len(rows.get("TOTAL", [])),
        }
        r = runA[(kind, n)]
        print(f"{t:>18}  total={r['total']:8.1f}  haproxy.cfg={r['cfg']:8.1f}  "
              f"maps={r['maps']:7.1f}  samples={r['n']}")

print()
print("=" * 100)
print("RUN D - one config per step (isolated process), 9 samples + peak RSS")
print("=" * 100)
runD = {}
for kind in KINDS:
    for n in STEPS:
        agg = collect(f"runD-{kind}-{n}-r*.txt")
        t = f"{kind}-{n}"
        rows = agg.get(t, {})
        rr = rss(f"runD-{kind}-{n}-r*.rss")
        runD[(kind, n)] = {
            "total": med(rows.get("TOTAL", [])),
            "cfg": med(rows.get("haproxy.cfg", [])),
            "maps": maps_total(rows),
            "rss": med(rr),
            "n": len(rows.get("TOTAL", [])),
        }
        r = runD[(kind, n)]
        print(f"{t:>18}  total={r['total']:8.1f}  haproxy.cfg={r['cfg']:8.1f}  "
              f"maps={r['maps']:7.1f}  peakRSS={r['rss']:7.1f} MiB  samples={r['n']}")

print()
print("=" * 100)
print("ARTIFACT SIZES + haproxy -dr -c (median of 5)")
print("=" * 100)
sizes = {}
for kind in KINDS:
    for n in STEPS:
        p = os.path.join(RAW, f"sizes-{kind}-{n}.json")
        d = json.load(open(p))
        hc = hc_times(kind, n)
        sizes[(kind, n)] = dict(d, hc=med(hc))
        print(f"{kind}-{n:<6}  cfg={d['cfg_bytes']:>9,}B / {d['cfg_lines']:>6,} lines  "
              f"maps={d['map_count']:>2} files / {d['map_bytes']:>8,}B / {d['map_lines']:>6,} lines  "
              f"general={d['general_count']}/{d['general_bytes']:,}B  haproxy -c={med(hc):.0f} ms")

print()
print("=" * 100)
print("VALIDATE COST (haptic validate, median of 3): plain vs +haproxy_valid")
print("=" * 100)
valid = {}
for kind in KINDS:
    for n in STEPS:
        plain = med(val_secs(f"val-{kind}-{n}-plain-r*.txt"))
        hv = med(val_secs(f"val-{kind}-{n}-hv-r*.txt"))
        valid[(kind, n)] = {"plain": plain, "hv": hv, "delta": hv - plain}
        print(f"{kind}-{n:<6}  plain={plain:8.1f} ms  +haproxy_valid={hv:8.1f} ms  "
              f"validation={hv - plain:7.1f} ms  (of which haproxy -c={sizes[(kind, n)]['hc']:.0f} ms)")

print()
print("=" * 100)
print("CONTENTION at 3000 routes (5 rounds x 3 iterations = 15 samples)")
print("=" * 100)
cont = {}
labels = {
    "C0": "isolated, 16 CPUs",
    "C1": "+ background haproxy -dr -c loop",
    "C2": "two concurrent render processes",
    "C3": "isolated, pinned to 2 CPUs",
    "C4": "2 CPUs + haproxy -dr -c loop on same CPUs",
}
for kind in KINDS:
    base = None
    for c in ["C0", "C1", "C2", "C3", "C4"]:
        pats = [f"c2-{kind}-{c}-r*.txt"] if c != "C2" else [
            f"c2-{kind}-C2a-r*.txt", f"c2-{kind}-C2b-r*.txt"]
        vals = []
        for pat in pats:
            agg = collect(pat)
            vals.extend(agg.get(f"{kind}-3000", {}).get("TOTAL", []))
        m = med(vals)
        if c == "C0":
            base = m
        cont[(kind, c)] = {"med": m, "p95": (sorted(vals)[int(0.95 * (len(vals) - 1))] if vals else 0),
                           "n": len(vals), "ratio": m / base}
        print(f"{kind:>10} {c}  median={m:8.1f} ms  max={max(vals):8.1f}  "
              f"x{m / base:.2f}  n={len(vals)}   {labels[c]}")

print()
print("=" * 100)
print("GOMAXPROCS sweep at 3000 routes (9 samples each)")
print("=" * 100)
gomax = {}
for kind in KINDS:
    for g in [1, 2, 4, 8, 16]:
        agg = collect(f"gomax-{kind}-{g}-r*.txt")
        vals = agg.get(f"{kind}-3000", {}).get("TOTAL", [])
        rr = rss(f"gomax-{kind}-{g}-r*.rss")
        gomax[(kind, g)] = {"med": med(vals), "rss": med(rr)}
        print(f"{kind:>10} GOMAXPROCS={g:<3} median={med(vals):8.1f} ms  peakRSS={med(rr):7.1f} MiB")

json.dump(
    {
        "runA": {f"{k[0]}-{k[1]}": v for k, v in runA.items()},
        "runD": {f"{k[0]}-{k[1]}": v for k, v in runD.items()},
        "sizes": {f"{k[0]}-{k[1]}": v for k, v in sizes.items()},
        "valid": {f"{k[0]}-{k[1]}": v for k, v in valid.items()},
        "cont": {f"{k[0]}-{k[1]}": v for k, v in cont.items()},
        "gomax": {f"{k[0]}-{k[1]}": v for k, v in gomax.items()},
    },
    open(os.path.join(SPIKE, "summary.json"), "w"),
    indent=2,
)
print("\nwrote summary.json")
