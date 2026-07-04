# status-patch-libraries Specification

## Purpose

Chart-side template libraries that declare Kubernetes status updates as template-registered patches: the base library exposes a `status-patches-*` extension point, and the resource libraries register `statusPatch()` entries with `rendered`, `deployed`, and `deployFailed` variants for Ingress, Gateway, HTTPRoute, and GRPCRoute resources, which the controller applies at the matching pipeline phase. A controller-service watch discovers LoadBalancer addresses and shares them via `gf["addresses"]`, so published status (Ingress loadBalancer entries, Gateway addresses and conditions, route parent conditions) reflects real deployment state without any resource-specific controller code.

## Requirements

### Requirement: Status Patches Extension Point

The base library SHALL define a `status-patches-*` extension point rendered at priority 200 in the haproxyConfig template, after `features-*` (050-150) and before `backends-*` (500). The render_glob call `render_glob "status-patches-*"` SHALL be placed in the haproxyConfig template. Status patch snippets SHALL produce no visible output in the HAProxy configuration (they only register side-effects via `statusPatch()`). The extension point SHALL be rendered as a Scriggo comment or suppressed output block so that status patch calls do not inject whitespace into the config.

#### Scenario: Status patches render after features

- **WHEN** both `features-100-ingress-tls` and `status-patches-200-ingress` snippets exist
- **THEN** `features-100-ingress-tls` SHALL execute before `status-patches-200-ingress`

#### Scenario: Status patches render before backends

- **WHEN** both `status-patches-200-ingress` and `backends-500-ingress` snippets exist
- **THEN** `status-patches-200-ingress` SHALL execute before `backends-500-ingress`

#### Scenario: Status patch snippets produce no config output

- **WHEN** `status-patches-200-ingress` executes and registers status patches
- **THEN** the rendered HAProxy configuration SHALL not contain any text output from the status patch snippet

### Requirement: Address Discovery via Controller Service Watch

The library SHALL add a namespace-scoped `watchedResources` entry named `controller_services` that watches `v1/services` in the controller's namespace (injected via Helm as `{{ .Release.Namespace }}`), filtered by the label selector `app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller`. A `features-*` priority snippet SHALL extract the first entry from `.status.loadBalancer.ingress` of the discovered service and store it in `gf["addresses"]` as a slice of address objects (each with `ip` or `hostname` key). If no service is found or the service has no LoadBalancer status, `gf["addresses"]` SHALL remain nil and no address-related status SHALL be emitted.

#### Scenario: LoadBalancer IP discovered

- **WHEN** the controller service has `.status.loadBalancer.ingress[0].ip = "192.0.2.1"`
- **THEN** `gf["addresses"]` SHALL contain `[{"ip": "192.0.2.1"}]`

#### Scenario: LoadBalancer hostname discovered

- **WHEN** the controller service has `.status.loadBalancer.ingress[0].hostname = "lb.example.com"`
- **THEN** `gf["addresses"]` SHALL contain `[{"hostname": "lb.example.com"}]`

#### Scenario: Multiple addresses discovered

- **WHEN** the controller service has multiple `.status.loadBalancer.ingress` entries (e.g., dual-stack IPv4 + IPv6)
- **THEN** `gf["addresses"]` SHALL contain all entries

#### Scenario: No LoadBalancer address available

- **WHEN** the controller service has no `.status.loadBalancer.ingress` entries (pending provisioning)
- **THEN** `gf["addresses"]` SHALL remain nil and no address-related status patches SHALL be emitted

### Requirement: Ingress Status Patch Snippet

The ingress library SHALL provide a `status-patches-200-ingress` snippet that registers status patches for all Ingress resources. The snippet SHALL use the sharded parallel rendering pattern (`ShardedX` macro with `shard_slice()` and `go`) matching existing ingress library patterns. For each Ingress, the snippet SHALL register a `statusPatch()` with `deployed` and `deployFailed` variants. The `deployed` variant SHALL set `status.loadBalancer.ingress` to `gf["addresses"]`. The `deployFailed` variant SHALL set `status.loadBalancer.ingress` to an empty slice. If `gf["addresses"]` is nil, no status patch SHALL be registered for Ingress resources.

#### Scenario: Ingress status patch with discovered address

- **WHEN** `gf["addresses"]` contains `[{"ip": "10.0.0.1"}]` and an Ingress `default/my-app` exists
- **THEN** a status patch SHALL be registered for `default/my-app` with `deployed` variant containing `{"loadBalancer": {"ingress": [{"ip": "10.0.0.1"}]}}`

#### Scenario: No address skips Ingress status patches

- **WHEN** `gf["addresses"]` is nil
- **THEN** no status patches SHALL be registered for any Ingress resources

#### Scenario: Parallel rendering for large Ingress sets

- **WHEN** more than 100 Ingress resources exist
- **THEN** the status patch snippet SHALL use `shard_slice()` with `go` goroutines matching `CalculateShardCount` logic

