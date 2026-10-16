# ADR-0036: Pace a follower's input acceptance

## Status

Accepted 2026-10-16. Amends ADR-0032 and ADR-0035 for followers.

## Problem

A follower's render warmer ran input acceptance (ADR-0032) for every
reconciliation trigger, so it ran one `haproxy -c` per watched-resource change
for renders it never deploys. With EndpointSlice churn that is one check per
endpoint change (issue #335).

Measured on kind with two replicas, 2,000 Ingresses, 20 Gateways, a 1.15 MB
`haproxy.cfg`, and one EndpointSlice changing about 65 times a minute, over
120 seconds on the follower:

| | Before | After |
|---|---|---|
| `haproxy -c` runs (`haptic_haproxy_checks_total`) | 131 | 24 |
| PIDs consumed (`ns_last_pid` delta) | 261 | 48 |
| Controller process CPU | 0.49 cores | 0.27 cores |
| Reaped child CPU | 0.136 cores | 0.024 cores |

Every `haproxy -c` run, and `haproxy -v`, consumes two PIDs: the process and
one thread, whatever `nbthread` is. The issue's ~157 PIDs a minute match about
78 checks, one per render.

## Decision

The warmer starts a follower render at most once every 5 seconds. Triggers in
between coalesce into the next render, which reads the stores at that moment.
The first trigger after an idle period renders at once, so a hand-over still
waits only for one render. The leader's Coordinator is unchanged: it accepts
every trigger's render.

## Validation delta

No check is removed from any input that is accepted or any output that is
deployed:

- Every follower acceptance still runs the complete render and `haproxy -c`.
  Pacing changes how often the follower accepts, never what it accepts.
- A follower deploys nothing. A new leader's Coordinator accepts and the render
  gate checks every render before deployment, starting from the follower's last
  validated baseline. A stale baseline means a larger change set, which input
  isolation handles exactly like any other set.
- Admission on a follower validates its complete proposed view itself. It uses
  the accepted state only to withhold revisions reconciliation already
  rejected, and that state was always as old as the last acceptance. Pacing
  makes it up to 5 seconds older. In that window a follower may refuse an
  unrelated proposal because of an input it hasn't rejected yet, or judge a
  proposal without a rejected revision that has since become valid. Neither
  reaches the fleet unchecked: input acceptance validates every change again
  before it's deployed.

The follower's accepted state, the `haptic_rejected_watched_inputs` gauge and
its graph lag the observed state by at most the interval plus one render.
