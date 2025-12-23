# pkg/dataplane/client

Low-level HAProxy Dataplane API client wrapper.

## Overview

Provides a wrapper around haproxytech/client-native for accessing the HAProxy Dataplane API.

## Quick Start

```go
import "haptic/pkg/dataplane/client"

client, err := client.New(client.Config{
    BaseURL:  "http://haproxy:5555",
    Username: "admin",
    Password: "password",
})
```

## License

See main repository for license information.
