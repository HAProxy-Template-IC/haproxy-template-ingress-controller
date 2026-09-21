# ADR-0025: Mutual TLS for agent transport

## Status

Accepted. Extends [ADR-0022](0022-haptic-agent.md)'s agent protocol.

## Context

Agent deployment requests contain rendered configuration and auxiliary files,
including private keys. A shared Basic-auth password over the pod network doesn't
encrypt them. Password updates also reach the controller before agents that read
credentials from their startup environment, leaving a mixed fleet temporarily
unable to accept configuration updates.

## Decision

Use TLS 1.3 with separate controller and agent identities. The chart enables
mutual TLS by default and supports managed or external identity Secrets. The
expected DNS SAN differs by role, so an agent certificate cannot authorize a
controller operation even when an issuer grants both TLS extended key usages.

The generic `transportsecurity` utility reads a complete immutable directory
revision. Kubernetes Secret mounts supply that revision through `..data`.
Invalid material fails the next operation; it never restores stale trust or
falls back to HTTP. TLS mode doesn't accept Basic-auth credentials.

CA rotation preserves peer trust during staggered updates. Previous trust has an
explicit deadline, limited to 24 hours. Servers revalidate client trust on every
control request, including reused connections. Clients replace connection pools
when their identity or active trust changes, and at least every 30 seconds to
bound peer-certificate expiry on idle connections. Existing requests remain
bounded by their operation deadlines.

Kubernetes liveness probes use the local Unix socket independently of certificate
validity. Network health endpoints may connect without a client certificate.
State and mutation APIs require the controller identity. A separate local Unix
socket exposes only state and process health,
so pod diagnostics don't require distributing the controller's private key.

## Consequences

An independent bootstrap Job and hourly CronJob renew the default CA and both
identities before expiry. The issuer Secret persists a whole generation before
either identity is published; retries finish that generation. A cross-signed new
CA allows old peers to verify new identities during the one-hour trust overlap.
The issuer private key is never mounted in either peer. Kubernetes API access
allows recovery even when agent certificates have expired.

Certificate state is generated at runtime, so offline rendering stays stable.
Cert-manager is an explicit alternative. Its managed root Certificate retains
the CA key during renewal, leaving two leaf lifetimes to distribute its renewed
certificate before the old root expires. External issuers own CA lifecycle.
Kubernetes' pod certificate projection still needs a signer and isn't available
across the supported Kubernetes versions.

Moving a legacy HTTP fleet to TLS changes its management protocol during rollout; existing
HAProxy workers retain their last valid configuration while agents are replaced.
Certificate rotation within a TLS fleet requires no pod restart.

TLS protects transport independently of routing resources. No Kubernetes routing
kind or template-resource schema enters the Go utility.
