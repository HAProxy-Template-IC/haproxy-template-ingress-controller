# ADR-0011: No HAProxy server-state-file — server addresses are config-driven

## Status

Accepted. Implementation on branch `fix/endpoint-conditions-filter`: removed
`server-state-file` / `load-server-state-from-file` / per-server `init-addr last`
from `charts/haptic/libraries/base.yaml`, the `@1 show servers state` dump from
the `reload.sh` wrapper in `charts/haptic/templates/haproxy-deployment.yaml`, and
the `init-addr` entry from `serverRuntimeSupportedJSONFields` in
`pkg/dataplane/comparator/sections/factory_server.go`. Supersedes the
server-state-preservation work in commits `518e1d9e` and `dc6f1133` (which never
shipped in a release).

## Context

HAPTIC renders HAProxy backends with a fixed number of **reserved server slots**
per backend: active slots carry a pod IP and `enabled`, unused slots are a
disabled placeholder. Endpoint changes (pod IP rotation, scale up/down) are
applied to the **live worker via the runtime API** (`set server … addr … port …`,
`set server … state …`) with no reload — only genuinely structural changes
reload. This keeps a single-replica rolling restart converging in milliseconds.

Commit `518e1d9e` ("preserve runtime API server changes across reloads") added
the standard HAProxy server-state machinery so that a runtime `set server`
landing on the live worker would survive a co-batched structural reload:

- `server-state-file <baseDir>/server-state` (global),
- `load-server-state-from-file global` (defaults),
- `init-addr last,<address>` on every rendered server line,
- a `reload.sh` `reload_cmd` that dumps `@1 show servers state` before reloading.

While stabilising the `TestIngressRollingRestartZeroDowntime` e2e under the full
parallel suite, a reproducible `HTTP 400` was traced to a backend server that,
**after a reload**, was serving `127.0.0.1:80` — i.e. pointing at HAProxy's own
`bind *:80`. Requests looped through the h2c loopback, accreting an
`x-forwarded-for` per pass until `tune.http.maxhdr` (101) tripped → 400. The
address `127.0.0.1` came from a placeholder, the port `:80` from the
state-file — a hybrid produced on reload.

## The finding: `init-addr last` does not preserve an IP-literal address

Investigating why the reload produced that hybrid revealed that the
address-preservation premise of `518e1d9e` is invalid for HAPTIC's servers.
Confirmed three ways:

1. **HAProxy source** (`/home/phil/Quellcode/haproxy`, also true on 3.0–3.3):
   - `src/server_state.c` loads the state-file address only into
     `srv->lastaddr` (`if (strcmp(params[0], "-") != 0) srv->lastaddr = strdup(...)`);
     it does **not** set the server address. The **port**, by contrast, is
     applied directly (`srv->svc_port = port_svc`).
   - `srv->lastaddr` is consumed only by the `init-addr` resolution chain
     (`srv_iterate_initaddr` → `case SRV_IADDR_LAST` → `srv_apply_lastaddr`).
   - `srv_init_addr()` runs that chain **only for FQDN / DNS-SRV servers**:
     `for (...) if (srv->hostname || srv->srvrq) srv_iterate_initaddr(srv);`.
     HAPTIC's servers are IP literals (pod IPs from EndpointSlices) — no
     `hostname`, no `srvrq` — so the chain never runs and `lastaddr` is never
     applied. The parsed config address always wins on reload.

2. **Controlled master-worker reload** (HAProxy 3.3.9): runtime-set a server to
   `203.0.113.30:5555`, dump state (file shows `203.0.113.30 … 5555`), reload →
   the server comes back as `198.51.100.20:5555` — **config address, state-file
   port**. Identical with `init-addr last`, with no `init-addr`, and with bare
   `init-addr last`: the directive makes no difference for an IP literal.

3. **HAProxy docs + community**: `init-addr` governs how a server's address is
   *resolved at startup if it uses an FQDN*; the DNS tutorial states it is "not
   [for] literal IP addresses". Multiple tracking issues report the same: on
   reload with a state file, port and status are restored but the IP is not.

