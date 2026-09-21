# Controller diagnostics

This utility joins existing controller, agent, and Kubernetes observations for
the `doctor` command. It doesn't change configuration or participate in
reconciliation.

Reports use explicit field lists. Never copy configuration, Secret data, rendered
files, event details, error messages, or arbitrary maps into a report or bundle.
Watched resources are discovered from configuration; Go must not recognize
routing kinds or assume their status layout.

Report missing permissions, unavailable endpoints, and truncated collection as
incomplete evidence. An unavailable observation must never become a healthy one.
