## Why

HAProxy 3.3 was released in November 2025 and is available as `haproxytech/haproxy-debian:3.3` (latest patch: 3.3.2). Users who want access to 3.3 features (kTLS, QUIC on backend, persistent stats over reloads, ACME DNS-01) cannot use them with the controller today. Adding 3.3 as a supported version keeps the project current while retaining 3.2 (LTS, supported until 2030) as the safe default.

## What Changes

- Add HAProxy 3.3 to `HAPROXY_VERSIONS` in `versions.env` and enterprise mapping
- Generate and integrate a v33 DataPlane API client (schema changes: `bind.name` and `defaults_base.name` now required, `name` field moved from `bind_params` to `bind`/`default_bind`, FCGI schema renames)
- Add 3.3 to the capabilities matrix in `pkg/dataplane/capabilities.go`
- Add v33 dispatcher routing in `pkg/dataplane/client/`
- **BREAKING** (HAProxy 3.3 behavior): Default `balance` algorithm changed from `roundrobin` to `random`. Move `balance roundrobin` into the HAProxy `defaults` section (base.yaml) and remove redundant per-backend hardcoded `balance roundrobin`. Annotation-driven balance overrides (`haproxy.org/load-balance`, `haproxy-ingress.github.io/balance-algorithm`) remain functional.
- Fix `extract-dataplane-spec.sh` binary path (`/usr/bin/dataplaneapi` → `/usr/local/bin/dataplaneapi`)
- Add 3.3 enterprise case to `start-dev-env.sh`
- Document that 3.2 remains the default (LTS) and 3.3 is a short-term release (EOL ~Q1 2027)

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `haproxy-version-management`: Add 3.3 to the set of supported versions, enterprise mapping, and document LTS vs short-term release distinction
- `dataplane-sync`: Add v33 DataPlane API client to handle schema changes (required `name` fields, `bind_params` restructuring, FCGI renames)
- `template-libraries`: Set `balance roundrobin` in the `defaults` section of base.yaml; remove redundant per-backend balance directives while keeping annotation-driven overrides

## Impact

- **CI/CD**: Matrix expands from 3 to 4 versions (build, integration test, validation test, route test per version)
- **Generated code**: New `pkg/generated/dataplaneapi/v33/` directory with generated client from OpenAPI spec
- **DataPlane client**: New v33 dispatcher entry and oapi-codegen config
- **Helm chart libraries**: base.yaml, ingress.yaml, gateway.yaml modified (balance directive consolidation)
- **Scripts**: `extract-dataplane-spec.sh` bug fix, `start-dev-env.sh` 3.3 case added
- **Documentation**: values.yaml comments, CHANGELOG entries for both controller and chart