So the state-file machinery preserved the **port, op-state, admin and weight**,
but never the **address** — the one thing the rolling restart needed. Its port
restoration is exactly what minted the `127.0.0.1:80` hybrid (config placeholder
address + stale state-file port on a vacated slot).

## Decision

Remove the server-state-file machinery. Server addresses **and** ports are
config-driven across reloads. Correctness rests on two properties HAPTIC already
has:

- The deploy pipeline always renders the **current** endpoints into the config
  (the single deploy-loop, ADR-adjacent to the deployer redesign, makes every
  scheduled deploy use the latest pending render), so a config-driven reload is
  internally consistent — a vacated slot reloads as a clean placeholder, never a
  hybrid.
- Endpoint changes still converge in milliseconds via the runtime fast path
  between reloads (no reload needed for a pure address/state change).

Defense in depth: placeholder slots use the RFC 5737 TEST-NET-1 sentinel
`192.0.2.1`, not `127.0.0.1`. Even if some future path produced a
placeholder-address + active-port slot, `192.0.2.1:<port>` cannot reach
`bind *:80`, so the worst case is a fast `timeout connect` + `option redispatch`
failover rather than an unbounded self-loop.

## Consequences

- **Address**: unchanged in practice — it was already config-driven (the
  state-file never restored it). The runtime fast-path address change is applied
  live; a reload reflects the rendered config.
- **Op-state / health**: no longer carried across a reload, so checked servers
  re-evaluate health on reload. With reserved slots that are `enabled` in config
  and an optimistic initial check state this is normally a non-event; verified
  under e2e reload churn.
- **The `reload_cmd` / `master_worker_mode: false` plumbing is now vestigial** —
  `reload.sh` is a plain master-socket reload, functionally identical to
  dataplaneapi's internal reload. A follow-up may set `master_worker_mode: true`
  and drop the custom `reload_cmd` entirely.

## Alternatives considered

1. **Make `init-addr last` work for our servers** — rejected. It only applies to
   FQDN / DNS-SRV servers (source above). There is no option to make
   `load-server-state-from-file` apply `lastaddr` to an IP-literal server.

2. **DNS / `server-template` + `resolvers`** (the only HAProxy-native way to
   *truly* preserve a dynamic server's address across reloads, and to re-resolve
   without reloading) — rejected for now. It requires rendering servers as FQDNs
   backed by a DNS source for endpoints (per-backend headless Services / pod DNS),
   which abandons the EndpointSlice-IP model, adds DNS-resolution latency, and
   couples the engine to Kubernetes DNS — against HAPTIC's resource-agnostic
   design (RULE #1). Keep as a documented fallback if cross-reload address
   preservation ever becomes a hard requirement.

3. **Keep the state file + rely only on the `192.0.2.1` sentinel** — rejected.
   The sentinel makes the hybrid non-catastrophic but leaves the slot transiently
   *wrong* (an unroutable server) rather than correct; config-driven reloads make
   it correct.

## Suggestion: preserving in-memory server state without the state file

There is no zero-window way to carry an IP-literal server's address through a
reload — HAProxy re-reads the config on reload and cannot restore the address.
The pragmatic answer within the EndpointSlice-IP model is to make the **rendered
config the single source of truth** and shrink the reload window:

1. **Config-as-truth (in place):** every reload renders current endpoints, so the
   post-reload state is correct without any preservation.
2. **Runtime fast path (in place):** endpoint changes apply live with no reload,
   so most convergence never touches a reload at all.
3. **Re-apply after a reload (suggested next step):** when a structural deploy
   completes, re-fire the runtime fast path for the current endpoints against the
   fresh worker, so a change that landed on the now-replaced worker is carried
   over. This shrinks — but does not fully eliminate — the boot window; the
   `192.0.2.1` sentinel + `option redispatch` keep that residual window
   non-catastrophic.
4. **Minimize reload triggers (suggested):** keep endpoint deltas on the runtime
   path and avoid co-batching them into structural reloads where feasible.

If a hard "no address loss across reloads" guarantee is ever required, the only
real option is alternative (2), DNS/`server-template` — adopted deliberately,
with its resource-agnosticism trade-off understood.
