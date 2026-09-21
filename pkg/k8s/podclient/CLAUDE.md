# Pod access

This utility reaches pod-local HTTP endpoints and executes commands through the
Kubernetes API. It has no controller, HAProxy, or watched-resource knowledge.
HTTP forwarding binds only to loopback. Callers choose response limits and must
not publish raw command output as a diagnostic report.
