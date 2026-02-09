## Context

The controller currently supports HAProxy 3.0, 3.1, and 3.2. The DataPlane API client uses a dispatcher pattern with generated clients per version (`v30`, `v31`, `v32` + enterprise variants). Each version has its own OpenAPI spec, oapi-codegen config, and generated Go client. The capabilities matrix in `buildCapabilities()` uses minor version thresholds to enable features. Template libraries set `balance roundrobin` per-backend rather than in the `defaults` section.

HAProxy 3.3 ships with a DataPlane API that has the same endpoints as 3.2 (all 219 paths identical) but introduces schema-level changes to existing models.

## Goals / Non-Goals

**Goals:**

- HAProxy 3.3 is a supported, tested version (CI matrix, integration tests, validation tests)
- The DataPlane API v3.3 client handles schema differences (required fields, model restructuring)
- The `balance roundrobin` directive is consolidated in `defaults` to avoid silent behavior change on 3.3
- The `extract-dataplane-spec.sh` script works for 3.3 and future versions
- 3.2 remains the default (LTS)

**Non-Goals:**

- Exposing new HAProxy 3.3 features (kTLS, QUIC backend, persistent stats) through controller configuration — these can be added later
- Dropping HAProxy 3.0 support
- Changing the default version to 3.3
- Adding enterprise 3.3 support (wait for HAProxy Enterprise 3.3 release)

## Decisions

### Decision 1: v33 client via code generation, same as v30/v31/v32

**Rationale**: The v3.3 API has schema changes (required `name` on `bind`/`defaults_base`, `name` moved from `bind_params` to `bind`, FCGI renames). These affect generated Go types. The existing pattern of one generated client per version handles this cleanly — the dispatcher routes to the correct types at runtime.

**Alternative considered**: Reuse v32 client since endpoints are identical. Rejected because the schema changes mean `v32.Bind` lacks the `Name` field that `v33.Bind` has as required, and JSON unmarshaling would silently produce incorrect structs.

**Approach**:

1. Extract spec: `./scripts/extract-dataplane-spec.sh 3.3 pkg/generated/dataplaneapi/v33/spec.json`
2. Create `hack/oapi-codegen-v33.yaml` (copy of v32 config, change package to `v33`, output to `v33/api.gen.go`)
3. Add `generate-dataplaneapi-v33` Makefile target
4. Run generation

### Decision 2: Extend dispatcher with V33/V33EE fields

**Rationale**: The `CallFunc[T]` struct and `Clientset` follow a pattern where each version gets explicit fields. Adding `V33`/`V33EE` is consistent and type-safe.

**Changes to `clientset.go`**:

- Add `v33Client *v33.Client` and `v33eeClient *v33ee.Client` fields
- Create v33 clients in `NewClientset`
- Add `V33()` and `V33EE()` accessors
- Add `case 3:` to `PreferredClient()` switch

**Changes to `dispatcher.go`**:

- Add `V33` and `V33EE` fields to `CallFunc[T]`
- Add `V33EE` field to `EnterpriseCallFunc[T]`
- Add `case *v33.Client:` and `case *v33ee.Client:` to all dispatch switches
- Add `case 3:` to `DispatchEnterpriseOnly` and `DispatchEnterpriseOnlyGeneric`

**Changes to section operations** (comparator/sections/): Every `Dispatch` call site currently has V30/V31/V32 entries. Each needs a V33 entry. The JSON marshal/unmarshal pattern means this is mechanical: copy the V32 block, change the type import to v33.

### Decision 3: Capabilities — 3.3 inherits all 3.2 capabilities, no new ones

**Rationale**: The API diff shows no new endpoints. New HAProxy 3.3 features (kTLS, QUIC backend) are HAProxy-level config directives, not DataPlane API endpoints. The capabilities matrix tracks API features, not HAProxy features.

**Change to `buildCapabilities()`**: The existing `minor >= 2` checks already cover 3.3 (since 3 >= 2). No new capability flags needed. The `CapabilitiesFromVersion()` function in `capabilities.go` similarly uses `>= 2` thresholds and will work for 3.3 without changes.

### Decision 4: Balance directive in defaults section

**Rationale**: HAProxy 3.3 changed the default balance algorithm from `roundrobin` to `random`. While all current backends explicitly set `balance roundrobin`, this is redundant and violates DRY. Moving it to `defaults` provides a single point of control and protects against future backends that might omit it.

**Changes**:

- `base.yaml` defaults section: Add `balance roundrobin` after `option forwardfor`
- `ingress.yaml`: Remove `balance roundrobin` from backend snippet
- `gateway.yaml`: Remove `balance roundrobin` from both HTTPRoute backend and SSL passthrough backend snippets
- `haproxytech.yaml`: Keep annotation-driven `balance {{ lb }}` (overrides defaults when set)
- `haproxy-ingress.yaml`: Keep annotation-driven `balance {{ lb }}` (overrides defaults when set)
- SSL passthrough backends in `haproxytech.yaml` and `haproxy-ingress.yaml`: Remove hardcoded `balance roundrobin`

Annotation-driven balance overrides work because HAProxy's per-backend `balance` directive takes precedence over `defaults`. When no annotation is set, the `defaults` value applies.

### Decision 5: Fix extract-dataplane-spec.sh binary path

**Rationale**: The script hardcodes `/usr/bin/dataplaneapi` on line 152 for community images, but the binary is at `/usr/local/bin/dataplaneapi` (verified for both 3.2 and 3.3 Alpine images). The script was working for 3.2 only because the image's entrypoint.sh happened to find the binary via PATH. For 3.3 the entrypoint changed and the hardcoded path breaks.

**Fix**: Change `dataplaneapi_bin="/usr/bin/dataplaneapi"` to `dataplaneapi_bin="/usr/local/bin/dataplaneapi"` on line 152.

### Decision 6: No enterprise 3.3 mapping yet

**Rationale**: HAProxy Enterprise releases lag behind community. There's no `3.3r1` image available yet. We'll add the enterprise 3.3 mapping and `v33ee` spec extraction when the enterprise image ships.

**For now**: Add a placeholder comment in `versions.env` noting that enterprise 3.3 is pending. Skip `v33ee` client generation. The `start-dev-env.sh` case for 3.3 enterprise can be added when available.

## Risks / Trade-offs

**[Risk] Balance directive removal breaks custom backends that relied on per-backend setting** → Mitigation: The `defaults` section applies to all backends that don't explicitly set balance. Annotation-driven overrides still work. Users with custom template snippets that set `balance` on specific backends are unaffected (their explicit setting takes precedence). This is strictly a DRY improvement, not a behavior change.

**[Risk] v33 dispatcher entries increase boilerplate across ~26 methods and ~30+ section operations** → Mitigation: This is the established pattern. The JSON marshal/unmarshal approach means each v33 entry is a mechanical copy of the v32 entry with type names changed. Could be partially automated with a script, but the one-time cost is manageable.

**[Risk] CI matrix expansion from 3 to 4 versions increases pipeline duration** → Mitigation: Jobs run in parallel. The main impact is on shared runners' capacity, not wall-clock time. Acceptable for supporting a current HAProxy release.

**[Risk] Enterprise 3.3 not available yet — users requesting enterprise 3.3 will get errors** → Mitigation: The controller already validates that the detected API version is supported. Clear error message when v3.3 enterprise is detected but no v33ee client exists. Document this gap.
