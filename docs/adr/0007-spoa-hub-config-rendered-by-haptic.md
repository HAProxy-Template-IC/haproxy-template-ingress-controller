# ADR-0007: SPOA hub config is rendered by HAPTIC, hot-reloaded by the hub

## Status

Accepted.

All four implementation steps merged: MR1 !147 (hub file-watch + reload
metrics), MR2 !900 (chart shared emptyDir + bootstrap), MR3 !901 (chart
libraries render the full hub TOML), MR4 !902 (admission validation +
e2e test).

## Context

The SPOA hub sidecar today gets its `config.toml` from a Helm-rendered ConfigMap (`charts/haptic/templates/spoa-hub-configmap.yaml`) mounted read-only into `/etc/haproxy-spoa-hub/config.toml`. That works for static plugin params (a fixed coraza directive block, a fixed external-auth params block, etc.) but blocks every operator workflow that needs the hub config to depend on watched-resource state. The motivating example is `nginx.ingress.kubernetes.io/modsecurity-snippet`: per-Ingress ModSecurity rules need to land in the runtime hub's coraza section, but Helm-time rendering can't see Ingresses. Operators today either (a) hand-curate the global `directives` block in chart values to cover every Ingress they own — unscalable — or (b) ship rules via per-request SPOE messages, which moves the rule text on every request and isn't what the hub or coraza were designed for.

Two related pieces are already in place:

- `pkg/controller/pluggablevalidator` (MR !895/!896) dispatches each rendered file to validator sidecars over a Unix socket and surfaces line/col diagnostics in the admission response. The chart wires a `haproxy-spoa-hub --validate-socket` sidecar in the controller pod for this; `docs/site/docs/operations/pluggable-validators.md` documents the contract and uses `/etc/haproxy-spoa-hub/*.toml` as the reference glob.
- The hub binary supports `--validate-socket` mode (used by the validator sidecar). Its runtime mode currently expects a static config path passed via `--config`.

What's missing is the runtime delivery side: how the rendered hub TOML reaches the runtime sidecar and how the hub picks it up without restarting.

## Decision

HAPTIC owns the SPOA hub's runtime config end to end:

1. **Bootstrap.** The runtime SPOA hub container starts with a *minimal placeholder* `config.toml` (just `[hub] listen = "..."` plus an empty plugins section) so the sidecar can come up before HAPTIC has rendered anything. The placeholder is shipped as a tiny ConfigMap or baked into the hub image — it's not the operator-tunable config.
2. **Rendered config.** A new template snippet in the chart libraries renders the full hub `config.toml`, including:
   - `[hub]` settings from `spoaHub.haproxy.*` values
   - One `[plugins.params.<name>]` block per enabled plugin, sourced from `spoaHub.plugins.<name>.params`
   - For coraza specifically: per-Ingress `modsecurity-snippet` annotation values **inlined** into the coraza section's `directives` (as appended `Sec*` lines), so each Ingress's rules are present in the rendered TOML without per-request SPOE shipping
   The snippet calls `fileRegistry.Register("file", "spoa-hub-config.toml", rendered)`. The filename is **flat** (no slash) because the DataPlane API's storage layer sanitizes any `/` in a filename to `_` before writing — verified in `client-native/storage/storage.go:198` (`SanitizeFilename`) and the test fixture `stringutil_test.go:42-45`. There is no subdirectory support.
3. **Push.** The rendered file flows through the existing dataplane API push path — same machinery as `general/400.http`, `maps/host.map`, etc. The dataplane writes it to the haproxy pod's filesystem at `<GeneralStorageDir>/spoa-hub-config.toml` (e.g. `/etc/haproxy/general/spoa-hub-config.toml` in production).
4. **Shared volume.** The chart's haproxy deployment grows one new shared `emptyDir` mount. HAProxy mounts it at `<GeneralStorageDir>` (so the dataplane API can write the rendered file). The spoa-hub container mounts it at `/etc/haproxy-spoa-hub/`, with `--config /etc/haproxy-spoa-hub/spoa-hub-config.toml` passed as the binary's argument. Both containers see the same file via the shared volume; the dataplane API uses `renameio` (atomic rename), so the hub observes a consistent file at all times.
5. **Reload trigger.** The hub binary today reloads on **SIGHUP** (`crates/hub/src/main.rs:145-162`), atomically swapping the plugin registry via `arc_swap::ArcSwap` and keeping the previous config on parse / plugin-init failure (`bail!` on line 256). It does **not** watch its config file. MR1 (Step 1 in the sequence below) adds a file-watch on the `--config` argument so HAPTIC pushing a new file triggers the same reload path SIGHUP already uses. The keep-current-on-failure and atomic-swap semantics are unchanged — file-watch only adds the trigger, the reload mechanics are reused.
6. **Validation gate.** Before HAPTIC pushes a new config to the dataplane, the admission webhook routes the same rendered TOML to the `--validate-socket` sidecar (`spec.validators[]` glob `general/spoa-hub-config.toml`). A broken modsecurity-snippet (or any other invalid plugin param) is rejected at `kubectl apply` time, before the file ever reaches the runtime hub. The graceful-reload behavior in step 5 is a defense in depth for cases the validator misses (e.g. an operator changing the chart values out from under a controller restart).

## Consequences

