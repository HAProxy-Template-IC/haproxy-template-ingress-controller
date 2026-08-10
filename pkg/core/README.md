# pkg/core

Shared primitives used everywhere in the controller. Two sub-packages, both intentionally pure (standard library + generated CRD types only):

- **`pkg/core/config`** — parses and validates the YAML that comes out of a `HAProxyTemplateConfig` CRD plus the controller's credentials `Secret`. Pure functions, no Kubernetes client calls.
- **`pkg/core/logging`** — sets up structured `log/slog` logging for the binary.

Module path: `gitlab.com/haproxy-haptic/haptic`. Source is authoritative; this README is a short map. When in doubt, prefer `go doc ./pkg/core/config` over the prose here or in `CLAUDE.md`.

## `config` — Loading and Validating

```go
import (
    "log"

    "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// 1. Obtain a *config.Config (the running controller gets one from
//    pkg/controller/conversion.ParseCRD, which maps a HAProxyTemplateConfig
//    CRD onto this struct).

// 2. Fill in defaults.
config.SetDefaults(cfg)

// 3. Structural validation (required fields, port ranges, enum values).
if err := config.ValidateStructure(cfg); err != nil {
    log.Fatalf("invalid config: %v", err)
}
```

Credentials follow the same shape:

```go
creds, err := config.LoadCredentials(secret.Data)   // map[string][]byte
if err != nil {
    log.Fatal(err)
}
if err := config.ValidateCredentials(creds); err != nil {
    log.Fatal(err)
}
```

Required `Secret` keys: `dataplane_username`, `dataplane_password` — both non-empty.

### What This Package Does Not Validate

`ValidateStructure` is deliberately narrow: it catches malformed YAML, missing required fields, out-of-range ports, and invalid enum values. **It does not** validate:

- Template syntax → `pkg/templating.ValidateTemplates`
- JSONPath expressions → `pkg/k8s/indexer.ValidateJSONPath`
- Cross-field business rules or rendered HAProxy config → `pkg/controller/validator` + `pkg/dataplane`

Those run via scatter-gather in the controller so each validator can evolve independently.

### Defaults Set by `SetDefaults`

Authoritative list lives in `pkg/core/config/defaults.go`. Commonly surprising ones:

- `dataplane.port`: `5555`
- `dataplane.minDeploymentInterval`: `2s`
- `dataplane.driftPreventionInterval`: `60s`
- `dataplane.deploymentTimeout`: `30s`
- `dataplane.{mapsDir,sslCertsDir,generalStorageDir,configFile}`: the standard `/etc/haproxy/...` paths
- `controller.leaderElection.{leaseName,leaseDuration,renewDeadline,retryPeriod}`: `haptic-leader`, `30s`, `20s`, `5s` (deliberately 2x the kube-controller-manager / kube-scheduler convention, for starvation headroom; the Helm chart leaves the timings alone but rewrites `leaseName` to the release fullname — see [High Availability](../../docs/site/docs/operations/high-availability.md))
- `controller.configPublishing.compressionThreshold`: `1048576` (1 MiB)
- `templatingSettings.engine`: `scriggo`

Two serialisations exist for the same struct, and they don't agree — see `pkg/core/config/README.md` for the full mapping:

- **CRD JSON keys** (what operators write in `kubectl apply`-style manifests, what `kubectl get -o yaml` prints): camelCase (`podSelector`, `watchedResources`, `indexBy`, `templatingSettings`).
- **YAML keys (`yaml:` struct tags)**: snake_case at the top level (`pod_selector`, `watched_resources`, `template_snippets`, `haproxy_config`, …) with a few nested fields in camelCase (`extraContext`, `currentConfig`, `httpResources`, `minHAProxyVersion`).

Use camelCase in CRD manifests; the `yaml:` tags in `types.go` are the authoritative shape for the YAML form.

## `logging` — slog Setup

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/core/logging"

// Dynamic (runtime-adjustable via SetLevel; level strings: "TRACE",
// "DEBUG", "INFO", "WARN"/"WARNING", "ERROR")
logger := logging.NewDynamicLogger(os.Getenv("LOG_LEVEL"))
slog.SetDefault(logger)
logging.SetLevel("DEBUG")  // bumps all existing loggers
```

Output is logfmt (`slog.NewTextHandler`) on **stdout** — not JSON, not stderr. The controller wires up `NewDynamicLogger` at startup using `LOG_LEVEL`, then `SetLevel`s from the CRD's `spec.logging.level` once the config loads. See [`pkg/core/logging/README.md`](./logging/README.md) for the full API.

## Testing

```bash
go test ./pkg/core/...            # unit tests
go test ./pkg/core/... -race      # race detector
```

All functions here are pure, so tests are straightforward table-driven cases — see `pkg/core/config/loader_test.go` and `validator_test.go` for the canonical patterns.

## See Also

- [`pkg/controller/conversion`](../controller/conversion/) — converts `HAProxyTemplateConfig` CRD types into the `config.Config` this package consumes
- [`pkg/controller/configloader`](../controller/configloader/) / [`credentialsloader`](../controller/credentialsloader/) — event adapters that watch the CRD / Secret and call into this package
- `docs/site/docs/crd-reference.md` — user-facing field reference

## License

Apache-2.0 — see root `LICENSE`.
