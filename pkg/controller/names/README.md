# pkg/controller/names

Well-known string constants shared across the controller layer.

## Overview

Two recurring string identifiers that several packages reference. Keeping them in one place lets compile-time checks catch typos rather than failing silently at runtime when a `switch` on a resource-type name misses its case.

## Constants

| Name | Value | Use for |
|------|-------|---------|
| `MainTemplateName` | `"haproxy.cfg"` | The primary HAProxy configuration template's key in `cfg.HAProxyConfig` and the rendered-output key on `*PipelineResult` |
| `HAProxyPodsResourceType` | `"haproxy-pods"` | The auto-injected pod watcher's resource-type key — appears in store maps, event filters, and the `controller.haproxy_pods` template context |

If you find yourself string-typing either of these somewhere, replace it with the constant from this package.

## License

Apache-2.0 — see root `LICENSE`.
