# Supported HAProxy Configuration

This document provides an overview of HAProxy configuration sections and child components supported by HAPTIC via the HAProxy Dataplane API.

## Overview

The controller supports all configuration sections that can be managed through the [HAProxy Dataplane API](https://www.haproxy.com/documentation/haproxy-data-plane-api/). Configuration changes are applied by pushing the full rendered config to the Dataplane API in a single request; a fine-grained comparison classifies each change so the push can skip the HAProxy reload — and apply the change through the Runtime API for zero-downtime updates — whenever every change is a runtime-eligible server update.

**API Version Support:** The controller supports Dataplane API versions 3.0, 3.1, 3.2, and 3.3. The API version is auto-detected at runtime.

**HAProxy Editions:** Both HAProxy Community and HAProxy Enterprise are supported. Enterprise-only features are automatically detected and enabled when connected to an Enterprise instance.

**Coverage:** Every section the Dataplane API exposes as a manageable resource is supported — see the tables below for the current list. The comparator delegates attribute-level equality to `haproxytech/client-native` models' generated `.Equal()` methods, so new fields and sections added by HAProxy upstream become supported automatically without controller changes.

## Supported Configuration Sections

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
| **LogProfiles** | Log-format profiles (DataPlane API 3.1+) | 10 | Create/Update/Delete |
| **Traces** | Traces section (singleton, DataPlane API 3.1+) | 10 | Update only |
| **FCGIApps** | FastCGI application configs | 10 | Create/Update/Delete |
| **CrtStores** | Certificate store sections | 10 | Create/Update/Delete |
| **AcmeProviders** | ACME certificate provider configurations | 10 | Create/Update/Delete |

**Note:** Lower priority numbers are processed first. Operations are automatically ordered by dependency and priority.

## Child Components by Section

### Frontend Child Components

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
| **QUIC Initial Rules** | Per-frontend rules applied to QUIC initial packets (HTTP/3); requires Dataplane API v3.1+ |

### Backend Child Components

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

### Mailers, Resolvers, and Peers Child Components

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

### Other Section Components

The following sections use **whole-section comparison** via the models' `.Equal()` method, which includes all nested components:

- **Rings**: All ring attributes
- **HTTPErrors**: Includes errorfiles
- **Userlists**: Includes users and groups
- **LogForwards**: Includes log targets
- **FCGIApps**: Includes pass-header and set-param directives
- **CrtStores**: Includes crt-load entries

## Reload Behavior

The controller minimizes HAProxy reloads by leveraging the Runtime API when possible. However, only specific operations can avoid reloads.

### Zero-Reload Operations (Runtime API)

The following changes are applied **without reloading HAProxy**:

#### Server Modifications (Specific Fields Only)

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

#### Frontend Modifications

| Field | Description | API Endpoint |
|-------|-------------|--------------|
| **Maxconn** | Maximum connections | Runtime API `/runtime/frontends` |

#### Map and Certificate Content

Content updates to an **existing, already-referenced** map or certificate are applied to the live worker via the Runtime API — no reload:

| Change | Mechanism | HAProxy version |
|--------|-----------|-----------------|
| **Map file content** — entries added, changed, or removed in a map already loaded by the running config (e.g. host/path routing maps, a weight or body-size policy map) | per-entry `set map` / `add map` / `del map` delta | v3.0+ |
| **TLS certificate content** — a renewed certificate that keeps the same filename (e.g. a cert-manager rotation) | `set ssl cert` + `commit ssl cert` | v3.2+ |

The new content is also written to disk (so a later, unrelated reload re-reads it), but the reload-free property comes from the Runtime API call, not the disk write. Caveats:

- The map or certificate must already exist **and be referenced by the running config** so HAProxy has it loaded. **Creating or deleting** a map/cert file, or changing one the config doesn't reference, takes the reload path.
- Other auxiliary files — general/error files, CA files, crt-lists — always reload when their content changes.
- If a runtime apply fails for any reason, the controller falls back to a single reload, so the result always converges.

### Reload-Required Operations

The following changes **require a HAProxy reload**:

#### Server Operations

| Operation | Reason |
|-----------|--------|
| **Creating servers** | New server requires configuration reload |
| **Deleting servers** | Removing server requires configuration reload |
| **Modifying non-runtime fields** | Fields like `check`, `inter`, `rise`, `fall`, `ssl`, `verify`, etc. are not supported by Runtime API |

Examples of server attributes that **require reload** when modified:

- Health check settings (`check`, `inter`, `rise`, `fall`, `fastinter`, `downinter`)
- SSL/TLS settings (`ssl`, `verify`, `ca-file`, `crt`, `sni`)
- Connection settings (`maxconn`, `maxqueue`, `minconn`)
- Advanced options (`send-proxy`, `send-proxy-v2`, `cookie`, `track`)

#### Structural and Logic Changes

| Category | Components | Reason |
|----------|------------|--------|
| **Structural Changes** | Frontends, Backends, Binds | Configuration structure changed |
| **Routing Logic** | ACLs, HTTP Rules, TCP Rules | Request processing logic changed |
| **Advanced Features** | Filters, Captures, Stick Rules | Feature configuration changed |
| **Section Changes** | All main sections | Section-level modifications |
| **Health Checks** | HTTP Checks, TCP Checks | Health check logic changed |
| **Frontend Attributes** | Most frontend settings except Maxconn | Not supported by Runtime API |
| **Auxiliary Files** | Creating/deleting any map or certificate; general/error files, CA files, crt-lists | Only content updates to an existing, referenced map (v3.0+) or certificate (v3.2+) are reload-free |

### Optimization Strategy

The controller uses fine-grained comparison to detect changes at the attribute level:

1. **Server Weight/Address/Port Changes**: Applied via Runtime API (no reload)
2. **Server Creation/Deletion**: Triggers reload (required by HAProxy)
3. **Server Health Check Changes**: Triggers reload (not supported by Runtime API)
4. **Other Changes**: Evaluated individually; structural changes trigger reloads

**Important:** The controller makes this decision itself, using its own `serverRuntimeSupportedJSONFields` table (`pkg/dataplane/comparator/sections/factory_server.go`) that mirrors the Dataplane API's `RuntimeSupportedFields["server"]`. If any modified field is not runtime-supported, the config push carries `force_reload`; when every change is runtime-eligible it carries `skip_reload` plus the `X-Runtime-Actions` header so the live worker is updated without a reload.

**Reference:** See [HAProxy Dataplane API runtime.go](https://github.com/haproxytech/dataplaneapi/blob/master/handlers/runtime.go) for the complete Runtime API field support logic.

## Not Supported

### Listen Sections

**Listen sections** are NOT supported because the HAProxy Dataplane API does not expose them as manageable resources.

**Background:** HAProxy's `listen` directive combines frontend and backend functionality into a single section. However, the Dataplane API enforces separation of concerns by requiring distinct `frontend` and `backend` sections.

**Workaround:** Any Listen section can be decomposed into:

- One Frontend section (handles client connections)
- One Backend section (handles server connections)

Since both Frontend and Backend are fully supported, this provides equivalent functionality.

## HAProxy Enterprise Sections

The following sections are available only when connected to HAProxy Enterprise. The controller automatically detects the Enterprise edition and enables support for these features.

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

### UDP Load Balancer Child Components

UDP Load Balancers support child components similar to TCP frontends/backends:

| Component | Description | API Version |
|-----------|-------------|-------------|
| **Binds** | UDP listen addresses and ports | Enterprise 3.0+ |
| **Servers** | Backend UDP server definitions | Enterprise 3.0+ |
| **ACLs** | Access control lists | Enterprise 3.2+ |
| **Server Switching Rules** | Dynamic server selection | Enterprise 3.2+ |

### Keepalived Child Components

Keepalived sections support fine-grained management of VRRP configuration:

| Component | Description |
|-----------|-------------|
| **Track Interfaces** | Network interfaces to monitor |
| **Track Scripts** | Health check scripts for failover |
| **Virtual IP Addresses** | VIPs managed by VRRP instance |

!!! note
    Enterprise features require HAProxy Enterprise license and the Enterprise Dataplane API. When connected to HAProxy Community, these sections are ignored without error.

## Implementation Details

### Comparison Strategies

The implementation uses two approaches for optimal performance:

1. **Fine-Grained Child Resource Management** (frequently-changing resources)
   - Frontends and backends expose per-child operations (binds, ACLs, rules, servers, health checks, …)
   - Each child resource is diffed as an individual Create/Update/Delete operation
   - These per-child operations drive change classification and the `X-Runtime-Actions` for runtime-eligible server updates; the config itself ships as one raw push
   - **Benefit:** Lets the controller skip the HAProxy reload whenever no structural change is present

2. **Whole-Section Replacement** (infrequently-changing resources)
   - Sections without exposed child operations: Rings, HTTPErrors, Userlists, LogForwards, FCGIApps, CrtStores
   - Uses `.Equal()` method to compare the entire section including nested components
   - If any attribute changes, the entire section is replaced
   - **Benefit:** Simpler code, fewer operations for resources that rarely change

   Resolvers, Mailers, and Peers sit in between — their *child entries*
   (Nameservers, MailerEntries, PeerEntries) use fine-grained Create/Update/Delete
   operations like Frontend/Backend children, while the parent section's own
   attributes are compared with an "Equal-without-children" helper.

### Operation Ordering

Operations are automatically ordered by:

1. **Priority** (lower numbers first)
2. **Type** (Delete → Create → Update)
3. **Dependencies** (parent sections before child components)

This keeps the generated operation stream internally consistent — a Backend's create precedes its Servers' creates, and Server deletes precede the Backend's delete. Because production applies the whole config in a single raw push (see Comparison Strategies above), this ordering doesn't sequence the live apply; it keeps the diff correct for change classification and for any consumer that applies the operations in order.

The comparator uses the `haproxytech/client-native` models' built-in `.Equal()` methods for comprehensive attribute comparison, ensuring zero-maintenance compatibility with future HAProxy features.
