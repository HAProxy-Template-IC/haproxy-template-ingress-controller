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

p := configpublisher.New(k8sClient, crdClient, logger)
// or:
p := configpublisher.NewWithListers(k8sClient, crdClient, listers, logger)

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

`PublishConfig` is best-effort for child resources — a single map file failing to update logs at debug level and continues with the rest. The next reconciliation re-publishes everything, so transient API errors heal automatically.

The top-level `HAProxyCfg` upsert is the only operation that returns an error; child operations log and skip. Status updates also log and skip on conflict (the next reconciliation cycle will retry).

## See Also

- [`pkg/dataplane`](../../dataplane/) — the live HAProxy push, separate from this observability path
- [`pkg/controller/configpublisher`](../../controller/configpublisher/) — event adapter that drives this publisher
- [`pkg/apis/haproxytemplate/v1alpha1`](../../apis/haproxytemplate/v1alpha1/) — the CRD types this package reads/writes

## License

Apache-2.0 — see root `LICENSE`.
