# ADR-0027: Disable transparent huge pages for HAPTIC processes

## Status

Accepted.

## Context

The 0.2.1 release comparison found both the published 0.2.0 controller and the
candidate exceeding the scale tier's unchanged 1 GiB working-set budget on a
Linux node with transparent huge pages (THP) set to `always`. Their working
sets were 1,276 MiB and 1,300 MiB, while kubelet RSS was 874 MiB and 862 MiB.
Neither process restarted or exhausted its 2 GiB container limit.

Disabling Go heap huge pages for a diagnostic run of the same candidate binary
reduced working set to 861 MiB. CPU usage across the measured workload remained
353 CPU-seconds, compared with 354 before the change. The workload contained
800 Ingresses and 20 Gateways; these numbers describe that experiment, not a
sizing guarantee or a result for larger heaps.

Go's soft memory limit accounts for runtime-managed memory. Linux can retain
additional physical pages when only part of a huge page remains mapped.
That overhead can exceed the headroom left by automatic container sizing.
The [Go GC guide](https://go.dev/doc/gc-guide#Linux_transparent_huge_pages)
describes substantial THP overhead for small heaps and recommends process-level
`PR_SET_THP_DISABLE` as a workaround.

## Decision

Disable THP with `prctl(PR_SET_THP_DISABLE)` when configuring process memory on
Linux, before controller, agent, or validation work starts. Keep automatic
container memory sizing and every memory/performance gate unchanged. Do not
require operator environment variables or changes to node-wide settings.
If the process policy cannot be set, log the failure; automatic memory sizing
still runs.

Use the kernel API rather than the temporary `GODEBUG=disablethp` workaround,
which Go may remove. Non-Linux builds use a no-op.

## Consequences

The policy is local to the HAPTIC process and inherited by its children.
It does not change other workloads or the node's huge-page policy. The syscall
needs no elevated capability under the default container security profile.

Large heaps can benefit from THP, so opting out may cost throughput on workloads
larger than the measured scale tier. Future changes must compare both memory
and CPU before re-enabling THP; a lower RSS alone is insufficient if the cgroup
working set still exceeds its budget.
