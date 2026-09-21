# Kubernetes versions

HAPTIC requires Kubernetes 1.33 or newer. The chart uses native sidecars to keep
the agent available while HAProxy drains connections during pod termination.
Helm rejects clusters below the chart's `kubeVersion` requirement.

## Tested versions

| Kubernetes | Coverage |
|------------|----------|
| 1.33 | Minimum supported version; routing, admission, upgrades, installation without Gateway API CRDs, and graceful shutdown |
| 1.37 | The same checks, plus the supported HAProxy 3.0–3.4 series |

Kubernetes 1.34–1.36 meet the chart's version requirement but don't have separate
test runs. For supported proxy versions, see [HAProxy versions](haproxy-versions.md).
