# pkg/core/config

Configuration types, parsing, and validation for the HAProxy Template Ingress Controller.

## Overview

Defines configuration schema and provides functions for parsing ConfigMap data and loading credentials from Secrets.

## Installation

```go
import "haproxy-template-ic/pkg/core/config"
```

## Quick Start

```go
// Parse configuration from ConfigMap data
cfg, err := config.ParseConfig(configYAML)
if err != nil {
    log.Fatal(err)
}

// Load credentials from Secret data
creds, err := config.LoadCredentials(secretData)
if err != nil {
    log.Fatal(err)
}
```

## Configuration Schema

### Main Types

```go
type Config struct {
    PodSelector                  PodSelector
    Controller                   ControllerConfig
    Logging                      LoggingConfig
    Dataplane                    DataplaneConfig
    WatchedResourcesIgnoreFields []string
    WatchedResources             map[string]WatchedResource
    TemplateSnippets             map[string]TemplateSnippet
    Maps                         map[string]MapFile
    Files                        map[string]GeneralFile
    SSLCertificates              map[string]SSLCertificate
    HAProxyConfig                HAProxyConfig
}

type Credentials struct {
    DataplaneUsername string
    DataplanePassword string
}
```

## Validation

**Basic Validation** (this package):

- Required fields present
- Port numbers valid (1-65535)
- Enum values valid

**Advanced Validation** (pkg/controller/validator):

- Template syntax
- JSONPath expressions
- Cross-field validation

## License

See main repository for license information.
