# pkg/controller/certloader

Stage-1 event adapter that turns webhook-certificate `Secret` updates into parsed PEM bytes.

## Overview

Subscribes to `CertResourceChangedEvent`, type-asserts the payload to a Kubernetes TLS Secret (`*unstructured.Unstructured`), pulls and base64-decodes `tls.crt` + `tls.key`, and publishes a `CertParsedEvent` carrying the decoded PEM bytes plus the Secret's `resourceVersion`. Errors (missing keys, malformed base64) are logged with the version number; nothing is published, and the previously-parsed certificate stays in effect.

The real cert *generation* and rotation happen outside the controller — typically cert-manager or a similar controller writes the Secret. This package only watches it.

## Quick Start

```go
import "gitlab.com/haproxy-haptic/haptic/pkg/controller/certloader"

loader := certloader.NewCertLoaderComponent(eventBus, logger)
go loader.Start(ctx)
```

The component is built on `pkg/controller/resourceloader.BaseLoader`, sharing the event-loop scaffold with `configloader` and `credentialsloader`. Subscription happens inside the constructor (before `bus.Start()`) so the initial Secret read isn't lost.

## Events

- Subscribes: `CertResourceChangedEvent`
- Publishes: `CertParsedEvent` (carrying `CertPEM`, `KeyPEM`, `Version`)

The watcher that produces `CertResourceChangedEvent` is wired in `pkg/controller/iteration.go` from a `pkg/k8s/watcher.SingleWatcher` pointed at the Secret named by `--webhook-cert-secret-name`.

## See Also

- [`pkg/controller/configloader`](../configloader/) / [`credentialsloader`](../credentialsloader/) — sibling loaders built on the same `BaseLoader` scaffold
- [`pkg/controller/webhook`](../webhook/) — downstream consumer of `CertParsedEvent` (the controller restarts the webhook adapter when fresh PEM bytes arrive)
- [`pkg/controller/resourceloader`](../resourceloader/) — shared event-loop scaffold

## License

Apache-2.0 — see root `LICENSE`.
