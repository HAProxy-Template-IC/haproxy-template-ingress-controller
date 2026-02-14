# High Availability with Leader Election

This guide explains how to deploy and operate the HAProxy Template Ingress Controller in high availability (HA) mode with multiple replicas.

## Overview

The controller supports running multiple replicas for high availability using leader election based on Kubernetes Leases. Only the elected leader performs write operations (deploying configurations to HAProxy), while all replicas continue watching resources, rendering templates, and validating configurations to maintain "hot standby" status.

**Benefits of HA deployment:**

- Zero-downtime during controller upgrades (rolling updates)
- Automatic failover if leader pod crashes (~15-20 seconds)
- All replicas ready to take over immediately (hot standby)
- Balanced leader distribution across nodes

**How it works:**

1. All replicas watch Kubernetes resources and render HAProxy configurations
2. Leader election determines which replica can deploy configs to HAProxy
3. When leader fails, followers automatically elect a new leader
4. Leadership transitions are logged and tracked via Prometheus metrics

## Configuration

### Enable Leader Election

Leader election is **enabled by default** when deploying with 2+ replicas via Helm:

```yaml
# values.yaml (defaults)
replicaCount: 2  # Run 2 replicas for HA

controller:
  config:
    controller:
      leader_election:
        enabled: true
        lease_name: haptic-leader
        lease_duration: 60s    # Failover happens within this time
        renew_deadline: 15s    # Leader tries to renew for this long
        retry_period: 5s       # Interval between renewal attempts
```

### Disable Leader Election

For development or single-replica deployments:

```yaml
# values.yaml
replicaCount: 1

controller:
  config:
    controller:
      leader_election:
        enabled: false  # Disabled in single-replica mode
```

### Timing Parameters

The timing parameters control failover speed and tolerance:

| Parameter | Default | Purpose | Recommendations |
|-----------|---------|---------|-----------------|
| `lease_duration` | 60s | Max time followers wait before taking over | Increase for flaky networks (120s) |
| `renew_deadline` | 15s | How long leader retries before giving up | Should be < `lease_duration` (1/4 ratio) |
| `retry_period` | 5s | Interval between leader renewal attempts | Should be < `renew_deadline` (1/3 ratio) |

**Failover time calculation:**

```
Worst-case failover = lease_duration + renew_deadline
Default failover    = 60s + 15s = 75s (but typically 15-20s)
```

When the leader fails, followers must wait for the lease to expire before they can acquire it. During this window, HAProxy continues serving traffic with its last known configuration -- no traffic is dropped, but new resource changes are not processed until a new leader is elected. The typical failover (15-20s) is faster than worst-case because the leader usually fails mid-lease rather than right after renewal.

**Clock skew tolerance:**

```
Skew tolerance = lease_duration - renew_deadline
Default        = 60s - 15s = 45s (handles up to 4x clock differences)
```

If clock skew exceeds this tolerance, brief split-brain may occur where two replicas both believe they are leader. NTP-synchronized nodes typically have sub-second skew, well within the default tolerance.

## Deployment

### Standard HA Deployment

Deploy with 2-3 replicas (default Helm configuration):

```bash
helm install haproxy-ic charts/haptic \
  --set replicaCount=2
```

### Scaling

Scale the deployment dynamically:

```bash
# Scale to 3 replicas
kubectl scale deployment haptic-controller --replicas=3

# Scale back to 2
kubectl scale deployment haptic-controller --replicas=2
```

### RBAC Requirements

The controller requires these additional permissions for leader election:

```yaml
apiGroups: ["coordination.k8s.io"]
resources: ["leases"]
verbs: ["get", "create", "update"]
```

These are automatically configured in the Helm chart's ClusterRole.

## Monitoring Leadership

### Check Current Leader

```bash
# View Lease resource
kubectl get lease -n <namespace> haptic-leader -o yaml

# Output shows current leader:
# spec:
#   holderIdentity: haptic-7d9f8b4c6d-abc12
```

### View Leadership Status in Logs

```bash
# Leader logs show:
kubectl logs -n <namespace> deployment/haptic-controller | grep -E "leader|election"

# Example output:
# level=INFO msg="Leader election started" identity=pod-abc12 lease=haptic-leader
# level=INFO msg="Became leader: pod-abc12" identity=pod-abc12
```

### Prometheus Metrics

Monitor leader election via metrics endpoint:

```bash
kubectl port-forward -n <namespace> deployment/haptic-controller 9090:9090
curl http://localhost:9090/metrics | grep leader_election
```

**Key metrics:**

```promql
# Current leader (should be 1 across all replicas)
sum(haptic_leader_election_is_leader)

# Identify which pod is leader
haptic_leader_election_is_leader{pod=~".*"} == 1

# Leadership transition rate (should be low)
rate(haptic_leader_election_transitions_total[1h])
```

## Troubleshooting

Check these areas in order of likelihood:

1. **RBAC permissions** (most common) -- service account missing lease permissions
2. **Environment variables** -- `POD_NAME` / `POD_NAMESPACE` not injected
3. **API server connectivity** -- network policies or firewall blocking access
4. **Clock skew** -- NTP not configured or excessive drift between nodes

### No Leader Elected

**Symptoms:**

- No deployments happening
- All replicas show `is_leader=0`
- Logs show constant election failures

**Common causes:**

1. **Missing RBAC permissions:**

   ```bash
   kubectl auth can-i get leases --as=system:serviceaccount:<namespace>:haptic
   kubectl auth can-i create leases --as=system:serviceaccount:<namespace>:haptic
   kubectl auth can-i update leases --as=system:serviceaccount:<namespace>:haptic
   ```

