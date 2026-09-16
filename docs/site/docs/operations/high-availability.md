# High availability with leader election

Run multiple controller replicas so configuration delivery can recover after a leader failure.

## Overview

The controller supports running multiple replicas for high availability using leader election based on Kubernetes Leases. Only the elected leader deploys; all replicas keep their Kubernetes-resource caches and their incremental render graph warm and serve admission webhook requests to reduce the work needed after election.

**Benefits of HA deployment:**

- Standby replicas remain available during controller rolling updates
- Automatic election after a leader failure; voluntary handoffs release the lease without waiting for expiry
- Warm resource caches and render graphs on standby replicas
- Replicas spread across nodes and zones via anti-affinity (see [Anti-Affinity](#anti-affinity))

**How it works:**

1. All replicas watch Kubernetes resources, run the admission webhook, discover HAProxy pods, and render every change to keep their incremental render graph warm. Only the elected leader deploys. Admission and new HTTP inputs are validated on the replica handling them. See [Leader Election](../development/design/leader-election.md) for the full all-replica vs leader-only component split.
2. Leader election determines which replica drives the pipeline and applies configs to the fleet.
3. When the leader fails, followers automatically elect a new one. Cached state from all-replica components (validated config, discovered HAProxy pods) is replayed on `BecameLeaderEvent` so the new leader starts with current state; the reconciler also fires immediately so the new leader produces a fresh render, from the graph it kept warm as a follower.
4. Leadership transitions are logged and tracked via Prometheus metrics.

## Configuration

### Enable leader election

Leader election is **enabled by default** in the Helm chart:

```yaml
# values.yaml (chart defaults)
controller:
  replicaCount: 2  # Run 2 replicas for HA
  config:
    controller:
      leaderElection:
        enabled: true
        leaseName: ""         # Defaults to the Helm release fullname
        leaseDuration: 30s    # Lease-expiry delay before takeover attempts
        renewDeadline: 20s    # Leader retries renewal for this long
        retryPeriod: 5s       # Interval between renewal attempts
```

!!! note
    The defaults allow brief API-server and CPU stalls without surrendering leadership. Shorter lease and renewal periods can reduce failover time but make the controller more sensitive to those stalls.

### Disable leader election

For development or single-replica deployments:

```yaml
# values.yaml
controller:
  replicaCount: 1
  config:
    controller:
      leaderElection:
        enabled: false
```

### Timing Parameters

The timing parameters control failover speed and tolerance:

| Parameter | Chart default | Purpose | Recommendations |
|-----------|---------------|---------|-----------------|
| `leaseDuration` | `30s` | Lease-expiry delay before takeover attempts | Increase for flaky networks (`60s`+) |
| `renewDeadline` | `20s` | How long leader retries before giving up | Must be < `leaseDuration` |
| `retryPeriod` | `5s` | Interval between leader renewal attempts | Should be < `renewDeadline` |

After a crash, a follower waits `leaseDuration` from its last observation of a
lease renewal before attempting election. Retry jitter and API latency add to
that delay; `leaseDuration + retryPeriod` isn't a hard upper bound. A voluntary
handoff releases the lease without waiting for expiry. HAProxy keeps serving its
current configuration while a new leader is elected.

**Clock-rate tolerance:**

Client-go tolerates differences in clock readings, but not arbitrary differences
in clock speed. Its approximate clock-rate tolerance is
`leaseDuration / renewDeadline`: `30s / 20s = 1.5` with these defaults. Increasing
both durations proportionally leaves that ratio unchanged. See the
[client-go leader-election documentation](https://pkg.go.dev/k8s.io/client-go/tools/leaderelection).

## Deployment

<a id="standard-ha-deployment"></a>

### Standard high-availability Deployment

Deploy with 2-3 replicas (default Helm configuration):

```bash
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 --namespace haptic --create-namespace \
  --set controller.replicaCount=2
```

### Scaling

Scale the deployment dynamically:

```bash
# Scale to 3 replicas
kubectl scale deployment haptic-controller -n haptic --replicas=3

# Scale back to 2
kubectl scale deployment haptic-controller -n haptic --replicas=2
```

### Autoscaling

The chart ships an optional HorizontalPodAutoscaler:

```yaml
controller:
  autoscaling:
    enabled: true
    minReplicas: 2      # keep at least 2 for failover
    maxReplicas: 10
    targetCPUUtilizationPercentage: 80
```

Adding controller replicas increases admission capacity and maintains more warm
standbys. Each replica renders, but only the leader deploys; replicas don't divide
one rendering workload between them. To increase traffic capacity, scale HAProxy
with `haproxy.keda` or `haproxy.replicaCount`.

### RBAC requirements

The controller requires these additional permissions for leader election:

```yaml
apiGroups: ["coordination.k8s.io"]
resources: ["leases"]
verbs: ["get", "create", "update"]
```

The Helm chart grants these permissions through a Role in the release namespace.

## Monitoring leadership

### Check current leader

The Lease resource is named after the chart `fullname` — `haptic` for `helm install haptic …`, but `<release>-haptic` when the release name doesn't contain `haptic`. Override by setting `controller.config.controller.leaderElection.leaseName`.

```bash
# List leases in the release namespace
kubectl get lease -n haptic

# View the lease for the haptic release
kubectl get lease -n haptic haptic -o yaml

# Output shows current leader:
# spec:
#   holderIdentity: haptic-controller-7d9f8b4c6d-abc12
```

### View leadership status in logs

```bash
# Leader logs show:
kubectl logs -n haptic deployment/haptic-controller | grep -E "leader|election"

# Example output:
# level=INFO msg="Leader election started" identity=pod-abc12 lease=<release>
# level=INFO msg="Became leader: pod-abc12" identity=pod-abc12
```

### Prometheus metrics

Monitor leader election via metrics endpoint:

```bash
kubectl port-forward -n haptic deployment/haptic-controller 9090:9090
```

In another terminal:

```bash
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

### No leader elected

**Symptoms:**

- No deployments happening
- All replicas show `is_leader=0`
- Logs show constant election failures

**Common causes:**

1. **Missing RBAC permissions:**

    The controller's ServiceAccount name is the Helm release `fullname` (unless you overrode `controller.serviceAccount.name`):

    ```bash
    SA=$(kubectl get deployment haptic-controller -n haptic -o jsonpath='{.spec.template.spec.serviceAccountName}')
    kubectl auth can-i get leases -n haptic --as=system:serviceaccount:haptic:$SA
    kubectl auth can-i create leases -n haptic --as=system:serviceaccount:haptic:$SA
    kubectl auth can-i update leases -n haptic --as=system:serviceaccount:haptic:$SA
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

### Multiple leaders (split-brain)

**Symptoms:**

- `sum(haptic_leader_election_is_leader) > 1`
- Multiple pods deploying configs simultaneously
- Conflicting deployments in HAProxy

First restrict the metric query to one HAPTIC release; separate releases each have a leader. If multiple replicas of the same release report leadership, check that they use the same lease name and namespace, then inspect clock stability and API connectivity:

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

### Frequent leadership changes

**Symptoms:**

- `increase(haptic_leader_election_transitions_total[1h]) > 5`
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

<a id="leader-not-deploying"></a>

### Leader isn't deploying

**Symptoms:**

- One replica shows `is_leader=1`
- No deployment errors in logs
- HAProxy configs not updating

**Diagnosis:**

```bash
# Check leader logs for deployment activity
kubectl logs -n haptic <leader-pod> | grep -i "deploy"

# Verify leader-only components started (requires debug log level)
kubectl logs -n haptic <leader-pod> | grep -i "deployer starting\|deployment scheduler starting\|starting deployment components"
```

**Common causes:**

- Deployment components failed to start (check logs for errors)
- Deployment pacing delaying structural changes (check `minDeploymentInterval`)
- HAProxy instances unreachable (check network connectivity)

## Best practices

### Replica count

**Development:**

- 1 replica with `leaderElection.enabled: false`

**Staging:**

- 2 replicas with leader election enabled

**Production:**

- 2-3 replicas across multiple availability zones
- A `PodDisruptionBudget` (`minAvailable: 1`) is created automatically once `controller.replicaCount > 1` — no action needed. Tune or disable it via:

    ```yaml
    controller:
      podDisruptionBudget:
        enabled: true
        minAvailable: 1
    ```

### Resource allocation

The leader does the heavy lifting (render + validate + deploy + status writes), but every replica still has the full Kubernetes-resource cache loaded in memory. CPU usage is markedly higher on the leader; memory is similar across leader and followers. Size them all the same so a freshly elected follower handles peak load without resizing — the chart defaults already do this:

```yaml
# chart default — sized for the typical 50–200 Ingress range
controller:
  resources:
    requests:
      cpu: 100m
      memory: 1Gi      # memory request = limit (pod stays Burstable — no CPU limit)
    limits:
      memory: 1Gi      # CPU limit deliberately omitted to avoid GOMAXPROCS throttling
```

For larger or smaller workloads see the sizing table in [Performance — Controller Resource Sizing](./performance.md#controller-resource-sizing). Measure startup and steady-state memory for your watched resources and template libraries before lowering the limit.

### Anti-affinity

Distribute replicas across nodes for better availability:

```yaml
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
              topologyKey: kubernetes.io/hostname
```

### Monitoring and alerts

The leader-election alerts (no leader, split-brain, frequent transitions) are part of the recommended alert set in [Monitoring — Alerting Rules](./monitoring.md#alerting-rules). Of those three, the chart's built-in `PrometheusRule` ships only the no-leader alert (`HAProxyControllerNoLeader`, toggled by `controller.monitoring.prometheusRule.defaultRules.leaderElectionLost`) — enable it with `controller.monitoring.prometheusRule.enabled`. Copy the split-brain and transition-rate rules from the recommended set if you want them.

## Migration from single-replica

To migrate an existing single-replica deployment to HA:

1. **Verify RBAC permissions** (Helm chart updates this automatically)

2. **Update values.yaml:**

    ```yaml
    controller:
      replicaCount: 2
      config:
        controller:
          leaderElection:
            enabled: true
    ```

3. **Upgrade with Helm:**

    ```bash
    helm upgrade haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
      -n haptic --reuse-values \
      -f values.yaml
    ```

4. **Verify leadership:**

    ```bash
    kubectl logs -f -n haptic deployment/haptic-controller | grep leader
    ```

5. **Confirm one leader:**

    ```bash
    # Query each pod's metrics; exactly one should report is_leader 1
    for pod in $(kubectl get pods -n haptic -l app.kubernetes.io/name=haptic,app.kubernetes.io/component=controller -o name); do
      echo "$pod:"
      kubectl exec -n haptic $pod -- wget -qO- localhost:9090/metrics | grep is_leader
    done
    ```

## See also

- [Leader Election Design](../development/design/leader-election.md) - Architecture and implementation details
- [Monitoring Guide](./monitoring.md) - Prometheus metrics and alerting
- [Debugging Guide](./debugging.md) - Runtime introspection and troubleshooting
- [Security Guide](./security.md) - RBAC and security best practices
- [Performance Guide](./performance.md) - Resource sizing and optimization
- [Troubleshooting Guide](../troubleshooting.md) - General troubleshooting
