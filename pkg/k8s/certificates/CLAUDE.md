# Certificate Secret lifecycle

This utility persists operational certificate generations and publishes their
identity Secrets. It uses Kubernetes API credentials independently of the TLS
identities it renews. It has no event, template, or reconciliation dependencies.

Persist a complete generation before updating any identity Secret. Retries must
finish that generation. Never replace an unowned Secret or regenerate a missing
issuer while its identity Secrets remain. Keep private material out of errors,
logs, and results. Callers provide explicit Secret names and certificate usages;
this package knows no routing resource kinds.
