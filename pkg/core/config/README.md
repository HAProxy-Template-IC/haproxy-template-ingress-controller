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

The Go fields are `PodSelector`, `Controller`, `Logging`, `Dataplane`, `TemplatingSettings`, `WatchedResources`, `WatchedResourcesIgnoreFields`, `Validators`, `TemplateSnippets`, `Maps`, `Files`, `SSLCertificates`, `K8sResources`, `HAProxyConfig`, `ValidationTests`. Three serialisation forms exist for the same struct, and they don't all agree:

- **Go field names** — PascalCase (`PodSelector`).
- **YAML keys (`yaml:` struct tags)** — snake_case at the top level (`pod_selector`, `templating_settings`, `watched_resources`, `haproxy_config`); a few nested fields use camelCase (`httpResources`, `currentConfig`, `extraContext`, `minHAProxyVersion`). `types.go`'s `yaml:` tags are authoritative.
- **CRD JSON keys (kubectl, ParseCRD)** — camelCase, per Kubernetes convention. The controller goes through `pkg/controller/conversion.ParseCRD` which deserialises into the typed CRD first and then maps it onto `*Config` field-by-field.

The YAML tags describe internal serialization. Author `HAProxyTemplateConfig` manifests with camelCase keys from `pkg/apis/haproxytemplate/v1alpha1`; the CLI and controller use that public schema.

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
- `controller.leaderElection.{leaseName,leaseDuration,renewDeadline,retryPeriod}`: `haptic-leader`, 30s, 20s, 5s
- `controller.configPublishing.compressionThreshold`: 1 MiB
- `templatingSettings.engine`: `scriggo`

## Credentials Schema

`LoadCredentials` expects two non-empty string keys in the Secret data:

- `dataplane_username`
- `dataplane_password`

These credentials authenticate controller requests to the HAPTIC agent in each
HAProxy pod. Local `haproxy -c` validation needs no credentials.
`ParseSecretData` decodes base64; `LoadCredentials` and `ValidateCredentials`
reject empty values. `Credentials` stores plain strings and has no redacting
formatter, so don't log the struct or format it with `%v`.

## See Also

- [`pkg/controller/conversion`](../../controller/conversion/) — CRD → `Config` adapter used by the running controller
- [`pkg/controller/configloader`](../../controller/configloader/) / [`credentialsloader`](../../controller/credentialsloader/) — event adapters that call into this package
- `docs/site/docs/crd-reference.md` — user-facing field reference

## License

Apache-2.0 — see root `LICENSE`.
