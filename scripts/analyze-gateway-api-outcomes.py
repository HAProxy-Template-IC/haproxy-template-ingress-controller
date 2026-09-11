#!/usr/bin/env python3

import argparse
from decimal import Decimal, InvalidOperation, localcontext
import json
import math
from pathlib import Path
import re
import sys


SCALARS = {
    "haptic_reconciliation_errors_total",
    "haptic_deployment_errors_total",
    "haptic_validation_errors_total",
    "haptic_events_dropped_total",
    "haptic_events_dropped_critical_total",
}
# These label vectors have no samples until their first event.
VECTORS = {"haptic_apply_rejected_total", "haptic_runtime_map_divergence_total"}
METRICS = SCALARS | VECTORS
AGENT_SCALARS = {"haptic_agent_map_divergence_total", "haptic_agent_rollbacks_total"}
AGENT_VECTORS = {
    "haptic_agent_deferred_deletes_total", "haptic_agent_op_errors_total",
    "haptic_agent_apply_rejected_total", "haptic_agent_invariant_violations_total",
    "haptic_agent_reloads_total",
}
AGENT_FILTERS = {
    "haptic_agent_deferred_deletes_total": ("outcome", "abandoned"),
    "haptic_agent_reloads_total": ("result", "failed"),
}
SAMPLE = re.compile(
    r'(?P<name>[a-zA-Z_:][a-zA-Z0-9_:]*)(?P<labels>\{.*\})?'
    r'\s+(?P<value>[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)(?:\s+[-+]?\d+)?'
)
LABEL = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="((?:[^"\\]|\\[\\n"])*)"')


def parse_labels(raw):
    if not raw:
        return ()
    remaining = raw[1:-1].strip()
    labels = {}
    while remaining:
        match = LABEL.match(remaining)
        if match is None or match[1] in labels:
            raise ValueError("invalid or duplicate counter labels")
        labels[match[1]] = match[2]
        remaining = remaining[match.end():].lstrip()
        if remaining:
            if not remaining.startswith(","):
                raise ValueError("invalid counter label separator")
            remaining = remaining[1:].lstrip()
    return tuple(sorted(labels.items()))


def read_counters(path, scalars=SCALARS, vectors=VECTORS):
    counters = {name: {} for name in scalars | vectors}
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        name = re.split(r"[\s{]", line, maxsplit=1)[0]
        if name not in counters:
            continue
        match = SAMPLE.fullmatch(line)
        if match is None:
            raise ValueError(f"malformed counter {name} in {path}")
        labels = parse_labels(match["labels"])
        if name in scalars and match["labels"] is not None:
            raise ValueError(f"scalar counter {name} has labels in {path}")
        if name in vectors and not labels:
            raise ValueError(f"vector counter {name} has no labels in {path}")
        if labels in counters[name]:
            raise ValueError(f"duplicate counter series {name} in {path}")
        value = Decimal(match["value"])
        if not value.is_finite() or not math.isfinite(float(value)) or value < 0:
            raise ValueError(f"invalid counter {name} in {path}")
        counters[name][labels] = value
    for name in scalars:
        if len(counters[name]) != 1:
            raise ValueError(f"missing scalar counter {name} in {path}")
    return counters


def component_names(identities, component, container_name):
    names = []
    for pod in identities:
        if pod.get("component") != component:
            continue
        name = pod.get("name", "")
        if (pod.get("namespace") != "haptic" or not re.fullmatch(r"[a-z0-9][a-z0-9.-]*", name)
                or not pod.get("uid") or name in names):
            raise ValueError(f"invalid or duplicate {component} identity")
        containers = pod.get("containers", [])
        container_names = [container.get("name") for container in containers]
        if (container_names.count(container_name) != 1
                or len(set(container_names)) != len(container_names)):
            raise ValueError(f"incomplete {component} containers on {name}")
        for container in containers:
            if (container.get("ready") is not True or container.get("restartCount") != 0
                    or any(not container.get(key) for key in ("name", "image", "imageID", "containerID"))):
                raise ValueError(f"{component} {name} is not ready, restart-free, and identity-complete")
        names.append(name)
    if not names:
        raise ValueError(f"no {component} identities")
    return sorted(names)


def read_snapshot(directory):
    identities = json.loads((directory / "haptic-identities.json").read_text(encoding="utf-8"))
    fleets = {}
    for role, component, container, scalars, vectors in (
        ("controller", "controller", "controller", SCALARS, VECTORS),
        ("agent", "loadbalancer", "agent", AGENT_SCALARS, AGENT_VECTORS),
    ):
        names = component_names(identities, component, container)
        metrics_dir = directory / f"{role}-metrics"
        if {path.name for path in metrics_dir.iterdir()} != {f"{name}.prom" for name in names}:
            raise ValueError(f"counter files do not cover the exact {role} fleet in {directory}")
        fleets[role] = {name: read_counters(metrics_dir / f"{name}.prom", scalars, vectors) for name in names}
    for pod, counters in fleets["agent"].items():
        for kind in ("server", "backend"):
            labels = (("kind", kind), ("outcome", "abandoned"))
            if labels not in counters["haptic_agent_deferred_deletes_total"]:
                raise ValueError(f"missing {kind} abandonment counter on {pod}")
        pending = json.loads((directory / "agent-pending" / f"{pod}.json").read_text(encoding="utf-8"))
        if pending != {"servers": [], "backends": []}:
            raise ValueError(f"agent {pod} still has pending deletes at the lifecycle boundary")
    epoch = Decimal((directory / "epoch.txt").read_text(encoding="utf-8").strip())
    if not epoch.is_finite() or epoch <= 0:
        raise ValueError(f"invalid snapshot epoch in {directory}")
    return identities, epoch, fleets


def exact_sum(values):
    nonzero = [value for value in values if value]
    if not nonzero:
        return Decimal(0)
    highest = max(value.adjusted() for value in nonzero)
    lowest = min(value.as_tuple().exponent for value in nonzero)
    with localcontext() as context:
        context.prec = highest - lowest + len(str(len(nonzero))) + 1
        return sum(nonzero, Decimal(0))


def counter_deltas(before, after, names, filters=None):
    filters = filters or {}
    metrics = []
    for metric in sorted(names):
        per_pod = []
        for pod in sorted(before):
            old, new = before[pod][metric], after[pod][metric]
            if not old.keys() <= new.keys():
                raise ValueError(f"counter series {metric} disappeared on {pod}")
            if any(value < old.get(labels, Decimal(0)) for labels, value in new.items()):
                raise ValueError(f"counter series {metric} decreased on {pod}")
            if metric in filters:
                key, value = filters[metric]
                if any(key not in dict(labels) for labels in new):
                    raise ValueError(f"counter {metric} lacks its {key} label on {pod}")
                old = {labels: count for labels, count in old.items() if dict(labels)[key] == value}
                new = {labels: count for labels, count in new.items() if dict(labels)[key] == value}
            old_sum, new_sum = exact_sum(old.values()), exact_sum(new.values())
            per_pod.append({
                "pod": pod, "before": str(old_sum), "after": str(new_sum),
                "delta": str(exact_sum((new_sum, old_sum.copy_negate()))),
                "advanced": any(value > old.get(labels, Decimal(0)) for labels, value in new.items()),
            })
        metrics.append({
            "metric": metric, "delta": str(exact_sum(Decimal(row["delta"]) for row in per_pod)),
            "per_pod": per_pod,
            "label_filter": dict([filters[metric]]) if metric in filters else {},
        })
    return metrics


def analyze(scenario_dir):
    before_ids, before_epoch, before = read_snapshot(scenario_dir / "before")
    after_ids, after_epoch, after = read_snapshot(scenario_dir / "after")
    if before_ids != after_ids:
        raise ValueError("HAPTIC identity changed across the scenario lifecycle")
    if after_epoch <= before_epoch:
        raise ValueError("scenario lifecycle snapshot interval is not positive")
    metrics = counter_deltas(before["controller"], after["controller"], METRICS)
    metrics += counter_deltas(before["agent"], after["agent"], AGENT_SCALARS | AGENT_VECTORS, AGENT_FILTERS)
    return {
        "schema_version": 2,
        "evidence_valid": True,
        "window": {"before_epoch": str(before_epoch), "after_epoch": str(after_epoch)},
        "scope": "before workload creation through workload teardown and baseline convergence",
        "controller_identities_unchanged": True,
        "agent_identities_unchanged": True,
        "agent_deletions_quiescent_at_boundaries": True,
        "all_counter_series_monotonic_per_pod": True,
        "metrics": metrics,
        "pass": not any(row["advanced"] for metric in metrics for row in metric["per_pod"]),
        "requirement": "all adverse counter deltas are zero across the whole scenario lifecycle",
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--scenario-dir", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        result = analyze(args.scenario_dir)
        args.output.write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    except (OSError, ValueError, KeyError, TypeError, InvalidOperation) as error:
        print(f"Lifecycle outcome evidence is invalid: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
