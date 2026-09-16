# pkg/dataplane/auxiliaryfiles

Value types for files rendered alongside `haproxy.cfg`. This package performs no
network or filesystem operations. The renderer produces the files, the render
plan describes them, and the HAPTIC agent writes them.

## File types

| Type | Content | Identifier |
|------|---------|------------|
| `GeneralFile` | Error pages, policy files, or other auxiliary data | `Filename` |
| `MapFile` | HAProxy map entries | `Path` |
| `SSLCertificate` | PEM certificate and private key | `Path` |
| `CRTListFile` | Certificate references, options, and SNI filters | `Path` |
| `SSLCaFile` | PEM trust bundle | `Path` |

All types implement `FileItem`: `GetIdentifier()` and `GetContent()`.
`Content` is excluded from JSON serialization to keep key material out of debug
responses.

`GeneralFile.IsCaFile` marks a trust bundle for runtime CA-store updates.
`ReloadsOnPush()` reads `ReloadOnPush`, treating nil as true. Set it false only
for files whose consumer can accept updates without an HAProxy reload.

## Related packages

- [`renderplan`](../renderplan/) — immutable description of the desired configuration
- [`deployplan`](../deployplan/) — per-pod change decisions
- [`agent/files`](../agent/files/) — file writes, journaling, and recovery

## License

Apache-2.0 — see root `LICENSE`.
