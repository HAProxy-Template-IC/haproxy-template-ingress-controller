## Context

The comparator in `pkg/dataplane/comparator/compare_rules.go` compares 8 indexed rule types (HTTP request, HTTP response, TCP request, TCP response, stick rules, HTTP after response, backend switching, server switching) using index-based positional comparison. When rules shift due to an insertion or deletion, all subsequent positions are counted as UPDATE operations, causing a cascade of thousands of phantom changes that exceed the raw push threshold.

The DataPlane API uses index-based operations:

- CREATE at index N inserts and shifts subsequent rules down (verified in dataplaneapi source: `config-parser/parsers/http/http-request_generated.go:Insert()`)
- DELETE at index N removes and shifts subsequent rules up
- UPDATE (PUT) at index N replaces in-place

The existing priority system already handles execution ordering: deletes run highest-index-first, creates run lowest-index-first.

## Goals / Non-Goals

**Goals:**

- Eliminate cascade phantom UPDATEs when rules shift due to insertion/deletion
- Produce correct INSERT (CREATE) and DELETE operations with accurate DPAPI indexes
- Apply to all 8 indexed rule types via a single generic implementation
- Maintain backward compatibility with the existing Operation interface and execution path

**Non-Goals:**

- Changing the raw push threshold logic or `TotalOperations()` calculation
- Changing the Operation interface, factory functions, or execution path
- Optimizing ACL comparison (already name-based)
- Handling rule reordering (rules that move position but don't change content are treated as delete+create)

## Decisions

### Decision 1: Use Myers diff algorithm (Go's `slices` or similar)

**Choice:** Myers diff (the algorithm behind `git diff`) operating on content-equality of rule models.

**Alternatives considered:**

- **Naive LCS via dynamic programming**: O(n*m) time and space. With ~11k rules, that's ~120M cells — too expensive.
- **Patience diff**: Better for text but no advantage for structured data.
- **Myers diff**: O(n*d) where d = edit distance. When d is small (typical case: 1-10 changes among 11k rules), this is nearly linear. Worst case is still O(n*m) but only triggers when configs are radically different — which would hit the raw push threshold anyway.

**Rationale:** Myers is the standard choice for ordered sequence diff. The edit distance `d` is small in the typical case (a few Ingresses changed), making it fast. For pathological cases where `d` is large, the raw push threshold would trigger before the diff completes enough iterations to matter.

### Decision 2: Generic implementation across all 8 rule types

**Choice:** A single generic `diffIndexedRules[T]` function that accepts an equality function and produces abstract diff entries (keep/insert/delete). Each rule-type-specific comparison function wraps this with its own factory calls.

**Rationale:** All 8 rule types follow the identical pattern: index-based comparison with create/update/delete operations. Extracting the diff algorithm into a generic function eliminates duplication and ensures consistent behavior. The type-specific wrappers handle only the factory function dispatch (frontend vs backend, rule-type-specific constructors).

### Decision 3: Index translation via running offset

**Choice:** After computing the LCS diff, translate abstract positions to DPAPI indexes using a running offset that tracks cumulative shifts from prior operations.

The algorithm:

1. Compute diff entries: KEEP, INSERT, DELETE
2. Walk the diff entries in order, maintaining `currentIdx` (position in current config)
3. For DELETE: emit operation at `currentIdx`, advance `currentIdx`
4. For INSERT: emit operation at `desiredIdx` (the target position in the final config)
5. For KEEP: advance both indexes, no operation
6. The priority system already sorts deletes highest-first and creates lowest-first for correct execution

**Rationale:** This is straightforward and correct. The priority system in `IndexChildOp.Priority()` already handles execution ordering, so we just need to emit operations with the right target indexes.

### Decision 4: Content equality via `Equal()` method

**Choice:** Use the existing `Equal()` method on client-native models (e.g., `HTTPRequestRule.Equal()`) for content comparison in the diff algorithm.

**Rationale:** These methods are already used by the current index-based comparator to detect updates. Reusing them ensures the diff algorithm considers the same fields as the current implementation — no behavioral change in what constitutes a "modified" rule.

## Risks / Trade-offs

**[Risk] Myers diff performance on large rule sets** → For the typical case (d=1-10 edits among 11k rules), Myers runs in O(n) time. For worst case (d=n), it degrades to O(n^2), but this only occurs when configs are radically different, which would trigger raw push anyway. Additionally, we can add an early-out: if the edit distance exceeds the raw push threshold, abort the diff and let the orchestrator fall back to raw push.

**[Risk] Subtle index calculation bugs** → The index translation logic must correctly account for the cumulative effect of prior inserts and deletes. This is mitigated by comprehensive table-driven tests covering: insert-only, delete-only, mixed insert+delete, insert at start/middle/end, delete at start/middle/end, and large cascades.

**[Trade-off] Rule reordering treated as delete+create** → If the template reorders rules without changing their content, LCS treats this as deletions at old positions and insertions at new positions. This produces more operations than strictly necessary, but rule reordering is rare in practice (template output is deterministic for a given set of Ingresses) and correctness is more important than optimality.