### Requirement: Gateway Status Patch Snippet

The gateway library SHALL provide a `status-patches-200-gateway` snippet that registers status patches for all Gateway resources. The snippet SHALL use the sharded parallel rendering pattern. For each Gateway, the snippet SHALL register a `statusPatch()` with `rendered`, `deployed`, and `deployFailed` variants. The `rendered` variant SHALL set conditions `Accepted: True` and `ResolvedRefs: True/False` (based on listener certificate resolution from `features-*` analysis) and `Programmed: Unknown` with reason `Pending`. The `deployed` variant SHALL set `Accepted: True`, `ResolvedRefs: True/False`, and `Programmed: True`, plus `status.addresses` from `gf["addresses"]` converted to Gateway address format (`[{"type": "IPAddress", "value": "..."}]` or `[{"type": "Hostname", "value": "..."}]`). The `deployFailed` variant SHALL set `Programmed: False` with reason `Invalid`. All conditions SHALL include `observedGeneration` from the Gateway's `metadata.generation` and `lastTransitionTime` via the `transitionTime()` helper.

#### Scenario: Gateway status with all conditions on deploy success

- **WHEN** Gateway `default/my-gw` at generation 5 is processed and deployment succeeds
- **THEN** the `deployed` variant SHALL contain conditions `Accepted: True`, `ResolvedRefs: True`, `Programmed: True` all with `observedGeneration: 5`

#### Scenario: Gateway addresses in status

- **WHEN** `gf["addresses"]` contains `[{"ip": "10.0.0.1"}]` and deployment succeeds
- **THEN** the `deployed` variant SHALL contain `addresses: [{"type": "IPAddress", "value": "10.0.0.1"}]`

#### Scenario: Gateway deploy failure sets Programmed False

- **WHEN** deployment fails for Gateway `default/my-gw`
- **THEN** the `deployFailed` variant SHALL contain `Programmed: False` with reason `Invalid`

### Requirement: HTTPRoute Status Patch Snippet

The gateway library SHALL provide status patches for HTTPRoute resources as part of the `status-patches-200-gateway` snippet. The snippet SHALL use the cached gateway analysis from `util-analyze-routes` (via `ComputeIfAbsent`) to avoid re-analyzing routes. For each HTTPRoute, for each `spec.parentRefs` entry, the snippet SHALL register a `statusPatch()` with `rendered`, `deployed`, and `deployFailed` variants. The status SHALL be structured as `status.parents[]` entries with `parentRef`, `controllerName` (from `gf["controllerName"]` or Helm-injected value), and conditions. The `rendered` variant SHALL set `Accepted: True` and `ResolvedRefs: True/False` (based on whether backend services exist in stores) and `Programmed: Unknown`. The `deployed` variant SHALL set `Accepted: True`, `ResolvedRefs: True/False`, and `Programmed: True`. All conditions SHALL include `observedGeneration` and `lastTransitionTime` via `transitionTime()`. The snippet SHALL use sharded parallel rendering.

#### Scenario: HTTPRoute status with parent ref

- **WHEN** HTTPRoute `default/my-route` at generation 3 has parentRef to Gateway `default/my-gw` and deployment succeeds
- **THEN** the `deployed` variant SHALL contain `parents: [{parentRef: {group: "gateway.networking.k8s.io", kind: "Gateway", namespace: "default", name: "my-gw"}, controllerName: "<controller-name>", conditions: [Accepted: True, ResolvedRefs: True, Programmed: True]}]` with `observedGeneration: 3`

#### Scenario: HTTPRoute with unresolved backend ref

- **WHEN** HTTPRoute `default/my-route` references a backend service `default/missing-svc` that does not exist in stores
- **THEN** the `rendered` variant SHALL contain `ResolvedRefs: False` with reason `BackendNotFound`

#### Scenario: HTTPRoute removed from Gateway

- **WHEN** HTTPRoute `default/my-route` previously had parentRef to Gateway A but now only references Gateway B
- **THEN** the status patch SHALL only contain a `parents[]` entry for Gateway B, and SSA SHALL remove the Gateway A entry via field ownership cleanup

### Requirement: GRPCRoute Status Patch Snippet

The gateway library SHALL provide status patches for GRPCRoute resources following the same pattern as HTTPRoute. GRPCRoute status SHALL use the same `status.parents[]` structure with `parentRef`, `controllerName`, and conditions (`Accepted`, `ResolvedRefs`, `Programmed`). The snippet SHALL reuse the cached gateway analysis.

#### Scenario: GRPCRoute status mirrors HTTPRoute pattern

- **WHEN** GRPCRoute `default/my-grpc-route` has parentRef to Gateway `default/my-gw` and deployment succeeds
- **THEN** the `deployed` variant SHALL contain the same condition structure as HTTPRoute with `Accepted: True`, `ResolvedRefs: True/False`, `Programmed: True`
