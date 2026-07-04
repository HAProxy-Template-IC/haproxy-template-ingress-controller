# High Availability

## Overview

The controller supports running multiple replicas with **leader election** to ensure only one replica deploys configurations to HAProxy while all replicas remain ready for immediate failover. This page covers the Helm configuration for HA deployments.

For detailed architecture, troubleshooting, and migration procedures, see the [High Availability Operations Guide](https://haproxy-haptic.org/controller/latest/operations/high-availability/) in the controller documentation.

## Leader Election (Default)

**Default configuration (2 replicas with leader election):**

```yaml
replicaCount: 2  # Runs 2 replicas by default

controller:
  config:
    controller:
      leaderElection:
        enabled: true  # Enabled by default
        leaseName: ""  # Defaults to Helm release fullname
        leaseDuration: 30s
        renewDeadline: 20s
        retryPeriod: 5s
```

**How it works:**

- All replicas watch resources, render templates, and validate configs
- Only the elected leader deploys configurations to HAProxy instances
- Automatic failover if leader fails (within leaseDuration, default 30s)
- Leadership transitions are logged and tracked via Prometheus metrics

During a failover, the standby replica acquires the lease and resumes deploying within the `leaseDuration` window (default ~30s). HAProxy continues serving traffic throughout — no config pushes happen during this window, but the load balancers are unaffected.

The `leaseDuration` window only applies when the leader dies without releasing the lease (node crash, OOM-kill). Voluntary handoffs — rolling updates, graceful shutdown — release the lease immediately, so a standby takes over within one `retryPeriod`. The defaults are 2x the client-go convention (15s/10s/2s) so the leader rides out multi-second API-server or CPU starvation stalls without losing the lease; tune them down if you prefer faster crash-failover over starvation headroom.

**Why 2 replicas by default?** Two replicas provide failover without requiring a quorum majority. Either replica can take over immediately, which is sufficient for an ingress controller that serves as a stateless configuration manager.

**Check current leader:**

The Lease resource is named after the Helm release (e.g. `my-controller` for `helm install my-controller ...`), not a fixed `haptic-leader`. Override by setting `controller.config.controller.leaderElection.leaseName`.

```bash
# List leases in the release namespace
kubectl get lease -n haptic

# View Lease resource (replace <release> with your Helm release name)
kubectl get lease -n haptic <release> -o yaml

# Check metrics via port-forward
kubectl port-forward -n haptic deployment/<release>-controller 9090:9090
curl http://localhost:9090/metrics | grep haptic_leader_election_is_leader
```

## Multiple Replicas

Run 3+ replicas for enhanced availability:

```yaml
replicaCount: 3

podDisruptionBudget:
  enabled: true
  minAvailable: 2

# Distribute across availability zones
controller:
  podSpec:
    affinity:
      podAntiAffinity:
        preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchLabels:
                  app.kubernetes.io/name: haptic
              topologyKey: topology.kubernetes.io/zone
```

## Single Replica (Development)

Disable leader election for development/testing:

```yaml
replicaCount: 1

controller:
  config:
    controller:
      leaderElection:
        enabled: false
```

## Autoscaling

```yaml
autoscaling:
  enabled: true
  minReplicas: 2  # Keep at least 2 for HA
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

Because the controller is leader-elected, autoscaling the controller deployment adds webhook-serving and warm-cache capacity (and faster failover) — **not** render/validate/deploy throughput. The reconciliation pipeline always runs on the single elected leader regardless of replica count, so CPU-based scaling driven by the leader's reconcile load adds standbys rather than parallel workers. To scale HAProxy data-plane throughput, scale HAProxy instead (`haproxy.keda` autoscaling or more HAProxy replicas).
