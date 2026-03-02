## 1. Myers Diff Algorithm

- [x] 1.1 Implement generic `diffIndexedRules[T]` function in `pkg/dataplane/comparator/lcs.go` using Myers diff algorithm, accepting an equality function `func(T, T) bool` and returning a slice of diff entries (keep/insert/delete with old and new indexes)
- [x] 1.2 Write table-driven unit tests for `diffIndexedRules` covering: identical sequences, empty sequences, single insert, single delete, single update (same position different content), insert at start/middle/end, delete at start/middle/end, mixed insert+delete, and large sequences with small edit distance

## 2. Index Translation

- [x] 2.1 Implement index translation function that converts diff entries into Operations using the correct DPAPI indexes: DELETE at current-config index, INSERT at desired-config index, UPDATE at current index for content changes at the same LCS position
- [x] 2.2 Write table-driven tests for index translation covering: single insert shifts, single delete shifts, mixed insert+delete with correct cumulative offset, insert at position 0, delete at last position, and cascade elimination (100 rules with 1 insert produces 1 CREATE not 99 UPDATEs)

## 3. Rule Type Integration

- [x] 3.1 Replace index-based loop in `compareHTTPRequestRules` with LCS-based comparison using the generic diff function and existing `createHTTPRequestRuleOperation`/`deleteHTTPRequestRuleOperation`/`updateHTTPRequestRuleOperation` factory functions
- [x] 3.2 Replace index-based loop in `compareHTTPResponseRules` with LCS-based comparison
- [x] 3.3 Replace index-based loops in remaining 6 rule types (TCP request, TCP response, stick rules, HTTP after-response, backend switching, server switching) with LCS-based comparison
- [x] 3.4 Remove the `safeGet*` helper functions if they become unused after the refactor

## 4. Integration Tests

- [x] 4.1 Write integration test in `pkg/dataplane/comparator/` that compares two full StructuredConfigs differing by one backend's HTTP request rules and verifies the ConfigDiff contains only INSERT/DELETE operations (no cascade UPDATEs)
- [x] 4.2 Write integration test verifying that a single-Ingress-equivalent change (one backend added with ~6 HTTP request rules) produces fewer than 10 total operations across all rule types
- [x] 4.3 Run existing comparator tests to verify no regressions in non-indexed-rule comparison (ACLs, servers, frontends, backends, global, defaults)

## 5. Verification

- [x] 5.1 Run `make lint` and fix any issues
- [x] 5.2 Run `make test` and verify all tests pass
