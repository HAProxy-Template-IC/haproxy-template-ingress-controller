# pkg/controller/currentconfigstore

In-memory cache of the parsed HAProxy `currentConfig` exposed to templates.

## Overview

Templates can reference the previously-deployed HAProxy configuration to keep server-slot ordering stable across rolling deploys (so connections don't migrate when an unrelated backend changes). This package provides the cache that holds that pre-parsed config: the `HAProxyCfg` CRD watcher feeds it raw resource bytes, and the renderer reads back a `*parserconfig.StructuredConfig` that templates consume via the `currentConfig` runtime variable.

This is a **utility component** — direct calls, no event loop, no goroutine. It's safe for concurrent reads and serialised writes via an internal `sync.RWMutex`.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"

store, err := currentconfigstore.New(logger)
if err != nil { /* parser construction failed */ }

// Watcher's OnChange callback feeds the raw resource (or nil on delete)
store.Update(unstructuredHAProxyCfg)

// Renderer pulls the parsed value
cfg := store.Get() // *parserconfig.StructuredConfig (nil if not yet populated)
```

## Hot-Path Optimisations

`Update` is called every time the `HAProxyCfg` CRD changes, including no-op status updates. Two short-circuits keep the parsing cost bounded:

1. **Generation check** — if `metadata.generation` matches the last-seen value, the spec hasn't changed and the cached config is returned without re-parsing.
2. **Content hash** — if the generation moved, a `spec.checksum` field (set by the config-publisher) is compared against the cached hash; if it matches, parsing is skipped without even decompressing. If no `spec.checksum` is present the content is decompressed and SHA-256'd, and parsing is skipped if the hash matches.

A single `*parser.Parser` is reused across calls to avoid the per-call `parser.New()` overhead.

## See Also

- [`pkg/dataplane/parser`](../../dataplane/parser/) — the `client-native` wrapper that produces `*StructuredConfig`
- [`pkg/controller/renderer`](../renderer/) — the consumer that exposes `Get()` to templates as `currentConfig`
- [`pkg/compression`](../../compression/) — handles the optional zstd+base64-encoded payload of large `HAProxyCfg` resources (above `controller.configPublishing.compressionThreshold`, default 1 MiB)

## License

Apache-2.0 — see root `LICENSE`.
