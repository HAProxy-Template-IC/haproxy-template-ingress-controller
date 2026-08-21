# Supported HAProxy Configuration

HAPTIC deploys any configuration HAProxy accepts; the tables below list every section the bundled libraries and the validation surface know about, with what a change to each one costs.

## Overview

The rendered bytes reach the pod unchanged, and the pod's own HAProxy binary is what judges them, so any directive your HAProxy release accepts can be deployed. What decides the *cost* of a change is what the render declares about its own structure: the controller compares two renders and asks whether every difference is something HAProxy has a runtime command for.

**HAProxy version support:** HAProxy 3.0 through 3.4. Which runtime commands a pod accepts follows its own version, reported by the agent — on 3.4 a route add can create a backend without a reload, below it a new backend reloads.

**HAProxy Editions:** Both HAProxy Community and HAProxy Enterprise are supported. Enterprise-only features are automatically detected and enabled when connected to an Enterprise instance.

**Coverage:** A section HAProxy parses is deployable whether or not it appears below. The tables list what the bundled libraries emit and what the reload-free lane covers; a section outside them still deploys, it just reloads when its text changes.

To get a feel for the breadth, here's every bundled library rendered into one config:

<div class="pg-embed" markdown data-scenario="all" data-facade="spec.watchedResources" data-tab="haproxy.cfg" data-controls="tabs" data-title="Every bundled library rendered into one haproxy.cfg" data-height="440">

</div>

## Supported configuration sections

| Section | Description | Priority | Implementation |
|---------|-------------|----------|----------------|
| **Global** | Global HAProxy settings (singleton) | 5 | Update only |
| **Defaults** | Default settings for proxies | 8 | Create/Update/Delete |
| **Frontends** | Frontend proxy definitions | 20 | Create/Update/Delete |
| **Backends** | Backend server pools | 30 | Create/Update/Delete |
| **Peers** | Peer sections for stick-table replication | 10 | Create/Update/Delete |
| **Resolvers** | DNS resolver configurations | 10 | Create/Update/Delete |
| **Mailers** | Email alert configurations | 10 | Create/Update/Delete |
| **Caches** | Cache configurations | 10 | Create/Update/Delete |
| **Rings** | Ring buffer configurations | 10 | Create/Update/Delete |
| **HTTPErrors** | HTTP error response sections | 10 | Create/Update/Delete |
| **Userlists** | User authentication lists | 10 | Create/Delete (no update) |
| **LogForwards** | Syslog forwarding sections | 10 | Create/Update/Delete |
| **LogProfiles** | Log-format profiles (HAProxy 3.1+) | 10 | Create/Update/Delete |
| **Traces** | Traces section (singleton, HAProxy 3.1+) | 10 | Update only |
| **FCGIApps** | FastCGI application configs | 10 | Create/Update/Delete |
| **CrtStores** | Certificate store sections | 10 | Create/Update/Delete |
| **AcmeProviders** | Automatic Certificate Management Environment (ACME) certificate provider configurations | 10 | Create/Update/Delete |

**Note:** Lower priority numbers are processed first. Operations are automatically ordered by dependency and priority.

## Child components by section

### Frontend child components

Frontends support these child component types with individual Create/Update/Delete operations:

| Component | Description |
|-----------|-------------|
| **Binds** | Listen addresses and ports |
| **ACLs** | Access control lists |
| **HTTP Request Rules** | HTTP request processing rules |
| **HTTP Response Rules** | HTTP response processing rules |
| **TCP Request Rules** | TCP request processing rules |
| **Backend Switching Rules** | Dynamic backend selection rules |
| **Filters** | Data filters (compression, trace, etc.) |
| **Captures** | Request/response capture declarations |
| **Log Targets** | Logging destinations |
| **QUIC Initial Rules** | Per-frontend rules applied to QUIC initial packets (HTTP/3); requires HAProxy 3.1+ |

### Backend child components

Backends support these child component types with individual Create/Update/Delete operations:

| Component | Description |
|-----------|-------------|
| **Servers** | Backend server definitions |
| **Server Templates** | Dynamic server templates |
| **ACLs** | Access control lists |
| **HTTP Request Rules** | HTTP request processing rules |
| **HTTP Response Rules** | HTTP response processing rules |
| **HTTP After Response Rules** | Post-response processing rules |
| **TCP Request Rules** | TCP request processing rules |
| **TCP Response Rules** | TCP response processing rules |
| **Server Switching Rules** | Dynamic server selection rules |
| **Stick Rules** | Session persistence rules |
| **Filters** | Data filters |
| **HTTP Checks** | HTTP health check configurations |
| **TCP Checks** | TCP health check configurations |
| **Log Targets** | Logging destinations |

