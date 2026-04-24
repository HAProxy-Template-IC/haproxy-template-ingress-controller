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
# values.yaml (chart defaults)
replicaCount: 2  # Run 2 replicas for HA

controller:
  config:
    controller:
      leaderElection:
        enabled: true
        leaseName: ""         # Defaults to the Helm release fullname
        leaseDuration: 15s    # Max time followers wait before taking over
        renewDeadline: 10s    # Leader retries renewal for this long
        retryPeriod: 2s       # Interval between renewal attempts
```

!!! note
    The chart defaults above are more aggressive than the bare CRD defaults (60s / 15s / 5s) to give faster failover in typical Kubernetes environments.

### Disable Leader Election

For development or single-replica deployments:

```yaml
# values.yaml
replicaCount: 1

controller:
  config:
    controller:
      leaderElection:
        enabled: false  # Disabled in single-replica mode
```

### Timing Parameters

The timing parameters control failover speed and tolerance:

| Parameter | Chart default | Purpose | Recommendations |
|-----------|---------------|---------|-----------------|
| `leaseDuration` | 15s | Max time followers wait before taking over | Increase for flaky networks (60s+) |
| `renewDeadline` | 10s | How long leader retries before giving up | Must be < `leaseDuration` |
| `retryPeriod` | 2s | Interval between leader renewal attempts | Should be < `renewDeadline` |

**Failover time calculation:**

```
Worst-case failover = leaseDuration + renewDeadline
Chart default       = 15s + 10s = 25s (typically faster)
```

When the leader fails, followers must wait for the lease to expire before they can acquire it. During this window, HAProxy continues serving traffic with its last known configuration — no traffic is dropped, but new resource changes are not processed until a new leader is elected.

**Clock skew tolerance:**

```
Skew tolerance = leaseDuration - renewDeadline
Chart default  = 15s - 10s = 5s
```

If clock skew exceeds this tolerance, brief split-brain may occur where two replicas both believe they are leader. NTP-synchronized nodes typically have sub-second skew, well within the default tolerance. In environments with looser time sync, raise `leaseDuration` (and `renewDeadline` proportionally).

## Deployment

### Standard HA Deployment

Deploy with 2-3 replicas (default Helm configuration):

```bash
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.1.0 --set replicaCount=2
```

### Scaling

Scale the deployment dynamically:

```bash
# Scale to 3 replicas
kubectl scale deployment haptic-controller -n haptic --replicas=3

# Scale back to 2
kubectl scale deployment haptic-controller -n haptic --replicas=2
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

The Lease resource is named after the Helm release (e.g. `haptic` for `helm install haptic ...`). Override by setting `controller.config.controller.leaderElection.leaseName`.

```bash
# List leases in the release namespace
kubectl get lease -n haptic

# View Lease resource (replace <release> with your Helm release name)
kubectl get lease -n haptic <release> -o yaml

# Output shows current leader:
# spec:
#   holderIdentity: <release>-controller-7d9f8b4c6d-abc12
```

### View Leadership Status in Logs

```bash
# Leader logs show:
kubectl logs -n haptic deployment/haptic-controller | grep -E "leader|election"

# Example output:
# level=INFO msg="Leader election started" identity=pod-abc12 lease=<release>
# level=INFO msg="Became leader: pod-abc12" identity=pod-abc12
```

### Prometheus Metrics

Monitor leader election via metrics endpoint:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 9090:9090
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
   kubectl rollout restart deployment haptic-controller -n haptic
   ```

### Frequent Leadership Changes

**Symptoms:**

- `rate(haptic_leader_election_transitions_total[1h]) > 5`
- Logs show frequent "Lost leadership" / "Became leader" messages
- Deployments failing intermittently

**Common causes:**

1. **Resource contention** - Leader pod can't renew lease in time:

   ```bash
   kubectl top pods -n haptic
   kubectl describe pod <leader-pod> | grep -A10 "Limits\|Requests"
   ```

   **Solution:** Increase CPU/memory limits

2. **Network issues** - API server communication delays:

   ```bash
   kubectl logs -n haptic <pod-name> | grep "lease renew\|deadline"
   ```

   **Solution:** Increase `leaseDuration` and `renewDeadline`

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
kubectl logs -n haptic <leader-pod> | grep -i "deploy"

# Verify leader-only components started
kubectl logs -n haptic <leader-pod> | grep "Started.*Deployer\|DeploymentScheduler"
```

**Common causes:**

- Deployment components failed to start (check logs for errors)
- Rate limiting preventing deployment (check drift prevention interval)
- HAProxy instances unreachable (check network connectivity)

## Best Practices

### Replica Count

**Development:**

- 1 replica with `leaderElection.enabled: false`

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
         leaderElection:
           enabled: true
   ```

3. **Upgrade with Helm:**

   ```bash
   helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
     -n haptic --reuse-values \
     -f new-values.yaml
   ```

4. **Verify leadership:**

   ```bash
   kubectl logs -f -n haptic deployment/haptic-controller | grep leader
   ```

5. **Confirm one leader:**

   ```bash
   kubectl get pods -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller \
     -o custom-columns=NAME:.metadata.name,LEADER:.status.podIP

   # Check metrics to identify leader
   for pod in $(kubectl get pods -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller -o name); do
     echo "$pod:"
     kubectl exec -n haptic $pod -- wget -qO- localhost:9090/metrics | grep is_leader
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
