# Input isolation

This is a pure coordination service, called through the reconciliation pipeline interface.
It selects resource revisions; it never changes templates or weakens their validation.
Every selected state must pass the complete render and output validation pipeline.
Keep aliases of one Kubernetes object in the same atomic change group.
Cancellation and failed baselines cannot authorize acceptance. An unavailable lazy
revision rejects its trial; every selected alternative must pass full validation.
