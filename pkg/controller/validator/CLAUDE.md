# pkg/controller/validator - Configuration Validation

Development context for validation components.

## When to Work Here

Work in this package when:

- Adding new validation rules
- Implementing new validator types
- Modifying scatter-gather coordination
- Fixing validation bugs

**DO NOT** work here for:

- Parsing configuration → Use `pkg/core/config`
- Event bus infrastructure → Use `pkg/events`

## Package Purpose

Houses the **responder** side of the configuration scatter-gather validation pattern, plus the rendered-config validator used by the deployment pipeline. The orchestrator that fans `ConfigValidationRequest` out to these responders and aggregates the answers lives in `pkg/controller/configchange.ConfigChangeHandler`.

## Architecture

```
ConfigParsedEvent
    ↓
configchange.ConfigChangeHandler  (issues request, gathers responses)
    ↓ ConfigValidationRequest (scatter via bus.Request)
    ├→ BasicValidator       (structural validation)
    ├→ TemplateValidator    (template syntax)
    └→ JSONPathValidator    (JSONPath expressions)
        ↓ ConfigValidationResponse (gather)
configchange.ConfigChangeHandler  (publishes outcome)
    ↓
ConfigValidatedEvent  or  ConfigInvalidEvent
```

`HAProxyValidatorComponent` (also in this package) is unrelated to the config scatter-gather; it validates rendered HAProxy configurations during the render-validate pipeline using the three-phase strategy (client-native syntax + OpenAPI schema + `haproxy -c` semantic).

## Validators

- **BasicValidator**: Structural validation (required fields, types)
- **TemplateValidator**: Template syntax validation. Calls `helpers.ExtractTemplatesFromConfig`, which walks `spec.haproxyConfig`, `spec.templateSnippets`, `spec.maps`, `spec.files`, and `spec.sslCertificates` (there is no flat `spec.templates` field), then compiles them with `templating.NewScriggoWithDeclarations`.
- **JSONPathValidator**: JSONPath expression validation (evaluates each `indexBy` expression)
- **HAProxyValidatorComponent**: Three-phase validation of rendered HAProxy configs (pipeline-side)

## Resources

- Scatter-gather pattern: `pkg/events/CLAUDE.md`
- Configuration schema: `pkg/core/CLAUDE.md` (or `pkg/core/config/README.md` for the public API)
