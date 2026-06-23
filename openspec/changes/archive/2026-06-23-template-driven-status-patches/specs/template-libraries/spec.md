# Template Libraries

## MODIFIED Requirements

### Requirement: Extension Point Pattern

The base library SHALL define extension points as render_glob calls with specific patterns. Each extension point SHALL render all matching template snippets in alphabetical order. Extension points SHALL include: `features-*` (feature registration), `status-patches-*` (resource status patch registration), `global-top-*` (global sections), `frontend-matchers-advanced-*` (advanced route matching), `frontend-filters-*` (request/response filters), `frontends-*` (additional frontends), `backends-*` (backend definitions), `backend-directives-*` (backend configuration), `map-host-*` (host mappings), `map-path-exact-*` (exact path mappings), `map-path-prefix-exact-*` (prefix-exact mappings), `map-path-prefix-*` (prefix mappings), `map-path-regex-*` (regex mappings), and `map-weighted-backend-*` (weighted routing).

#### Scenario: Snippets from multiple libraries rendered in alphabetical order

- **WHEN** backends-500-ingress (from ingress.yaml) and backends-500-gateway (from gateway.yaml) both exist
- **THEN** render_glob "backends-*" SHALL render backends-500-gateway before backends-500-ingress.

#### Scenario: Extension point renders nothing when no snippets match

- **WHEN** no library defines a snippet matching "global-top-*"
- **THEN** render_glob "global-top-*" SHALL produce empty output.

#### Scenario: Status patches extension point renders at priority 200

- **WHEN** `status-patches-200-ingress` and `backends-500-ingress` both exist
- **THEN** render_glob "status-patches-*" SHALL execute before render_glob "backends-*"
