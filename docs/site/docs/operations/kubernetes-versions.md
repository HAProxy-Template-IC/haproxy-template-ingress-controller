# Kubernetes versions

HAPTIC requires Kubernetes 1.35 or newer, the oldest release the Kubernetes
project still maintains. Helm rejects clusters below the chart's `kubeVersion`
requirement.

## Tested versions

| Kubernetes | Coverage |
|------------|----------|
| 1.35 | Minimum supported version; routing, admission, upgrades, installation without Gateway API CRDs, and graceful shutdown |
| 1.37 | The same checks, plus the supported HAProxy 3.0–3.4 series |

Kubernetes 1.36 meets the chart's version requirement but doesn't have separate
test runs. For supported proxy versions, see [HAProxy versions](haproxy-versions.md).
