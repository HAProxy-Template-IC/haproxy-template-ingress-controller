# spoa-hub-library Specification

## Purpose

Template library wiring the bundled spoa-hub SPOE sidecar into HAProxy: it renders the SPOE engine configuration and the hub's runtime TOML as auxiliary files and emits the SPOP backend, so hub plugins (external-auth, coraza WAF, request mirroring) receive SPOE messages without any controller-side knowledge of the sidecar.

## Requirements

### Requirement: Sidecar Wiring Emission

When spoa-hub is enabled (`extraContext.spoaHub.enabled`; the chart auto-loads the library whenever any spoaHub plugin is enabled), the library SHALL register two auxiliary files via the file registry — `spoe.conf` (the SPOE agent block, per-message bodies, and per-message groups) and `spoa-hub-config.toml` (the hub's runtime config with one plugins block per enabled plugin) — and SHALL emit a `backend spoa-hub` pointing at the hub's UNIX socket (default `/run/spoa/hub.sock`). The backend SHALL use `mode spop` on HAProxy 3.1 and newer and fall back to `mode tcp` on older versions. The sidecar picks up TOML changes via its file watch and graceful reload; no pod restart is involved.

#### Scenario: Disabled library emits nothing

- **WHEN** spoa-hub is not enabled
- **THEN** no spoe.conf or spoa-hub-config.toml SHALL be registered and no spoa-hub backend SHALL be emitted.

#### Scenario: SPOP mode falls back on HAProxy 3.0

- **WHEN** the injected haproxyVersion is 3.0
- **THEN** the spoa-hub backend SHALL use `mode tcp` instead of `mode spop`.

### Requirement: Mirror Message-Slot Sizing With a Static Floor

The mirror plugin's message list SHALL be sized max(mirrorMaxFanout, mirrorStaticMinSlots), where mirrorMaxFanout is the cluster-wide maximum number of mirror filters on any single route rule (written into globalFeatures by the producing routing library under a resource-neutral key) and mirrorStaticMinSlots defaults to 4 (`extraContext.spoaHub.mirrorStaticMinSlots`). The static floor SHALL NOT be removed: it masks the hub's plugin-reload race (race B, spoa-hub issue #47) — the hub rebuilds plugin handler state from scratch on every TOML reload, so a transient render that emits an empty messages list unregisters the mirror handler and every in-flight `send-spoe-group` NOTIFY is silently dropped (observed as ~50% mirror-rate conformance flakiness). The controller-side `skip_reload` on auxiliary-file updates fixed only the HAProxy reload-ordering race (race A) and does not close race B. The HAProxy-side SPOE message and group declarations SHALL be sized in lockstep with the plugin-side floor.

#### Scenario: Floor holds with no mirror routes

- **WHEN** no route in the cluster carries a mirror filter
- **THEN** the mirror plugin's messages list and the SPOE groups SHALL still cover mirror-1 through mirror-4.

#### Scenario: Plugin and engine sizing stay in lockstep

- **WHEN** the effective slot count is N
- **THEN** the TOML messages list and the spoe.conf message and group declarations SHALL both cover mirror-1 through mirror-N.

### Requirement: Dynamic Extension Beyond the Floor

Fanout above the floor SHALL extend the slot set dynamically: a rule carrying N mirror filters with N greater than the floor yields mirror-1 through mirror-N. Each mirror filter needs its own (message, group) pair because HAProxy SPOE processes each group at most once per stream. Slots above the floor remain exposed to race B until the upstream plugin loader stops unregistering already-registered messages on reload; mirrorStaticMinSlots SHALL be operator-tunable so workloads that regularly exceed the floor can raise it.

#### Scenario: High-fanout rule extends the slots

- **WHEN** a route rule carries 12 mirror filters
- **THEN** 12 mirror messages and 12 corresponding groups SHALL be emitted.

### Requirement: Per-Message Singleton Groups

Each SPOE message SHALL get its own singleton group named `<message>-group` so frontends trigger it explicitly via `send-spoe-group`. Per-message bodies SHALL be resolved per message: known plugin messages get their canonical body, and any other message gets a minimal stub so the agent's group references always parse.

#### Scenario: Unknown message gets a stub body

- **WHEN** the configured messages list contains a message no bundled plugin defines a body for
- **THEN** spoe.conf SHALL contain a minimal spoe-message stub and a `<message>-group` for it, keeping the config parseable.
