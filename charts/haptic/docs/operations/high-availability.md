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
        leaseDuration: 15s
        renewDeadline: 10s
        retryPeriod: 2s
```

**How it works:**

- All replicas watch resources, render templates, and validate configs
- Only the elected leader deploys configurations to HAProxy instances
- Automatic failover if leader fails (within leaseDuration, default 15s)
- Leadership transitions are logged and tracked via Prometheus metrics

**Check current leader:**

```bash
# View Lease resource
kubectl get lease haptic-leader -o yaml

# Check metrics
kubectl port-forward deployment/haptic-controller 9090:9090
curl http://localhost:9090/metrics | grep leader_election_is_leader
```

## Multiple Replicas

Run 3+ replicas for enhanced availability:

```yaml
replicaCount: 3

podDisruptionBudget:
  enabled: true
  minAvailable: 2

# Distribute across availability zones
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
      leader_election:
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
