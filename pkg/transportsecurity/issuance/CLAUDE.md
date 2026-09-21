# Certificate issuance

This utility issues operational identities with the Go standard library. It has
no Kubernetes or controller dependencies. Callers own storage, renewal timing,
and trust distribution.

CA transitions may include a cross-signed copy of the new CA. Its lifetime must
fit within the old and new CAs' validity and the caller's trust-overlap deadline.
Never issue a leaf that outlives its CA or treat expired trust as valid.