2. **Missing environment variables:**

   ```bash
   kubectl get pod <pod-name> -o yaml | grep -A2 "POD_NAME\|POD_NAMESPACE"

   # Should show:
   # - name: POD_NAME
   #   valueFrom:
   #     fieldRef:
   #       fieldPath: metadata.name
   ```

3. **API server connectivity:**

   ```bash
   kubectl logs <pod-name> | grep "connection refused\|timeout"
   ```

### Multiple Leaders (Split-Brain)

**Symptoms:**

- `sum(haptic_leader_election_is_leader) > 1`
- Multiple pods deploying configs simultaneously
- Conflicting deployments in HAProxy

**This should never happen** with proper Kubernetes Lease implementation. If it does:

1. Check for severe clock skew between nodes:

   ```bash
   # On each node
   timedatectl status
   ```

2. Verify Kubernetes API server health:

   ```bash
   kubectl get --raw /healthz
   ```

3. Restart all controller pods:

   ```bash
   kubectl rollout restart deployment haptic-controller
   ```

### Frequent Leadership Changes

**Symptoms:**

- `rate(haptic_leader_election_transitions_total[1h]) > 5`
- Logs show frequent "Lost leadership" / "Became leader" messages
- Deployments failing intermittently

**Common causes:**

1. **Resource contention** - Leader pod can't renew lease in time:

   ```bash
   kubectl top pods -n <namespace>
   kubectl describe pod <leader-pod> | grep -A10 "Limits\|Requests"
   ```

   **Solution:** Increase CPU/memory limits

2. **Network issues** - API server communication delays:

   ```bash
   kubectl logs <pod-name> | grep "lease renew\|deadline"
   ```

   **Solution:** Increase `lease_duration` and `renew_deadline`

3. **Node issues** - Leader pod node experiencing problems:

   ```bash
   kubectl describe node <node-name>
   ```

   **Solution:** Drain and investigate node

### Leader Not Deploying

**Symptoms:**

- One replica shows `is_leader=1`
- No deployment errors in logs
- HAProxy configs not updating

**Diagnosis:**

```bash
# Check leader logs for deployment activity
kubectl logs <leader-pod> | grep -i "deploy"

# Verify leader-only components started
kubectl logs <leader-pod> | grep "Started.*Deployer\|DeploymentScheduler"
```

**Common causes:**

- Deployment components failed to start (check logs for errors)
- Rate limiting preventing deployment (check drift prevention interval)
- HAProxy instances unreachable (check network connectivity)

## Best Practices

### Replica Count

**Development:**

- 1 replica with `leader_election.enabled: false`

**Staging:**

- 2 replicas with leader election enabled

**Production:**

- 2-3 replicas across multiple availability zones
- Enable PodDisruptionBudget:

  ```yaml
  podDisruptionBudget:
    enabled: true
    minAvailable: 1
  ```

### Resource Allocation

Allocate sufficient resources for hot standby:

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m      # Allow bursts during leader work
    memory: 512Mi
```

All replicas perform the same work (watching, rendering, validating), so resource usage is similar.

### Anti-Affinity

Distribute replicas across nodes for better availability:

```yaml
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: haptic
          topologyKey: kubernetes.io/hostname
```

### Monitoring and Alerts

Set up Prometheus alerts for leader election health:

```yaml
groups:
  - name: haproxy-ic-leader-election
    rules:
      # No leader
      - alert: NoLeaderElected
        expr: sum(haptic_leader_election_is_leader) < 1
        for: 1m
        annotations:
          summary: "No HAProxy controller leader elected"

      # Multiple leaders (split-brain)
      - alert: MultipleLeaders
        expr: sum(haptic_leader_election_is_leader) > 1
        annotations:
          summary: "Multiple HAProxy controller leaders detected (split-brain)"

      # Frequent transitions
      - alert: FrequentLeadershipChanges
        expr: rate(haptic_leader_election_transitions_total[1h]) > 5
        for: 15m
        annotations:
          summary: "HAProxy controller experiencing frequent leadership changes"
```

## Migration from Single-Replica

To migrate an existing single-replica deployment to HA:

1. **Verify RBAC permissions** (Helm chart updates this automatically)

2. **Update values.yaml:**

   ```yaml
   replicaCount: 2
   controller:
     config:
       controller:
         leader_election:
           enabled: true
   ```

3. **Upgrade with Helm:**

   ```bash
   helm upgrade haproxy-ic charts/haptic \
     --reuse-values \
     -f new-values.yaml
   ```

4. **Verify leadership:**

   ```bash
   kubectl logs -f deployment/haptic-controller | grep leader
   ```

5. **Confirm one leader:**

   ```bash
   kubectl get pods -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller \
     -o custom-columns=NAME:.metadata.name,LEADER:.status.podIP

   # Check metrics to identify leader
   for pod in $(kubectl get pods -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller -o name); do
     echo "$pod:"
     kubectl exec $pod -- wget -qO- localhost:9090/metrics | grep is_leader
   done
   ```

## See Also

- [Helm Chart HA Configuration](https://haproxy-haptic.org/helm-chart/operations/high-availability/) - HA configuration via Helm values
- [Leader Election Design](../development/design/leader-election.md) - Architecture and implementation details
- [Monitoring Guide](./monitoring.md) - Prometheus metrics and alerting
- [Debugging Guide](./debugging.md) - Runtime introspection and troubleshooting
- [Security Guide](./security.md) - RBAC and security best practices
- [Performance Guide](./performance.md) - Resource sizing and optimization
- [Troubleshooting Guide](../troubleshooting.md) - General troubleshooting
