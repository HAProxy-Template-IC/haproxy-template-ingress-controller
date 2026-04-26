# pkg/controller/validator

Configuration validators (scatter-gather participants).

## Overview

Three validators run as the responder side of the scatter-gather validation pattern. Each one wraps a shared `BaseValidator`, subscribes to `ConfigValidationRequest` on the EventBus, and responds with a `ConfigValidationResponse` flagged valid or invalid. The orchestrator that fans the request out and aggregates responses is **not** in this package — it lives in `pkg/controller/configchange.ConfigChangeHandler`. The same package also hosts `HAProxyValidatorComponent`, which performs the three-phase HAProxy validation (syntax + OpenAPI schema + `haproxy -c`) for rendered configs as part of the deployment pipeline.

## Validators

- **BasicValidator** — Structural validation (required fields, type checks, basic schema sanity).
- **TemplateValidator** — Calls `helpers.ExtractTemplatesFromConfig` to collect every template defined under `haproxyConfig`, `templateSnippets`, `maps`, `files`, and `sslCertificates`, then compiles them with `templating.NewScriggoWithDeclarations` to surface syntax errors before they reach the render pipeline.
- **JSONPathValidator** — Evaluates the `indexBy` JSONPath expressions on every entry under `spec.watchedResources` against a synthetic resource.
- **HAProxyValidatorComponent** — Three-phase validation of a rendered HAProxy config (used by the render-validate pipeline, not the config-validation scatter-gather).

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"

basic := validator.NewBasicValidator(bus, logger)
tmpl := validator.NewTemplateValidator(bus, logger)
jp := validator.NewJSONPathValidator(bus, logger)

go basic.Start(ctx)
go tmpl.Start(ctx)
go jp.Start(ctx)
```

The validators take only `(eventBus, logger)` — no engine, no validator-name list. Their internal name (`"basic"`, `"template"`, `"jsonpath"`) is what the orchestrator uses in its `ExpectedResponders` list.

## Events

### Subscribed

- `ConfigValidationRequest` — scatter-gather validation request (handled by all three validators)

### Published

- `ConfigValidationResponse` — one per validator, per request

`ConfigValidatedEvent` and `ConfigInvalidEvent` are published by the orchestrator (`configchange.ConfigChangeHandler`) after collecting all three responses, not by these validators directly.

## License

Apache-2.0 — see root `LICENSE`.
