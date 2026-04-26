# pkg/dataplane/auxiliaryfiles

Compare-and-sync helpers for the auxiliary files HAProxy serves alongside its main config: maps, general files, CRT lists, SSL certificates, and SSL CA files.

## Overview

Each file kind has a `Compare*` and a `Sync*` pair. `Compare` fetches the current contents from the Dataplane API's storage endpoints, diffs them against a desired list, and returns a typed `*FileDiffGeneric[T]`. `Sync` applies the diff (creates / updates / deletes). The two halves can be called separately so the orchestrator can decide whether and when to commit changes.

The package only deals with auxiliary files. The main HAProxy config goes through `pkg/dataplane.Client.Sync` + the comparator pipeline; storage state on individual HAProxy pods (which file is on which pod) lives in `pkg/k8s/configpublisher`.

## File Kinds and Entry Points

| Kind | Type | Compare | Sync |
|------|------|---------|------|
| Maps | `MapFile` | `CompareMapFiles` | `SyncMapFiles` |
| General files | `GeneralFile` | `CompareGeneralFiles` | `SyncGeneralFiles` |
| SSL certificates | `SSLCertificate` | `CompareSSLCertificates` | `SyncSSLCertificates` |
| CRT lists | `CRTListFile` | `CompareCRTLists` | `SyncCRTLists` |
| SSL CA files | `SSLCaFile` | `CompareSSLCaFiles` | `SyncSSLCaFiles` |

All Compare functions return `*FileDiffGeneric[T]` (with type aliases `FileDiff`, `MapFileDiff`, `SSLCertificateDiff`, `CRTListDiff`, `SSLCaFileDiff` so call sites read naturally).

## Quick Start

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

dpClient, _ := client.New(ctx, &client.Config{...})

diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, dpClient, desired)
if err != nil { /* ... */ }

changed, err := auxiliaryfiles.SyncGeneralFiles(ctx, dpClient, diff)
// 'changed' is the list of file names that were actually written or removed.
```

CRT lists are special-cased — but **not** for the reason an older draft of this README claimed. `CompareCRTLists` / `SyncCRTLists` *always* go through general-file storage via `CRTListsToGeneralFiles`, regardless of HAProxy version. The reason is reload accounting: the native CRT-list API (`POST ssl_crt_lists`) triggers a reload and doesn't support `skip_reload`, while general-file `CREATE` returns 201 with no reload, letting the orchestrator batch every aux-file change into the single reload that the main config sync triggers. There's no `Capabilities.SupportsCrtList` branch here. (See the `Storage strategy` block in `crtlist.go` for the rationale.)

## See Also

- [`pkg/dataplane/client`](../client/) — provides the `*DataplaneClient` consumed here
- [`pkg/dataplane`](../) — `Client.Sync` invokes these helpers transparently
- [`pkg/k8s/configpublisher`](../../k8s/configpublisher/) — publishes per-pod auxiliary file state to Kubernetes CRDs

## License

Apache-2.0 — see root `LICENSE`.
