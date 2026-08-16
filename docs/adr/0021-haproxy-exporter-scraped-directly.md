# ADR-0021: Prometheus scrapes HAProxy's exporter directly; the exclusion policy lives in HAProxy

## Status

Accepted 2026-08-16.

## Context

The Vector sidecar scraped HAProxy's Prometheus exporter over loopback and
re-exported it on its own port, together with the SPOA hub's metrics and its
own, so Prometheus had one target per pod. HAProxy's `/metrics` answered only
on `127.0.0.0/8` in that mode, and the chart's exclusion policy — `?no-maint`
and `metrics=-<family>` on the scrape URL plus a Vector-side regex filter —
was applied by Vector.

Measured standalone (HAProxy 3.4.3, 2,500 chart-shaped backends, the chart's
rendered `vector.yaml`, Vector 0.57.0), the re-export was the sidecar's memory:

| variant | steady RSS | peak |
|---|---|---|
| full config, no traffic | 1.42 GB | 2.48 GB |
| only the scrape → filter → exporter chain | 1.44 GB | 2.41 GB |
| no HAProxy scrape source | 146 MB | 151 MB |
| scrape every 60 s, jemalloc preload, or glibc trim/mmap tunables | 1.47–1.54 GB | 2.25–2.40 GB |
| full config + 5,000 routes at 500 records/s | 1.82 GB | 2.44 GB |
| no HAProxy scrape source + the same traffic | 364 MB | 698 MB |

HAProxy's exposition through the chart's filter is 280k series, 112 per
backend, 21.9 MB per scrape. Vector holds every retained series at ~1.8 KB
(`prometheus_exporter` sink: 497 MB live) and parses a same-sized batch on every
scrape (`prometheus_scrape` source: 433 MB live). No allocator or interval
setting moves that; it is live data, and it scales with the fleet.

## Decision

Vector no longer scrapes or re-exports HAProxy's exporter. Prometheus scrapes it
directly on the `stats` port on every pod, whether or not the sidecar runs.
Vector keeps the access log, the log-derived and request metrics, and the SPOA
hub's re-export — the hub's series scale per plugin, not per backend, and the
re-export is what lets the hub keep a loopback bind.

The exclusion policy moves into HAProxy. `extraContext.prometheusExporter`
(`excludeMaintServers`, `excludeMetrics.<name>.{enabled, families, requires}`)
renders as

    http-request set-query "<policy>" if { path /metrics } !{ query -m found }

in the status frontend, so a scrape that sends no query gets the chart's policy
and a scrape that sends its own keeps it. Every scraper — the bundled PodMonitor
and a hand-written job alike — therefore sees the same exposition, and
`/metrics?` returns the raw one. Verified on HAProxy 3.0 and 3.4. Regex
exclusions are gone: the exporter filters by exact family name, and every
shipped exclusion already listed its families.

One PodMonitor, `haproxy.monitoring.podMonitor`, replaces `vector.podMonitor`
and `spoaHub.monitoring.podMonitor`: `stats` always, Vector's endpoints while
the sidecar is on, the hub's `metrics` port when the hub is on and the sidecar
off. The retired keys fail the render with their replacement.

## Alternatives considered

- **Keep the re-export as an opt-in.** Its only benefit is one scrape target
  per pod; PodMonitor endpoints make that free. Keeping it would keep the code
  path, the tests, and a 1.3 GB per-pod hazard behind a flag.
- **Scrape parameters on the PodMonitor.** Faithful for PodMonitor users only;
  a hand-written scrape would silently get the unfiltered exposition, and the
  Vector-side regex filter would need a `metricRelabelings` translation.
  Applying the policy in HAProxy covers every scraper with one mechanism.
- **Cut per-backend families.** Rejected: the information becomes too coarse to
  act on. Every metric HAProxy exposed before is still exposed.

## Consequences

- Vector's memory tracks the request metrics (traffic shape, bounded by
  `cardinalityLimit`) rather than the fleet size; `vector.resources` defaults
  drop to a 256Mi request and 1Gi limit.
- Two scrape targets per pod instead of one, both declared by the PodMonitor.
- HAProxy's `/metrics` answers on the pod IP again; the NetworkPolicy already
  allowed the port.
- The exclusion policy applies to every scraper, including one that predates
  the sidecar (`vector.enabled: false`) — previously that path scraped
  unfiltered.
