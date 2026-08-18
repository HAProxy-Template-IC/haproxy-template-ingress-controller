# ADR-0009: Auxiliary file UPDATEs always skip the dataplane's auto-reload

## Status

Obsoleted by [ADR-0022](0022-haptic-agent.md) on 2026-08-18. The Data Plane API
this decision worked around is gone: the agent writes every file of an apply in
one transaction and reloads once, when the controller tells it to, so there is
no per-file auto-reload left to skip. The context below records why the problem
existed.

## Context

The HAProxy DataPlane API's `PUT /storage/{general,maps,ssl_certificates}/<name>`
endpoint auto-reloads HAProxy by default after writing the new content to
disk. That auto-reload runs against the **current** `haproxy.cfg`, which
became a race during the controller's fine-grained sync:

1. `PhasePreConfig` writes new auxiliary files (spoe.conf, map files,
   certificates, error pages) via `Update*` calls.
2. The dataplane API's auto-reload fires immediately after each PUT.
3. `PhaseConfig` then pushes the new `haproxy.cfg`.

If any auxiliary file change removes a reference the **current** (pre-sync)
`haproxy.cfg` still has — e.g. shrinking `spoe.conf` from
`mirror-1..mirror-4` down to `mirror-1..mirror-3` while the live config
still names `spoe-group mirror-4-group` — HAProxy aborts the reload with
a structured "unknown reference" error and the orchestrator's
"raw config fallback" path got stuck on the same error.

The conformance suite's `SupportHTTPRouteRequestMultipleMirrors` exposed
this when one HTTPRoute carried 3 mirror filters and another carried 1:
removing the route with 3 mirrors shrank `spoe.conf`, the auto-reload
fired against the old `haproxy.cfg` that still referenced the bigger
slot set, and reload failed. The chart-side workaround was a static
floor of 4 mirror slots in `spoe.conf` to keep the file from shrinking
below the live config's reference set. That worked for mirror but
didn't generalise — any future shrinkage (crt-list entries removed,
error-file references dropped, map keys deleted) would hit the same
race.

## Decision

Two changes in `pkg/dataplane/client/`, both on the controller side:

1. **`Update{GeneralFile,MapFile,SSLCertificate}` always sends
   `skip_reload=true`**. Auxiliary content is persisted to disk but
   HAProxy keeps using the in-memory pre-update copy until the next
   reload. Symmetric with the `Create*` path, which already returned
   201 with no auto-reload. The behaviour is pinned by
   `TestUpdateGeneralFile_SendsSkipReload` (and equivalents) so a
   regression that forgets the flag fails at unit-test time.

2. **The sync path (`orchestrator.sync` → `applyChanges`, `pkg/dataplane/orchestrator.go`) gains a post-`PhaseConfig` reload guard**:
   if any auxiliary file had a create-or-update in this sync AND
   `PhaseConfig` did NOT trigger a reload of its own (empty
   `haproxy.cfg` diff, or every config op was runtime-eligible), the
   sync forces an explicit reload by calling `PushRawConfiguration`
   with the desired config. The desired config is threaded through
   from `sync()` to make this possible.

The post-`PhaseConfig` guard closes the corner case that motivated the
original auto-reload: aux-only changes (no `haproxy.cfg` diff) would
otherwise sit on disk indefinitely until the next config change forced
a reload.

## Consequences

- `skip_reload=true` closes the HAProxy reload-ordering race (race A)
  only. Historically the mirror plugin's SPOE messages were sized to the
  number of mirror targets (`mirror-1..mirror-N`), so a shrink in that
  count rewrote `spoe.conf` and the hub TOML — which is what surfaced
  race A here, and a second race B in the hub's plugin loader (spoa-hub
  issue #47) where a transient empty `messages` list dropped in-flight
  NOTIFYs. That per-target sizing is gone: the mirror plugin (v0.6.0+)
  takes a per-request **list** of targets in one static `mirror` message,
  and each frontend mirror source appends its target to a request-scoped
  variable (`txn.gw_mirror_targets`). So `spoe.conf` and the hub TOML no
  longer depend on the mirror-target set at all — adding or removing a
  mirror target changes only the HAProxy frontend, the hub never reloads
  for it, and there is no message-slot count to shrink. Race B's window
  is closed by construction (the mirror `messages` list is a constant),
  and the `mirrorMinMessageSlots` floor knob was removed along with the
  slots. This ADR's `skip_reload` guarantee still matters for the *other*
  auxiliary files whose contents do shrink (crt-list entries, error
  files, map keys).
- The race generalises away: any future auxiliary file type (crt-list,
  error files, additional map files) inherits the same atomic-swap
  semantics — content lands on disk silently, then exactly one reload
  applies new `haproxy.cfg` + new aux files together.
- One extra HTTP roundtrip per sync that touches aux files but has an
  empty config diff (the guard's explicit reload). Sync paths that
  always change `haproxy.cfg` (the common case) are unaffected because
  `PhaseConfig`'s own reload covers the aux updates too.
- The `skip_reload=true` query parameter is a documented dataplane API
  knob, present since HAProxy DataPlane API v2.0 — no version pin
  needed for haptic's supported HAProxy series.

## Constraints

- **No Update path may skip the flag.** Adding a new
  `Update<Resource>File` helper to `pkg/dataplane/client/` requires
  setting `skip_reload=true` and pinning the behaviour with a unit
  test, the same as the existing three. The flag is load-bearing, not
  cosmetic.
- **Runtime-eligible ops still don't trigger reloads.** The guard
  fires only when aux files changed AND `PhaseConfig` produced no
  reload. Runtime API changes (server enable/disable, weight, address)
  apply via the runtime API regardless and don't need a reload.
- **Desired config must reach `orchestrator.applyChanges`.** The guard's
  fallback `PushRawConfiguration` call needs the rendered
  `haproxy.cfg`. Code paths that bypass `sync()` (none today) would
  need to thread the desired config through to the same point or
  forgo the guard.

## Do not re-suggest

- "Just turn the dataplane API's auto-reload back on for safer
  defaults." The auto-reload is what *caused* the race; restoring it
  would re-introduce the shrink-aware-aux-then-stale-haproxy-cfg
  failure mode that the chart had to paper over with a static floor.
  The `Create*` path has been auto-reload-free since the dataplane
  client was first written; the asymmetric `Update*` behaviour was the
  bug, not the new convention.
- "Move the post-`PhaseConfig` guard into the dataplane client so
  every caller gets it." The guard is sync-orchestrator-specific
  (it knows which sync phase ran, whether `PhaseConfig` reloaded, and
  has access to the desired config). Putting it in the client would
  either require the client to track state across calls (it's
  stateless on purpose) or every caller would replicate the logic. The
  orchestrator is the right home.