### Mailers, resolvers, and peers child components

The following sections support fine-grained child component management:

**Mailers** - Individual mailer entry operations:

| Component | Description |
|-----------|-------------|
| **Mailer Entries** | SMTP server definitions for email alerts |

**Resolvers** - Individual nameserver operations:

| Component | Description |
|-----------|-------------|
| **Nameservers** | DNS server definitions for service discovery |

**Peers** - Individual peer entry operations:

| Component | Description |
|-----------|-------------|
| **Peer Entries** | Peer server definitions for stick-table replication |

### Other section components

The following sections use **whole-section comparison** via the models' `.Equal()` method, which includes all nested components:

- **Rings**: All ring attributes
- **HTTPErrors**: Includes errorfiles
- **Userlists**: Includes users and groups
- **LogForwards**: Includes log targets
- **FCGIApps**: Includes pass-header and set-param directives
- **CrtStores**: Includes crt-load entries

## Reload behavior

The controller skips the HAProxy reload when every change in a push can be applied through the Runtime API. The sections below list exactly which changes qualify.

!!! note "Reloads are seamless"
    When a change *does* require a reload, HAProxy reloads seamlessly: the new worker takes over new connections while established connections keep running on the old worker, which drains them before exiting. Requests aren't dropped, so even reload-required changes are effectively zero-downtime. Skipping the reload (above) still matters — it avoids forking a fresh worker process at all — but a reload isn't a traffic outage.

### Zero-reload operations (runtime API)

The following changes are applied **without reloading HAProxy**:

#### Server modifications (specific fields only)

Server modifications avoid reloads **only** when changing these Runtime API-supported fields:

| Field | Description | API Endpoint |
|-------|-------------|--------------|
| **Weight** | Server weight for load balancing | Runtime API `/runtime/servers` |
| **Address** | Server IP address | Runtime API `/runtime/servers` |
| **Port** | Server port number | Runtime API `/runtime/servers` |
| **Maintenance** | Enable/disable/drain server state | Runtime API `/runtime/servers` |
| **AgentCheck** | Agent check status | Runtime API `/runtime/servers` |
| **AgentAddr** | Agent check address | Runtime API `/runtime/servers` |
| **AgentSend** | Agent check send string | Runtime API `/runtime/servers` |
| **HealthCheckPort** | Health check port | Runtime API `/runtime/servers` |

#### Frontend modifications

| Field | Description | API Endpoint |
|-------|-------------|--------------|
| **`Maxconn`** | Maximum connections | Runtime API `/runtime/frontends` |

#### Map and certificate content

Content updates to an **existing, already-referenced** map or certificate are applied to the live worker via the Runtime API — no reload:

| Change | Mechanism | HAProxy version |
|--------|-----------|-----------------|
| **Map file content** — entries added, changed, or removed in a map already loaded by the running config (for example host/path routing maps, a weight or body-size policy map) | per-entry `set map` / `add map` / `del map` delta | v3.0+ |
| **TLS certificate content** — a renewed certificate that keeps the same filename (for example a cert-manager rotation) | `set ssl cert` + `commit ssl cert` | v3.2+ |

#### Gateway API per-route logic

Most of what an HTTPRoute or GRPCRoute rule configures is a map entry rather than a configuration line, so changing one is a map content update — the row above:

| Route feature | Where the value lives | Reloads when |
|---------------|-----------------------|--------------|
| `RequestHeaderModifier`, `ResponseHeaderModifier` (rule- and backendRef-level) | `gw-reqhdr.map`, `gw-reshdr.map` | the first route in the cluster names a given header |
| `RequestRedirect` | `gw-redirect.map` | a rule matching several path prefixes with `ReplacePrefixMatch` |
| `URLRewrite` | `gw-urlrewrite.map` | a rule matching several path prefixes with `ReplacePrefixMatch` |
| `spec.rules[].timeouts` (Gateway Enhancement Proposal 1742) | `gw-timeout.map` | never |
| `RequestMirror` | `gw-mirror.map`, `gw-mirror-pct.map` | a rule's mirrors sample at different percentages |
| Host and path routing, including `RegularExpression` paths | `host.map`, `path-*.map`, `route-winner.map`, `route-backend.map` | never |

