# pkg/k8s/configpublisher

Publishes the rendered HAProxy runtime configuration as observable Kubernetes CRDs (`HAProxyCfg`, `HAProxyMapFile`, `HAProxyGeneralFile`, `HAProxyCRTListFile`) plus the SSL Secrets that auxiliary files reference.

## Overview

The reconciliation pipeline produces a `*dataplane.AuxiliaryFiles` and a string of HAProxy config; this package writes those into the cluster so operators can `kubectl describe` them, GitOps tooling can diff them, and audit logs can trace which controller produced which config. The CRDs are owned by their parent `HAProxyTemplateConfig` so they cascade-delete on uninstall.

This package does **not** push config to HAProxy pods — that's `pkg/dataplane.Client.Sync` driven from `pkg/controller/deployer`. The publisher lives between rendering and deployment as a parallel observability path.

## Quick Start

```go
import (
    "context"

    "k8s.io/client-go/kubernetes"
    "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
    "gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

p := configpublisher.NewWithListers(k8sClient, crdClient, listers, logger)
// pass nil listers to fall back to direct API reads

result, err := p.PublishConfig(ctx, &configpublisher.PublishRequest{
    TemplateConfigName:      "haptic-config",
    TemplateConfigNamespace: "haptic",
    TemplateConfigUID:       crd.UID,
    Config:                  rendered,             // the rendered haproxy.cfg text
    Checksum:                sha256OfRendered,
    AuxiliaryFiles:          auxFiles,
    // optional: ConfigPath, NameSuffix, ValidationError, CompressionThreshold
    // (see types.go for the full field list)
})
// result.RuntimeConfigName, MapFileNames, SecretNames, GeneralFileNames, CRTListFileNames
```

`NewWithListers` is the production path — passing the informer-backed listers means status updates check the cache before issuing a GET, cutting API-server load on busy clusters.

## What Gets Published

| Kind | Field source | Purpose |
|------|--------------|---------|
| `HAProxyCfg` | `req.Config` | The rendered `haproxy.cfg` text |
| `HAProxyMapFile` | one per `AuxiliaryFiles.MapFiles[i]` | Map file contents (path-prefix.map, etc.) |
| `HAProxyGeneralFile` | one per `AuxiliaryFiles.GeneralFiles[i]` | Custom error pages, raw files |
| `HAProxyCRTListFile` | one per `AuxiliaryFiles.CRTListFiles[i]` | crt-list manifests for SSL frontends |
| `Secret` (TLS type) | one per `AuxiliaryFiles.SSLCertificates[i]` | Cert + key bundle as a standard kubernetes.io/tls Secret |

Every child carries `metadata.ownerReferences` pointing at the parent `HAProxyCfg`, so `kubectl delete haproxycfg <name>` cascades to maps / general files / crt-lists / Secrets.

## Failure Mode

`PublishConfig` succeeds only after the `HAProxyCfg`, every desired child, the complete child-reference status, and removal of obsolete owned children have succeeded. A required write or cleanup failure returns an `IncompletePublicationError` identifying the failed stage and resource. Repeating the same request is safe: resources already in the desired state are skipped, and incomplete work resumes. The publisher canonicalizes identical file definitions and rejects conflicting Dataplane storage identities before its first API write. Auxiliary-set annotations, committed parent references, and object-version preconditions fence cleanup, preventing a retired publication from deleting newer output. Invalid publications suffix their child names with `-invalid`, keeping the last valid artifact set intact. Long, colliding, or foreign-owned names receive stable identity hashes, so every resource name stays valid and one runtime config never takes over another's child.

## See Also

- [`pkg/dataplane`](../../dataplane/) — the live HAProxy push, separate from this observability path
- [`pkg/controller/configpublisher`](../../controller/configpublisher/) — event adapter that drives this publisher
- [`pkg/apis/haproxytemplate/v1alpha1`](../../apis/haproxytemplate/v1alpha1/) — the CRD types this package reads/writes

## License

Apache-2.0 — see root `LICENSE`.
