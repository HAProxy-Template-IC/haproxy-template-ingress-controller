# spoa-hub-library Specification

## Purpose

Template library wiring the bundled spoa-hub SPOE sidecar into HAProxy: it renders the SPOE engine configuration and the hub's runtime TOML as auxiliary files and emits the SPOP backend, so hub plugins (external-auth, coraza WAF, request mirroring) receive SPOE messages without any controller-side knowledge of the sidecar.

## Requirements

### Requirement: Sidecar Wiring Emission

When spoa-hub is enabled (`extraContext.spoaHub.enabled`; the chart auto-loads the library whenever any spoaHub plugin is enabled), the library SHALL register two auxiliary files via the file registry — `spoe.conf` (the SPOE agent block, per-message bodies, and per-message groups) and `spoa-hub-config.toml` (the hub's runtime config with one plugins block per enabled plugin) — and SHALL emit a `backend spoa-hub` pointing at the hub's UNIX socket (default `/run/spoa/hub.sock`). The backend SHALL use `mode spop` on HAProxy 3.1 and newer and fall back to `mode tcp` on older versions. The sidecar picks up TOML changes via its file watch and graceful reload; no pod restart is involved.

#### Scenario: Disabled library emits nothing

- **WHEN** spoa-hub is not enabled
- **THEN** no spoe.conf or spoa-hub-config.toml SHALL be registered and no spoa-hub backend SHALL be emitted.

#### Scenario: The sidecar is never deployed alongside a dormant library

- **WHEN** `spoaHub.enabled` is forced true but no enabled plugin contributes an SPOE message
- **THEN** chart rendering SHALL fail. The sidecar SHALL NOT be deployed with the library dormant, because the chart's bootstrap initContainer seeds `spoa-hub-config.toml` into general storage while the library renders no runtime TOML, and the controller's general-file sync would orphan-delete the file out from under the running hub.

#### Scenario: SPOP mode falls back on HAProxy 3.0

- **WHEN** the injected haproxyVersion is 3.0
- **THEN** the spoa-hub backend SHALL use `mode tcp` instead of `mode spop`.

### Requirement: Mirror Uses a Single Static Message and Group

The mirror plugin SHALL use exactly one SPOE message named `mirror` and one group named `mirror-group`, independent of how many mirror targets exist. The message SHALL carry a per-request list of targets via `arg_targets=var(txn.gw_mirror_targets)` plus the shared request-line/body args (`arg_method`, `arg_path`, `arg_query`, `arg_ver`, `arg_hdrs`, `arg_body`). Each frontend mirror source (a Gateway `RequestMirror` filter, an Ingress `mirror-target` annotation) SHALL append one `scheme|host:port|timeout_ms|retries` entry to `txn.gw_mirror_targets`, gated by that source's match (and sampling) condition, using `set-var(...) str(<entry>;),concat(,txn.gw_mirror_targets,)`. The timeout and retry fields SHALL come from the mirror-specific bounded chart values, not the application backend budget. A single resource-agnostic snippet SHALL fire `send-spoe-group spoa-hub-mirror mirror-group` once per request, after all appends, guarded by `{ var(txn.gw_mirror_targets) -m found }`, and only when the mirror plugin is enabled. The mirror plugin (haproxy-spoa-hub-plugin-mirror v0.6.0+) SHALL split the list and dispatch one fire-and-forget request per entry. Because the SPOE message set does not depend on the mirror-target set, `spoe.conf` and the hub TOML SHALL NOT change when mirror targets are added or removed, and the hub SHALL NOT reload for a mirror-target change. There SHALL be no per-target message-slot count, no `minMessageSlots` floor, and no upper bound on the number of mirror targets.

#### Scenario: No mirror sources still declares the static message

- **WHEN** the mirror plugin is enabled and no route or Ingress declares a mirror
- **THEN** `spoe.conf` SHALL declare the single `mirror` message and `mirror-group`, and no numbered `mirror-<i>` message or group SHALL appear.

#### Scenario: Adding targets does not change the SPOE config

- **WHEN** mirror-target Ingresses or `RequestMirror` filters are added or removed
- **THEN** the `mirror` message, `mirror-group`, and the mirror plugin's TOML `messages` list SHALL be byte-identical, and only the HAProxy frontend `txn.gw_mirror_targets` appends SHALL change.

#### Scenario: One rule with several mirror filters fans out from one group

- **WHEN** a single route rule carries several `RequestMirror` filters
- **THEN** each filter SHALL append its own entry to `txn.gw_mirror_targets`, the single `mirror-group` SHALL be fired once, and the plugin SHALL dispatch one request per entry.

#### Scenario: Many mirror-target Ingresses do not fail the render

- **WHEN** five or more host-gated mirror-target Ingresses are present
- **THEN** each SHALL append its entry, the render SHALL succeed, and the SPOE config SHALL remain the single static message and group.

### Requirement: Per-Message Singleton Groups

Each SPOE message SHALL get its own singleton group named `<message>-group` so frontends trigger it explicitly via `send-spoe-group`. Per-message bodies SHALL be resolved per message: known plugin messages get their canonical body, and any other message gets a minimal stub so the agent's group references always parse.

#### Scenario: Unknown message gets a stub body

- **WHEN** the configured messages list contains a message no bundled plugin defines a body for
- **THEN** spoe.conf SHALL contain a minimal spoe-message stub and a `<message>-group` for it, keeping the config parseable.
