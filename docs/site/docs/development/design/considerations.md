# Considerations

## What the Controller Does

HAPTIC is an ingress controller for a fleet of HAProxy load balancers. It continuously watches a user-defined set of Kubernetes resource types and uses them as input to render:

- The main HAProxy configuration file (`haproxy.cfg`)
- Optional auxiliary files — custom error pages (e.g. `500.http`) and lookup map files (e.g. `host.map`)

After rendering, files are validated and pushed to each HAProxy pod via the [HAProxy Dataplane API](https://www.haproxy.com/documentation/haproxy-data-plane-api/). The controller pushes the full rendered configuration in a single raw update per pod; when a change touches only runtime-eligible server attributes (weight, address, port, enabled/disabled state) it is applied through the [HAProxy Runtime API](https://www.haproxy.com/documentation/haproxy-runtime-api/) instead, updating HAProxy at runtime without a process reload.

## Triggers

Two mechanisms trigger reconciliation:

- **Watched resource changes** — the primary trigger; debounced to coalesce bursts.
- **Drift prevention** — a periodic check (default 60s, set via `spec.dataplane.driftPreventionInterval`) that re-deploys if any rendered file differs from what was last pushed. This catches out-of-band changes to HAProxy and keeps desired and actual configuration eventually consistent.

## Constraints

- The Dataplane API does not cover every directive in the [HAProxy configuration language](https://www.haproxy.com/documentation/haproxy-configuration-manual/latest/). HAPTIC can only deploy configurations that the underlying [`haproxytech/client-native`](https://github.com/haproxytech/client-native) parser accepts. See [Supported Configuration](../../supported-configuration.md) for the current coverage.
- The controller assumes HAProxy runs alongside a Dataplane API instance reachable on the pod network (default port `5555`). Validation and deployment go through that API; there is no SSH or kubectl-exec path into HAProxy.

## System Environment

- The controller runs as a Kubernetes container.
- Each managed HAProxy instance must be a Kubernetes Pod with a Dataplane API sidecar sharing the HAProxy config volume.
- The controller's ServiceAccount needs `get`/`list`/`watch` on every resource type listed in `spec.watchedResources`, plus the standard set granted by the chart (Pods, Services, EndpointSlices, the CRDs, and `coordination.k8s.io/leases` for leader election). See [Security — RBAC](../../operations/security.md#rbac).
