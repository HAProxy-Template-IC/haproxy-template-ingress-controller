# Supported HAProxy Configuration

This document provides an overview of HAProxy configuration sections and child components supported by the haproxy-template-ic via the HAProxy Dataplane API.

## Overview

The controller supports all configuration sections that can be managed through the [HAProxy Dataplane API](https://www.haproxy.com/documentation/haproxy-data-plane-api/). Configuration changes are applied using fine-grained operations to minimize HAProxy reloads and maximize use of the Runtime API for zero-downtime updates.

**Coverage:**
- **15 main configuration sections** (global, defaults, frontends, backends, etc.)
- **23 child component types** across frontends and backends (servers, ACLs, rules, etc.)
- **Complete Dataplane API compatibility** - All manageable resources are supported

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
| **Programs** | External program configurations | 10 | Create/Update/Delete |
| **LogForwards** | Syslog forwarding sections | 10 | Create/Update/Delete |
| **FCGIApps** | FastCGI application configs | 10 | Create/Update/Delete |
| **CrtStores** | Certificate store sections | 10 | Create/Update/Delete |

**Note:** Lower priority numbers are processed first. Operations are automatically ordered by dependency and priority.

## Child Components by Section

### Frontend Child Components

Frontends support **9 child component types** with individual Create/Update/Delete operations:

| Component | Description | Code Reference |
|-----------|-------------|----------------|
| **Binds** | Listen addresses and ports | `comparator.go:1191` |
| **ACLs** | Access control lists | `comparator.go:744` |
| **HTTP Request Rules** | HTTP request processing rules | `comparator.go:808` |
| **HTTP Response Rules** | HTTP response processing rules | `comparator.go:857` |
| **TCP Request Rules** | TCP request processing rules | `comparator.go:906` |
| **Backend Switching Rules** | Dynamic backend selection rules | `comparator.go:1117` |
| **Filters** | Data filters (compression, trace, etc.) | `comparator.go:1261` |
| **Captures** | Request/response capture declarations | `comparator.go:1384` |
| **Log Targets** | Logging destinations | `comparator.go:992` |

### Backend Child Components

Backends support **14 child component types** with individual Create/Update/Delete operations:

| Component | Description | Code Reference |
|-----------|-------------|----------------|
| **Servers** | Backend server definitions | `comparator.go:338` |
| **Server Templates** | Dynamic server templates | `comparator.go:1420` |
| **ACLs** | Access control lists | `comparator.go:744` |
| **HTTP Request Rules** | HTTP request processing rules | `comparator.go:808` |
| **HTTP Response Rules** | HTTP response processing rules | `comparator.go:857` |
| **HTTP After Response Rules** | Post-response processing rules | `comparator.go:1080` |
| **TCP Request Rules** | TCP request processing rules | `comparator.go:906` |
| **TCP Response Rules** | TCP response processing rules | `comparator.go:955` |
| **Server Switching Rules** | Dynamic server selection rules | `comparator.go:1154` |
| **Stick Rules** | Session persistence rules | `comparator.go:1042` |
| **Filters** | Data filters | `comparator.go:1261` |
| **HTTP Checks** | HTTP health check configurations | `comparator.go:1310` |
| **TCP Checks** | TCP health check configurations | `comparator.go:1347` |
| **Log Targets** | Logging destinations | `comparator.go:992` |

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
- **Programs**: All program attributes
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

**Note:** Map files and auxiliary files are updated via the Storage API, which also avoids reloads when only file contents change.

### Reload-Required Operations

The following changes **require an HAProxy reload**:

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

### Optimization Strategy

The controller uses fine-grained comparison to detect changes at the attribute level:

1. **Server Weight/Address/Port Changes**: Applied via Runtime API (no reload)
2. **Server Creation/Deletion**: Triggers reload (required by HAProxy)
3. **Server Health Check Changes**: Triggers reload (not supported by Runtime API)
4. **Other Changes**: Evaluated individually; structural changes trigger reloads

**Important:** The Dataplane API automatically determines whether a change can use the Runtime API. If any modified field is not runtime-supported, a reload is triggered. The controller delegates this decision to the Dataplane API's `changeThroughRuntimeAPI` function.

**Reference:** See [HAProxy Dataplane API runtime.go](https://github.com/haproxytech/dataplaneapi/blob/master/handlers/runtime.go) for the complete Runtime API field support logic.

## Not Supported

### Listen Sections

**Listen sections** are NOT supported because the HAProxy Dataplane API does not expose them as manageable resources.

**Background:** HAProxy's `listen` directive combines frontend and backend functionality into a single section. However, the Dataplane API enforces separation of concerns by requiring distinct `frontend` and `backend` sections.

**Workaround:** Any Listen section can be decomposed into:
- One Frontend section (handles client connections)
- One Backend section (handles server connections)

Since both Frontend and Backend are fully supported, this provides equivalent functionality.

## Implementation Details

### Comparison Strategies

The implementation uses two approaches for optimal performance:

1. **Fine-Grained Child Resource Management** (frequently-changing resources)
   - Frontends: 9 child resource types
   - Backends: 14 child resource types
   - Each child resource has individual Create/Update/Delete operations
   - Changes to individual ACLs, rules, or servers are applied independently
   - **Benefit:** Minimizes API calls and reduces reload frequency

2. **Whole-Section Replacement** (infrequently-changing resources)
   - Other sections (Resolvers, Mailers, Peers, etc.)
   - Uses `.Equal()` method to compare entire section including nested components
   - If any attribute changes, the entire section is replaced
   - **Benefit:** Simpler code, fewer operations for resources that rarely change

### Operation Ordering

Operations are automatically ordered by:
1. **Priority** (lower numbers first)
2. **Type** (Delete → Create → Update)
3. **Dependencies** (parent sections before child components)

This ensures that, for example, a Backend is created before its Servers, and Servers are deleted before the Backend is removed.

### Code References

All comparison logic is implemented in:
- **Main Comparator:** `pkg/dataplane/comparator/comparator.go`
- **Operation Definitions:** `pkg/dataplane/comparator/sections/*.go`

The comparator uses the `haproxytech/client-native` models' built-in `.Equal()` methods for comprehensive attribute comparison, ensuring zero-maintenance compatibility with future HAProxy features.

## Summary

The haproxy-template-ic provides complete HAProxy Dataplane API coverage:

- ✅ **15 main sections** fully supported
- ✅ **23 child component types** with fine-grained operations
- ✅ **Runtime API optimization** for zero-reload server updates
- ✅ **Dependency-aware operation ordering** for safe deployments
- ✅ **Future-proof comparison** using HAProxy models' `.Equal()` methods
- ❌ **Listen sections** not supported (Dataplane API limitation)

For implementation details, see:
- Architecture: `docs/design.md`
- Parser: `pkg/dataplane/parser/`
- Comparator: `pkg/dataplane/comparator/`
- Operations: `pkg/dataplane/comparator/sections/`
