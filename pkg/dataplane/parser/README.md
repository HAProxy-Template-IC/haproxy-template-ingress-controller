# pkg/dataplane/parser

HAProxy configuration parser.

## Overview

Parses HAProxy configuration files into structured format for comparison and validation.

## Quick Start

```go
import "haptic/pkg/dataplane/parser"

parser, err := parser.New()
parsed, err := parser.Parse(haproxyConfig)
```

## License

See main repository for license information.
