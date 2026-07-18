# ADR-0009: Auxiliary file UPDATEs always skip the dataplane's auto-reload

## Status

Accepted.

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

2. **`executeFineGrainedSync` gains a post-`PhaseConfig` reload guard**:
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
  only. The chart-side static floor of 4 mirror message slots was
  removed on that basis, but has since been re-added
  (`mirrorMinMessageSlots`, default 4): the floor independently masked
  a second, unrelated race in the SPOA hub's plugin loader (race B,
  spoa-hub issue #47), where a transient render emitting an empty
  `messages` list made the hub unregister the mirror handler on TOML
  reload and silently drop in-flight NOTIFYs. Race B's silent drop is
  fixed upstream as of spoa-hub v0.7.3: reloads quiesce
  (`swap → quiesce → drain → shutdown` — in-flight NOTIFYs complete
  against the pre-swap plugin generation), and NOTIFYs for messages
  absent from the hub's current config are loud (WARN +
  `spoa_messages_unhandled_total{message}`) instead of silent. The
  floor nevertheless stays, for two reasons that survive the upstream
  fix: (1) it is the slot-capacity contract for chart consumers that
  don't participate in dynamic fanout sizing — nginx-ingress's
  `mirror-target` allocates one slot per mirror-target Ingress up to
  the floor and `fail()`s beyond it, and its `send-spoe-group`
  references need the floor-declared groups to pass `haproxy -c`;
  (2) unhandled messages are still not *served* (SPOP has no
  message-level error frame), so a fanout shrink mid-flight loses
  mirrors fired during the hub-TOML-reload-to-HAProxy-reload window —
  the floor keeps slots [1, floor] permanently registered so the
  common small-fanout case never enters that window. Above the floor,
  slot counts still size dynamically from
  `globalFeatures["mirrorMaxFanout"]`; shrinkage within the dynamic
  range correctly shrinks `spoe.conf`, and the guard's explicit
  reload loads the new (smaller) `haproxy.cfg` and `spoe.conf`
  atomically.
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
- **Desired config must reach `executeFineGrainedSync`.** The guard's
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
