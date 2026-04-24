# pkg/controller/debug

Controller-specific `introspection.Var` implementations that expose controller state on the `/debug/vars` endpoint. This package is the bridge between the generic `pkg/introspection` server and the controller's internal state cache — operators querying `/debug/vars/config`, `/debug/vars/rendered`, etc. are hitting the Vars defined here.

## How It Fits Together

```
Controller (pkg/controller)
    │
    ├── StateCache — caches config / credentials / rendered / aux / resources
    │   from events, implements StateProvider
    │
    └── starts introspection.Server ──> Registry ──┐
                                                   │
                                      ┌────────────┴───────────┐
                                      │   debug.RegisterVariables
                                      │   (registers each Var)
                                      ▼
                       ConfigVar, CredentialsVar, RenderedVar,
                       AuxFilesVar, ResourcesVar, FullStateVar,
                       PipelineVar, ValidatedVar, ErrorsVar, EventsVar
```

Each `Var` has one responsibility: fetch a piece of state from the `StateProvider` (or the event buffer), shape it into JSON, and return it when the introspection server calls `.Get()`.

## StateProvider

The controller supplies a `StateProvider` that exposes cached state under an `RWMutex`:

```go
type StateProvider interface {
    GetConfig() (*config.Config, string, error)               // cfg, version, err
    GetCredentials() (*config.Credentials, string, error)     // creds metadata only
    GetRenderedConfig() (string, time.Time, error)
    GetAuxiliaryFiles() (*dataplane.AuxiliaryFiles, time.Time, error)
    GetResourceCounts() (map[string]int, error)
    GetResourcesByType(resourceType string) ([]any, error)
    // plus pipeline / validation / error accessors used by
    // PipelineVar, ValidatedVar, ErrorsVar
}
```

Every method returns a "not ready" error until the relevant event has been observed — callers get a clean 404-ish response instead of empty payloads during startup.

## Registering

```go
import (
    "context"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/debug"
    "gitlab.com/haproxy-haptic/haptic/pkg/introspection"
)

registry := introspection.NewRegistry()

eventBuffer := debug.NewEventBuffer(1000, eventBus)
go eventBuffer.Run(ctx)

debug.RegisterVariables(registry, stateProvider, eventBuffer)

server := introspection.NewServer(":8080", registry)
go server.Run(ctx)
```

`EventBuffer` subscribes to the `EventBus` separately from the `commentator` so that the debug endpoint has an independent ring-buffered history for `/debug/vars/events` — the two consumers can be tuned and cleaned up in isolation.

## Exposed Paths

Each of these maps to a `Var` in this package:

| Path | Var | Returns |
|------|-----|---------|
| `/debug/vars/config` | `ConfigVar` | Parsed config + version + load timestamp |
| `/debug/vars/credentials` | `CredentialsVar` | Metadata only (`has_dataplane_creds`, version) — **never** the passwords |
| `/debug/vars/rendered` | `RenderedVar` | Last rendered `haproxy.cfg` + size + timestamp |
| `/debug/vars/auxfiles` | `AuxFilesVar` | Last rendered maps / SSL / general / crt-list files + counts |
| `/debug/vars/resources` | `ResourcesVar` | Per-type resource counts |
| `/debug/vars/events` | `EventsVar` | Ring-buffered event history (last N) |
| `/debug/vars/state` | `FullStateVar` | Aggregate of the above — large; prefer the specific paths |
| `/debug/vars/pipeline` | `PipelineVar` | Last pipeline execution metadata (stages, timings) |
| `/debug/vars/validated` | `ValidatedVar` | Last validation result (syntax + semantic phases) |
| `/debug/vars/errors` | `ErrorsVar` | Recent per-component errors |

The introspection server supports `?field={...}` JSONPath on every response, so operators can narrow to a specific field without downloading the whole payload.

## Security

- `CredentialsVar` is deliberately built to expose metadata only. It constructs its response map field-by-field — no marshalling of the `Credentials` struct, so a future accidental field addition can't leak the password by default.
- `FullStateVar` includes the rendered `haproxy.cfg`, which references internal hostnames and backend IPs. Operators should restrict the debug port with a NetworkPolicy (see `docs/controller/docs/operations/security.md`).
- Validators in this package never read raw `Credentials.Password` fields for their own responses — the `StateProvider.GetCredentials` signature returns `*config.Credentials` but the `CredentialsVar.Get()` implementation picks out individual safe fields.

## Testing

```bash
go test ./pkg/controller/debug/...            # unit tests
go test ./pkg/controller/debug/... -race      # race detector
```

Each Var has a test that constructs a mock `StateProvider`, calls `.Get()`, and asserts on the returned shape — including explicit "no password in output" assertions on `CredentialsVar` to prevent regressions.

## See Also

- `pkg/introspection` — generic registry + HTTP server this package plugs into
- `pkg/controller/debug/CLAUDE.md` — developer context, walkthrough for adding a new Var
- `docs/controller/docs/operations/debugging.md` — operator-facing view of the same endpoints
- `tests/acceptance/debug_client.go` — end-to-end consumer that polls these endpoints

## License

Apache-2.0 — see root `LICENSE`.
