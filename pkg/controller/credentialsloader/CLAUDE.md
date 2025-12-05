# pkg/controller/credentialsloader - Credentials Loader

Development context for the CredentialsLoader component.

## When to Work Here

Work in this package when:

- Modifying Secret parsing logic
- Changing credential extraction
- Adding credential validation
- Debugging credential loading

**DO NOT** work here for:

- Credential schema → Use `pkg/core/config`
- Secret watching → Use `pkg/controller/resourcewatcher`

## Package Purpose

Event-driven component that parses Secret data into config.Credentials structures. Part of Stage 1 (Config Management).

## Architecture

```
SecretResourceChangedEvent
    ↓
CredentialsLoaderComponent
    ├─ Extract Secret.Data
    ├─ Parse credentials
    └─ Publish CredentialsUpdatedEvent or CredentialsInvalidEvent
```

## Usage

```go
loader := credentialsloader.NewCredentialsLoaderComponent(bus, logger)
go loader.Start(ctx)
```

## Common Pitfalls

### Missing Required Fields

Secret must contain `dataplane_username` and `dataplane_password`.

### Base64 Encoding

Kubernetes automatically handles base64 encoding/decoding. Component receives decoded values.
