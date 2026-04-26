# pkg/k8s/client

Initialises the Kubernetes client pair (typed clientset + dynamic client) the rest of `pkg/k8s` and `pkg/controller` depend on.

## Overview

Watchers and resource loaders need both a typed `kubernetes.Interface` (for built-in resources) and a `dynamic.Interface` (for CRDs that aren't in the typed scheme). `*Client` wraps both behind one struct and handles in-cluster vs. kubeconfig discovery, plus best-effort detection of the controller's own namespace from the service-account token mount.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"

// In-cluster (the production path)
c, err := client.New(client.Config{})

// Out-of-cluster (development)
c, err = client.New(client.Config{
    Kubeconfig: "/path/to/kubeconfig",
    Namespace:  "default", // empty = auto-detect from /var/run/secrets/.../namespace
})
if err != nil { /* ... */ }

// Typed clientset
pods, _ := c.Clientset().CoreV1().Pods("default").List(ctx, metav1.ListOptions{})

// Dynamic client for CRDs not in the typed scheme
gvr := schema.GroupVersionResource{Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxytemplateconfigs"}
list, _ := c.DynamicClient().Resource(gvr).Namespace(c.Namespace()).List(ctx, metav1.ListOptions{})
```

The `Config` struct has just two fields (`Kubeconfig`, `Namespace`); QPS/burst rate-limiting is hardcoded inside `New` to values appropriate for high-frequency CRD operations (QPS 50, burst 100). `NewFromClientset` builds a `*Client` from existing fakes — used by tests only.

## Other Exported Surfaces

| Symbol | Purpose |
|--------|---------|
| `(*Client).RestConfig() *rest.Config` | Underlying `*rest.Config` for callers that need to build their own client variants (e.g. metrics/v1 clients in tests). |
| `(*Client).GetResource(ctx, gvr, name) (*unstructured.Unstructured, error)` | Convenience wrapper around `DynamicClient().Resource(gvr).Namespace(c.Namespace()).Get(...)` — saves a line in CRD lookups. |
| `DiscoverNamespace() (string, error)` | Reads the namespace from `DefaultNamespaceFile` (the standard `/var/run/secrets/.../namespace` path). Used by `New` when `Config.Namespace` is empty. |
| `DiscoverNamespaceFromFile(path) (string, error)` | Same, but lets tests point at a fixture path. |
| `DefaultNamespaceFile` | The constant `/var/run/secrets/kubernetes.io/serviceaccount/namespace` — exported so callers can compare or override in tests. |
| `*ClientError` / `*NamespaceDiscoveryError` | Typed errors. Both implement `Unwrap()` so `errors.Is`/`errors.As` walks through to the underlying cause; use these to distinguish "couldn't reach the cluster" from "couldn't discover the namespace from the SA token mount". |

## See Also

- [`pkg/k8s/watcher`](../watcher/) — wraps `*Client` to deliver typed change callbacks
- [`pkg/k8s/store`](../store/) — backing storage for watcher results
- `client.go` — `Config` field semantics and the in-cluster vs kubeconfig discovery rules

## License

Apache-2.0 — see root `LICENSE`.
