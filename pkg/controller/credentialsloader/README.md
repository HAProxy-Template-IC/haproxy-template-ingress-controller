# pkg/controller/credentialsloader

CredentialsLoader component - parses Secret data into credential structures.

## Overview

Event-driven component that subscribes to SecretResourceChangedEvent, extracts credentials from Secret resources, and publishes CredentialsUpdatedEvent or CredentialsInvalidEvent.

## Quick Start

```go
import "haptic/pkg/controller/credentialsloader"

loader := credentialsloader.NewCredentialsLoaderComponent(bus, logger)
go loader.Start(ctx)
```

## Expected Secret Format

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: haproxy-credentials
type: Opaque
stringData:
  dataplane_username: admin
  dataplane_password: password
```

**Required fields**:

- `dataplane_username`: HAProxy Dataplane API username
- `dataplane_password`: HAProxy Dataplane API password

## Events

### Subscribes To

- **SecretResourceChangedEvent**: Secret updated

### Publishes

- **CredentialsUpdatedEvent**: Valid credentials loaded
- **CredentialsInvalidEvent**: Invalid or missing credentials

## License

See main repository for license information.
