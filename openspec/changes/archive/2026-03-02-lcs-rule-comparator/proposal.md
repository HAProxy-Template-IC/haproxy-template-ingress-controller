## Why

The dataplane comparator uses index-based comparison for HTTP request/response rules. When a single Ingress is added or removed, all subsequent rule indexes shift, causing ~11,000 phantom UPDATE operations. This exceeds the raw push threshold (100), forcing a full config push every reconciliation cycle. DPAPI serialization instability then keeps the system in a perpetual raw push loop, spiking dataplane CPU from 400m to 1700m.

## What Changes

- Replace index-based positional comparison with LCS (Longest Common Subsequence) content matching for all indexed rule types (http-request, http-response, tcp-request, tcp-response)
- Produce INSERT (CREATE at index) and DELETE operations instead of cascading UPDATEs when rules shift
- Translate LCS diff positions to correct DPAPI indexes accounting for cumulative shifts from prior operations within the same transaction
- A single-Ingress change drops from ~11k operations to ~1-5 operations, staying well below the raw push threshold

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `dataplane-sync`: The "Fine-Grained Configuration Comparison" requirement changes from index-based positional comparison to LCS-based content matching for indexed rule types. Operation counts for rule insertions/deletions drop by orders of magnitude. The existing Operation interface, factory functions, priority system, and execution path remain unchanged.

## Impact

- `pkg/dataplane/comparator/compare_rules.go` — core comparison logic for indexed rules
- `pkg/dataplane/comparator/` — new LCS algorithm, index translation, and tests
- No changes to the Operation interface, execution path, DPAPI client, or orchestrator threshold logic
- No template or chart changes
