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

Stage 1 component that implements scatter-gather validation pattern. Multiple validators respond to ConfigValidationRequest, coordinator aggregates results.

## Architecture

```
ConfigParsedEvent
    ↓
Coordinator → ConfigValidationRequest (scatter-gather)
    ├→ BasicValidator (structural validation)
    ├→ TemplateValidator (template syntax)
    └→ JSONPathValidator (JSONPath expressions)
        ↓
    Responses aggregated
        ↓
ConfigValidatedEvent or ConfigInvalidEvent
```

## Validators

- **BasicValidator**: Structural validation (required fields, types)
- **TemplateValidator**: Template syntax validation
- **JSONPathValidator**: JSONPath expression validation

## Resources

- Scatter-gather pattern: `pkg/events/CLAUDE.md`
- Configuration schema: `pkg/core/config/CLAUDE.md`
