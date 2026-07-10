# pkg/compression

Tiny zstd + base64 compression helper used for the HAProxyCfg / HAProxyMapFile / HAProxyGeneralFile / HAProxyCRTListFile CRD payloads.

## Overview

Two functions, no state to construct, no goroutines. The package wraps a process-global `zstd.Encoder` (level `SpeedDefault`) and `zstd.Decoder` lazily so callers don't pay the construction cost more than once.

The encoder/decoder are global because compression is always sequential in the controller's hot path — every call holds the encoder for the duration. If concurrent compression ever becomes desirable, switch to a `sync.Pool`.

## API

```go
func Compress(data string) (string, error)    // → zstd-compressed bytes, base64-encoded
func Decompress(data string) (string, error)  // ← base64 → zstd → original string
```

`Decompress(Compress(s))` round-trips. The base64 wrapper is there because the compressed payload lives inside Kubernetes CRD `string` fields, which can't carry raw binary.

## When to Compress

`pkg/k8s/configpublisher.PublishConfig` switches to compression when the payload exceeds `controller.configPublishing.compressionThreshold` (default 1 MiB). The CRD then carries `spec.compressed: true` and consumers know to call `Decompress` on `spec.content`.

```bash
# Operator-side decompression (using the CLI):
kubectl get haproxycfg <name> -o jsonpath='{.spec.content}' | base64 -d | zstd -d
```

## See Also

- [`pkg/k8s/configpublisher`](../k8s/configpublisher/) — primary caller; threshold-driven compression
- `docs/site/docs/crd-reference.md` — operator-facing description of the compression knob

## License

Apache-2.0 — see root `LICENSE`.
