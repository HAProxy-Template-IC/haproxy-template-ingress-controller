# Transport security

This package is a utility: it loads certificate snapshots and builds authenticated
HTTP transports. It has no Kubernetes, controller, or HAProxy dependencies.

Each source reads one immutable directory revision. Rotate files by replacing a
symlink to a complete revision; Kubernetes Secret mounts expose that revision at
`..data`. Invalid material fails the next operation instead of retaining stale
authority. Previous CA trust has an explicit deadline, limited to 24 hours.

Servers verify client identity on every authenticated request, including reused
connections. Clients replace their connection pool when material or active trust
changes. In-flight requests retain their caller's deadline.