What stays structural, because HAProxy can't source it at request time: advanced matchers (method, header, query parameter, gRPC method), the `CORS` filter, `RegularExpression` path *rewrites*, and the backend section a new route's Service needs.

The new content is also written to disk (so a later, unrelated reload re-reads it), but the reload-free property comes from the Runtime API call, not the disk write. Caveats:

- The map or certificate must already exist **and be referenced by the running config** so HAProxy has it loaded. **Creating or deleting** a map/cert file, or changing one the config doesn't reference, takes the reload path. This is why the bundled libraries register their maps unconditionally, empty ones included.
- A map read with `map_reg`, `map_sub`, `map_dom`, `map_dir` or `map_end` is evaluated as a first-match-wins list, so an entry has to land in its intended position: a change beyond a pure append is applied as one whole-file runtime replace rather than a per-entry delta — still no reload, just a larger runtime payload. Declare `ordered: false` on a map read with `map_str`, `map_beg`, `map_ip` or `map_str_int` (see [`maps`](./crd-reference.md#maps)) and every change stays a per-entry delta.
- Other auxiliary files — general/error files, CA files, crt-lists — always reload when their content changes.
- If a runtime apply fails for any reason, the controller falls back to a single reload, so the result always converges.

### Reload-required operations

The following changes **require a HAProxy reload**:

#### Server operations

| Operation | Reason |
|-----------|--------|
| **Creating servers** | New server requires configuration reload |
| **Deleting servers** | Removing server requires configuration reload |
| **Modifying non-runtime fields** | Fields like `check`, `inter`, `rise`, `fall`, `ssl`, `verify`, etc. aren't supported by Runtime API |

Examples of server attributes that **require reload** when modified:

- Health check settings (`check`, `inter`, `rise`, `fall`, `fastinter`, `downinter`)
- SSL/TLS settings (`ssl`, `verify`, `ca-file`, `crt`, `sni`)
- Connection settings (`maxconn`, `maxqueue`, `minconn`)
- Advanced options (`send-proxy`, `send-proxy-v2`, `cookie`, `track`)

#### Structural and logic changes

| Category | Components | Reason |
|----------|------------|--------|
| **Structural Changes** | Frontends, Backends, Binds | Configuration structure changed |
| **Routing Logic** | ACLs, HTTP Rules, TCP Rules | Request processing logic changed |
| **Advanced Features** | Filters, Captures, Stick Rules | Feature configuration changed |
| **Section Changes** | All main sections | Section-level modifications |
| **Health Checks** | HTTP Checks, TCP Checks | Health check logic changed |
| **Frontend Attributes** | Most frontend settings except `Maxconn` | Not supported by Runtime API |
| **Auxiliary Files** | Creating/deleting any map or certificate; general/error files, CA files, crt-lists | Only content updates to an existing, referenced map (v3.0+) or certificate (v3.2+) are reload-free |

### What a change costs

The controller compares two renders and gives each pod one of three verdicts:

| Verdict | What changed | What the pod does |
|---------|--------------|-------------------|
| `runtime` | Map entries; certificate, CA or crt-list content; a server's address, port, weight or admin state; a server added to or removed from a backend; on HAProxy 3.4, a backend the render declared dynamic | The agent writes the files and runs the commands on the worker's stats socket. The process keeps serving |
| `file_only` | A file nothing running reads yet — a general file no section references, a map the worker never loaded | The agent writes it. The next reload picks it up |
| `reload` | A section's text; a named defaults profile appearing or disappearing; a file declared reload-on-change; configuration text no section accounts for | The agent writes the whole set and reloads through the master socket |

A server keyword HAProxy has no runtime setter for — `ssl-min-ver`, `no-check`
and the rest of the list in `pkg/dataplane/deployplan/keywords.go` — makes that
server's change structural. That's why the bundled libraries put `check` on
`default-server` rather than on every server line: a keyword on the line takes
the whole backend off the reload-free lane.

Every reason a change didn't stay reload-free is on the pod's status, so an
operator can see which part of a render cost them a reload.

**Reference:** the rules are table tested per rule in `pkg/dataplane/deployplan`, and the playground runs the same code to answer "does this change reload?" before you apply it.

## Not supported

### Listen sections

**Listen sections** are deployable — HAProxy parses them — but the bundled libraries don't emit them, and a change to one always reloads.

**Background:** HAProxy's `listen` directive combines frontend and backend behavior into a single section, so a route change inside it changes that section. Splitting it keeps the backend half on the reload-free lane.

**Workaround:** Any Listen section can be decomposed into:

- One Frontend section (handles client connections)
- One Backend section (handles server connections)

Since both Frontend and Backend are fully supported, this provides equivalent behavior.

## HAProxy Enterprise sections

The following sections are available only when connected to HAProxy Enterprise; they include the Web Application Firewall (WAF) and the Keepalived Virtual Router Redundancy Protocol (VRRP) sections. The controller automatically detects the Enterprise edition and enables support for these features.

| Section | Description | API Version |
|---------|-------------|-------------|
| **WAF Profiles** | Web Application Firewall profile definitions | Enterprise 3.2+ |
| **WAF Body Rules** | WAF request body inspection rules | Enterprise 3.0+ |
| **WAF Rulesets** | ModSecurity ruleset file references | Enterprise 3.0+ |
| **WAF Global** | Global WAF configuration settings | Enterprise 3.2+ |
| **Bot Management Profiles** | Bot detection and mitigation profiles | Enterprise 3.0+ |
| **CAPTCHAs** | CAPTCHA challenge configurations | Enterprise 3.0+ |
| **UDP Load Balancers** | UDP protocol load balancer sections | Enterprise 3.0+ |
| **Keepalived VRRP** | VRRP instances for high availability | Enterprise 3.0+ |
| **Keepalived Sync Groups** | VRRP synchronization groups | Enterprise 3.0+ |
| **Dynamic Updates** | Runtime configuration update rules | Enterprise 3.0+ |
| **Advanced Logging** | Extended log inputs and outputs | Enterprise 3.0+ |
| **Git Integration** | Configuration version control settings | Enterprise 3.0+ |

### UDP load balancer child components

UDP Load Balancers support child components similar to TCP frontends/backends:

| Component | Description | API Version |
|-----------|-------------|-------------|
| **Binds** | UDP listen addresses and ports | Enterprise 3.0+ |
| **Servers** | Backend UDP server definitions | Enterprise 3.0+ |
| **ACLs** | Access control lists | Enterprise 3.2+ |
| **Server Switching Rules** | Dynamic server selection | Enterprise 3.2+ |

### Keepalived child components

Keepalived sections support fine-grained management of VRRP configuration:

| Component | Description |
|-----------|-------------|
| **Track Interfaces** | Network interfaces to monitor |
| **Track Scripts** | Health check scripts for failover |
| **Virtual IP Addresses** | VIPs managed by VRRP instance |

!!! note
    Enterprise features require an HAProxy Enterprise licence and its binary. Deployed to a Community pod, the pod's own binary rejects the configuration at reload and the apply comes back with HAProxy's message.

## Implementation details

### Comparison strategies

The comparator uses two comparison strategies:

1. **Fine-Grained Child Resource Management** (frequently changing resources)
    - Frontends and backends expose per-child operations (binds, ACLs, rules, servers, health checks, …)
    - Each child resource is diffed as an individual Create/Update/Delete operation
    - These per-child operations drive change classification and the `X-Runtime-Actions` for runtime-eligible server updates; the config itself ships as one raw push
    - **Benefit:** Lets the controller skip the HAProxy reload whenever no structural change is present

2. **Whole-Section Replacement** (infrequently changing resources)
    - Sections without exposed child operations: Rings, HTTPErrors, Userlists, LogForwards, FCGIApps, CrtStores
    - Uses `.Equal()` method to compare the entire section including nested components
    - If any attribute changes, the entire section is replaced
    - **Benefit:** Simpler code, fewer operations for resources that rarely change

    Resolvers, Mailers, and Peers sit between the two strategies — their *child entries*
    (Nameservers, MailerEntries, PeerEntries) use fine-grained Create/Update/Delete
    operations like Frontend/Backend children, while the parent section's own
    attributes are compared with an "Equal-without-children" helper.

### Operation ordering

Operations are automatically ordered by:

1. **Priority** (lower numbers first)
2. **Type** (Delete → Create → Update)
3. **Dependencies** (parent sections before child components)

This keeps the generated operation stream internally consistent — a Backend's create precedes its Servers' creates, and Server deletes precede the Backend's delete. Because production applies the whole config in a single raw push (see Comparison strategies above), this ordering doesn't sequence the live apply; it keeps the diff correct for change classification and for any consumer that applies the operations in order.

The comparator uses the `haproxytech/client-native` models' built-in `.Equal()` methods for comprehensive attribute comparison, ensuring zero-maintenance compatibility with future HAProxy features.
