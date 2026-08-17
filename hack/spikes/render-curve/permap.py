#!/usr/bin/env python3
"""Per-template median render time across the steps, to name the superlinear rows."""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from analyze import collect, med  # noqa: E402

STEPS = [300, 1000, 1500, 3000]
for kind in ["httproute", "ingress"]:
    per = {}
    for n in STEPS:
        agg = collect(f"runD-{kind}-{n}-r*.txt")
        rows = agg.get(f"{kind}-{n}", {})
        for lbl, vals in rows.items():
            per.setdefault(lbl, {})[n] = med(vals)
    print(f"\n### {kind} (ms, median of 9)")
    print(f"{'template':<32} " + " ".join(f"{n:>9}" for n in STEPS) + "   growth 300->3000")
    ordered = sorted(per.items(), key=lambda kv: -kv[1].get(3000, 0))
    for lbl, byn in ordered:
        if byn.get(3000, 0) < 1.0 and lbl != "TOTAL":
            continue
        g = byn.get(3000, 0) / byn[300] if byn.get(300) else 0
        print(f"{lbl:<32} " + " ".join(f"{byn.get(n, 0):9.2f}" for n in STEPS) + f"   {g:6.1f}x")
