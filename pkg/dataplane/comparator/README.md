# pkg/dataplane/comparator

HAProxy configuration comparator.

## Overview

Compares parsed HAProxy configurations to determine if deployment is needed.

## Quick Start

```go
import "haptic/pkg/dataplane/comparator"

comp := comparator.New()
needsUpdate := comp.Compare(current, desired)
```

## License

See main repository for license information.
