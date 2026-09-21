# High availability

The chart starts two controller replicas and two HAProxy replicas by default.
Spread each pair across nodes so a node failure leaves a working controller and
proxy; see [anti-affinity](#anti-affinity). Each controller needs the full
[controller resource budget](performance.md#controller-resource-sizing).

## Overview

Leader election uses a Kubernetes Lease to choose the replica that deploys
configuration. Every replica watches resources, renders changes, and serves
admission requests. Followers keep their caches and render state ready for takeover.

If the leader fails, a follower acquires the lease and renders the current state
before deploying. A voluntary handoff releases the lease without waiting for it
to expire. HAProxy continues serving its existing configuration during election.
Spread replicas across nodes or zones to tolerate a node failure; see
[anti-affinity](#anti-affinity).

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

## Deployment

<a id="standard-ha-deployment"></a>

### Standard high-availability Deployment

A new installation uses two controller replicas:

```bash
helm install haptic oci://registry.gitlab.com/haproxy-haptic/haptic/charts/haptic \
  --version 0.2.0-alpha.3 --namespace haptic --create-namespace \
  --set controller.replicaCount=2
```

### Scaling

Set the replica count in your Helm values and apply them through your normal
deployment workflow:

```yaml
controller:
  replicaCount: 3
```

Keep at least two replicas for failover. Adding replicas increases the total
resource reservation; it doesn't reduce the work each replica performs.

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

Check these dependencies when leader election fails:

1. **RBAC permissions** -- service account missing lease permissions
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

    **Action:** Check CPU throttling and memory pressure. Increase CPU requests or memory requests and limits when the pod lacks resources; see [resource sizing](performance.md).

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

Every replica renders changes and holds resource and render caches. The leader
also deploys configuration and writes status. Give all replicas enough CPU and
memory to handle peak load after election:

Use the [resource sizing guide](./performance.md#controller-resource-sizing) for
starting requests and limits. Apply the same budget to every replica.

### Anti-affinity

This preference spreads controllers from the `haptic` release across nodes.
Change the instance label for another release. Apply an equivalent preference
under `haproxy.podSpec.affinity` with component `loadbalancer` for the proxy pods:

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
                  app.kubernetes.io/instance: haptic
                  app.kubernetes.io/component: controller
              topologyKey: kubernetes.io/hostname
```

### Monitoring and alerts

The bundled `PrometheusRule` includes `HAProxyControllerNoLeader`. Enable it
through the [monitoring setup](./monitoring.md#enable-the-bundled-monitoring).

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

4. **Confirm the controller rollout:**

    ```bash
    kubectl rollout status deployment/haptic-controller -n haptic
    ```

5. **Read the active leader from the Lease:**

    ```bash
    kubectl get lease haptic -n haptic -o jsonpath='{.spec.holderIdentity}{"\n"}'
    ```

## See also

- [Leader Election Design](../development/design/leader-election.md) - Architecture and implementation details
- [Monitoring Guide](./monitoring.md) - Prometheus metrics and alerting
- [Debugging Guide](./debugging.md) - Runtime introspection and troubleshooting
- [Security Guide](./security.md) - RBAC and security best practices
- [Performance Guide](./performance.md) - Resource sizing and optimization
- [Troubleshooting Guide](../troubleshooting.md) - General troubleshooting
