# Kubernetes versions

HAPTIC requires Kubernetes 1.33 or newer. The chart uses native sidecars to keep
the agent available while HAProxy drains connections during pod termination.
Helm rejects clusters below the chart's `kubeVersion` requirement.

## Compatibility checks

CI tests the minimum release separately from the newer Kubernetes version:

| Kubernetes | HAProxy | Checks |
|------------|---------|--------|
| 1.33.0 | 3.4 | Full e2e suite, upgrades from retained released charts, installation without Gateway API CRDs, and chart schema validation |
| 1.37.0 | 3.0 and 3.4 on merge requests; 3.1–3.3 also on main and nightly runs | Full e2e suite |
| 1.37.0 | 3.4 | Released-chart upgrades, installation without Gateway API CRDs, and chart schema validation |

The e2e suite covers routing, admission rejection, EndpointSlice updates during
backend replacement, and HAProxy pod termination with requests in flight.
The upgrade suite checks successful upgrades, rejection without configuration
mutation, and recovery from a broken installation.

The minimum jobs inherit the corresponding suite's triggers and failure rules.
They use a [digest-pinned Kubernetes 1.33.0 node image](https://github.com/kubernetes-sigs/kind/releases/tag/v0.27.0).
Intermediate Kubernetes minor versions don't have dedicated CI lanes.

## Select a Kubernetes version for local tests

`KIND_NODE_IMAGE` selects the image when a test suite creates its Kind cluster.
The e2e suite can reuse a cluster, so remove only the test cluster you own before
changing its version. Upgrade and bare-cluster installation tests create their
own fresh clusters.

```bash
KIND_NODE_IMAGE=kindest/node:v1.33.0@sha256:02f73d6ae3f11ad5d543f16736a2cb2a63a300ad60e81dac22099b0b04784a4e \
  make test-e2e
```

```bash
KIND_NODE_IMAGE=kindest/node:v1.33.0@sha256:02f73d6ae3f11ad5d543f16736a2cb2a63a300ad60e81dac22099b0b04784a4e \
  make test-chart-upgrade
```

For chart schema validation without a cluster:

```bash
make lint-chart-ci KUBE_VERSION=1.33.0
```