- **Per-Ingress rules work end to end.** A `nginx.ingress.kubernetes.io/modsecurity-snippet` annotation produces appended Sec-rules in the rendered hub config; the runtime hub picks them up on file change; validation rejects broken snippets at admission. No per-request SPOE rule shipping.
- **One config source.** Helm operators no longer hand-edit `spoaHub.plugins.<name>.params` to encode runtime-changing state. Static plugin tuning still goes in values; per-resource state lives in the templates.
- **One less Helm-managed ConfigMap.** `templates/spoa-hub-configmap.yaml` is replaced by a tiny placeholder; the operator-facing config surface in `values.yaml` doesn't change.
- **Hub binary already does graceful reload + keep-current-on-failure; needs file-watch trigger.** Confirmed against the hub source: `arc_swap`-based atomic swap is in place, `bail!` keeps the current config when the new one fails to parse or plugin init returns an error. The only missing piece is file-watch — today the hub only reloads on SIGHUP. MR1 adds the file-watch trigger; the rest of the reload machinery is reused as-is.
- **Reload observability is logs-only today.** The hub emits structured tracing logs on SIGHUP receipt and on reload success / failure. There are no Prometheus metrics named `spoa_hub_config_reload_*`. MR1 should add at minimum a success-counter and a failure-counter so operators can alert on stalls; gauge for "seconds since last successful reload" is nice-to-have.
- **Two-instance config drift between push and reload becomes possible.** If HAPTIC pushes config N+1 but the hub hasn't reloaded yet, requests during that window run against config N. That's intentional (graceful failure mode); the metric above is what operators use to spot stalls.
- **Dataplane API does not support subdirectory paths in storage names.** Verified in `client-native/storage/storage.go:198` — `SanitizeFilename` collapses `/` to `_` before writing. So the rendered file uses a flat name (`spoa-hub-config.toml`), the validator glob is the corresponding flat path (`general/spoa-hub-config.toml`), and the hub's `--config` arg points at the shared volume's mount path. Operators wanting to inject extra TOML fragments cannot use a subdirectory layout; if multi-file support is later needed, it would require a chart-side concatenation step or a dataplane-side change upstream.
- **Path glob in `pluggable-validators.md` example needs updating.** That doc currently uses `/etc/haproxy-spoa-hub/*.toml` as the validator glob example. With the flat-name finding, the actual production path is `general/spoa-hub-config.toml` (path-resolver-relative). The doc's example will be updated as part of MR4 to match what HAPTIC actually emits.

## Implementation sequence

This rework needs four MRs landing in this order. Each unlocks the next:

1. **MR1 — hub file-watch trigger + reload metrics** (in `haproxy-spoa-hub`): add file-watch on the `--config` argument that calls into the existing SIGHUP reload path. Reuse the in-place `arc_swap` swap and `bail!`-on-failure semantics; do not change them. Add `spoa_hub_config_reload_success_total` / `spoa_hub_config_reload_failure_total` counters and a `spoa_hub_config_reload_age_seconds` gauge. Unit test: feed a valid file then a broken one, assert the hub still serves the valid rules and the failure counter incremented.
2. **MR2 — chart wiring**: drop the rendered config block from `templates/spoa-hub-configmap.yaml` (replace with a placeholder), add a shared `emptyDir` mounted into both the haproxy and spoa-hub containers, point the spoa-hub container's `--config` at the shared mount path. Do *not* yet emit any HAPTIC-rendered config — the hub starts on the placeholder and stays there until MR3.
3. **MR3 — controller-side rendering**: a chart-library snippet that renders the full hub `config.toml` and registers it via `fileRegistry.Register("file", "spoa-hub-config.toml", rendered)` (flat filename). Smoke-tested with no per-Ingress annotations (chart values only), then extended to inline per-Ingress modsecurity-snippet.
4. **MR4 — admission validation + e2e test**: add the `spec.validators[]` entry pointing at the existing `--validate-socket` sidecar with the glob `general/spoa-hub-config.toml`; update `docs/site/docs/operations/pluggable-validators.md` to use the actual flat path in its example (replacing the speculative `/etc/haproxy-spoa-hub/*.toml`); restore the e2e test from the closed !898 branch with the assertions adapted to the new file path.

## Do not re-suggest

If you find yourself proposing a chart that emits one file per Ingress under `general/coraza/*.conf` (or any other per-resource fanout for plugin rules), stop. That was tried in !898 and failed both architecturally (the runtime hub never reads `general/coraza/`) and practically (the runtime sidecar segfaulted on a too-thin coraza directive block). The only correct shape is "rendered hub TOML, one file, watched by the hub, validated by the validator sidecar."

If you find yourself proposing per-request SPOE rule shipping (sending the modsecurity-snippet text on every SPOE message), stop. That was the pre-ADR fallback and the explicit reason for this ADR — moving rule text per-request defeats the SPOA architecture.

If the hub binary turns out not to support file-watch + graceful-reload, *don't* simulate it from HAPTIC's side (e.g. by deleting and recreating the spoa-hub pod on every config change). The hub-side change is the right place; do it properly there.

## Related

- `docs/site/docs/operations/pluggable-validators.md` — documents the validator wire-up that will gate this ADR's runtime config push. The reference glob `/etc/haproxy-spoa-hub/*.toml` already implies the file path this ADR commits to.
- `docs/development/validator-protocol.md` — the wire protocol the validator sidecar speaks. No changes needed.
- ADR-0001 — establishes that synchronous direct calls beat event hops when there's only one publisher and one subscriber. This ADR's "render → push → file-watch → reload" loop is asynchronous by necessity (the hub reload is local to the hub process, not HAPTIC); ADR-0001 doesn't apply.
