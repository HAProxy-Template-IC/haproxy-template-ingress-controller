# pkg/controller/conversion

Converts the `HAProxyTemplateConfig` CRD (Kubernetes API surface) into the internal `*config.Config` (controller-internal) the rest of the controller works with.

## Overview

Two entry points:

| Function | Input | Output |
|----------|-------|--------|
| `ParseCRD(resource)` | `*unstructured.Unstructured` from a watcher | `(*config.Config, *v1alpha1.HAProxyTemplateConfig, error)` — both the converted internal config and the typed CRD wrapper for k8s metadata |
| `ConvertSpec(spec)` | typed `*v1alpha1.HAProxyTemplateConfigSpec` | `(*config.Config, error)` — useful when callers already have the typed object |

`ParseCRD` is the production path; the controller's CRD watcher hands raw unstructured bytes to it, then publishes the resulting `*config.Config` plus the CRD wrapper as a `ConfigParsedEvent`. The wrapper is kept around so downstream components (status updater, config publisher) have access to `metadata.name` / `metadata.namespace` / `metadata.uid` for owner references and status patches.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"

cfg, crd, err := conversion.ParseCRD(unstructuredResource)
if err != nil {
    // type mismatch, missing fields, or invalid YAML inside spec
    return err
}
// cfg  → *config.Config (templates, watched resources, dataplane settings, etc.)
// crd  → *v1alpha1.HAProxyTemplateConfig (typed CRD with metadata)
```

## Design

The conversion is intentionally a one-way street: CRD types live in `pkg/apis/haproxytemplate/v1alpha1` and are generated from kubebuilder annotations; internal `*config.Config` lives in `pkg/core/config` and is plain Go. Keeping them separate lets the internal type evolve independently of the CRD schema (and lets tests construct configs without going through YAML).

Validation of field-level constraints (template syntax, JSONPath expressions, port ranges) does **not** happen here — `pkg/controller/configchange.ConfigChangeHandler` runs the scatter-gather validation against the converted `*config.Config`. This package is just the structural conversion.

## See Also

- [`pkg/controller/configloader`](../configloader/) — calls `ParseCRD` and publishes `ConfigParsedEvent`
- [`pkg/controller/configchange`](../configchange/) — runs scatter-gather validation on the converted config
- [`pkg/apis/haproxytemplate/v1alpha1`](../../apis/haproxytemplate/v1alpha1/) — CRD type definitions
- [`pkg/core/config`](../../core/config/) — internal `*config.Config` definition

## License

Apache-2.0 — see root `LICENSE`.
