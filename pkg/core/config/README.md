# pkg/core/config

Defines the internal `Config` / `Credentials` structs and the pure functions that load and validate them. No Kubernetes client calls, no event bus — everything here operates on already-materialised bytes or strings.

Upstream in the pipeline: `pkg/controller/conversion.ParseCRD` converts a `HAProxyTemplateConfig` CRD into the `*Config` this package defines.

## Public API

```go
// Fill in defaults (mutates in place)
func SetDefaults(cfg *Config)

// Required fields, port ranges, enum values
func ValidateStructure(cfg *Config) error

// Secret data → Credentials
func LoadCredentials(secretData map[string][]byte) (*Credentials, error)
func ValidateCredentials(creds *Credentials) error

// Helpers
func ParseSecretData(raw map[string]any) (map[string][]byte, error)
```

The Go fields are `PodSelector`, `Controller`, `Logging`, `Dataplane`, `TemplatingSettings`, `WatchedResources`, `WatchedResourcesIgnoreFields`, `Validators`, `TemplateSnippets`, `Maps`, `Files`, `SSLCertificates`, `K8sResources`, `CRTLists`, `HAProxyConfig`, `ValidationTests`. Three serialisation forms exist for the same struct, and they don't all agree:

- **Go field names** — PascalCase (`PodSelector`).
- **YAML keys (`yaml:` struct tags)** — snake_case at the top level (`pod_selector`, `templating_settings`, `watched_resources`, `haproxy_config`); a few nested fields use camelCase (`httpResources`, `currentConfig`, `extraContext`, `minHAProxyVersion`). `types.go`'s `yaml:` tags are authoritative.
- **CRD JSON keys (kubectl, ParseCRD)** — camelCase, per Kubernetes convention. The controller goes through `pkg/controller/conversion.ParseCRD` which deserialises into the typed CRD first and then maps it onto `*Config` field-by-field.

Use snake_case in YAML files; use camelCase in CRD manifests. The `types.go` source is the authoritative schema for either.

## Validation Layers

This package only does **structural** validation:

- Required fields present
- `int` fields in range (ports 1–65535, non-negative counters)
- Enum values from the allowed set
- Non-empty strings where semantically required
- `time.Duration` strings parse

It deliberately **does not**:

- Validate template syntax → `pkg/templating.ValidateTemplates`
- Validate JSONPath expressions → `pkg/k8s/indexer.ValidateJSONPath`
- Validate rendered HAProxy config → `pkg/dataplane.ValidateConfiguration`
- Apply cross-field business rules → `pkg/controller/validator`

Those run via scatter-gather in the controller so each validator can evolve independently.

## Key Defaults (`SetDefaults`)

Authoritative list is `defaults.go`. Ones operators commonly look up:

- `dataplane.port`: 5555
- `dataplane.minDeploymentInterval`: 2s
- `dataplane.driftPreventionInterval`: 60s
- `dataplane.deploymentTimeout`: 30s
- `dataplane.{mapsDir,sslCertsDir,generalStorageDir,configFile}`: `/etc/haproxy/...`
- `controller.leaderElection.{leaseName,leaseDuration,renewDeadline,retryPeriod}`: `haptic-leader`, 15s, 10s, 2s (matches Kubernetes' recommended fast-failover triplet — defaults.go:54-60)
- `controller.configPublishing.compressionThreshold`: 1 MiB
- `templatingSettings.engine`: `scriggo`

## Credentials Schema

`LoadCredentials` expects two non-empty string keys in the Secret data:

- `dataplane_username`
- `dataplane_password`

These are used to authenticate against the production HAProxy pods' Dataplane API instances. The controller's local `haproxy -c` validation step does not need credentials — it shells out to the binary directly with the rendered config and auxiliary files. `ValidateCredentials` rejects empty strings after base64 decode. No `String()` / `GoString()` methods are defined on `Credentials` — helps prevent accidental password leaks via `%v` or `log.Info("…", creds)`.

## See Also

- [`pkg/controller/conversion`](../../controller/conversion/) — CRD → `Config` adapter used by the running controller
- [`pkg/controller/configloader`](../../controller/configloader/) / [`credentialsloader`](../../controller/credentialsloader/) — event adapters that call into this package
- `docs/controller/docs/crd-reference.md` — user-facing field reference

## License

Apache-2.0 — see root `LICENSE`.
