package comparator

import (
	"fmt"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

// extractIndex derives the DPAPI index from an operation's Priority().
// This uses the known priority formula: create/update = base + index, delete = base + (999 - index).
func extractIndex(t *testing.T, op Operation) int {
	t.Helper()
	base := sections.PriorityRule * sections.PriorityMultiplier
	if op.Type() == sections.OperationDelete {
		return base + 999 - op.Priority()
	}
	return op.Priority() - base
}

func TestCompare_HTTPRequestRuleInsertNoCascade(t *testing.T) {
	currentConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request deny if { path_beg /admin }
    http-request redirect scheme https if !{ ssl_fc }
    http-request set-header X-Forwarded-Proto https
    default_backend default_be

backend default_be
    server srv1 127.0.0.1:8080
`

	desiredConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request deny if { path_beg /admin }
    http-request deny if { path_beg /secret }
    http-request redirect scheme https if !{ ssl_fc }
    http-request set-header X-Forwarded-Proto https
    default_backend default_be

backend default_be
    server srv1 127.0.0.1:8080
`

	current, desired := parseTestConfigs(t, currentConfig, desiredConfig)
	comp := New()
	diff, err := comp.Compare(current, desired)
	require.NoError(t, err)

	// Count operation types for http_request_rule
	var creates, updates, deletes int
	for _, op := range diff.Operations {
		if op.Section() != "http_request_rule" {
			continue
		}
		switch op.Type() {
		case sections.OperationCreate:
			creates++
		case sections.OperationUpdate:
			updates++
		case sections.OperationDelete:
			deletes++
		}
	}

	assert.Equal(t, 1, creates, "should have exactly 1 CREATE for the inserted rule")
	assert.Equal(t, 0, updates, "should have 0 UPDATEs (no cascade)")
	assert.Equal(t, 0, deletes, "should have 0 DELETEs")
}

func TestCompare_HTTPRequestRuleDeleteNoCascade(t *testing.T) {
	currentConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request deny if { path_beg /admin }
    http-request deny if { path_beg /secret }
    http-request redirect scheme https if !{ ssl_fc }
    http-request set-header X-Forwarded-Proto https
    default_backend default_be

backend default_be
    server srv1 127.0.0.1:8080
`

	desiredConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request deny if { path_beg /admin }
    http-request redirect scheme https if !{ ssl_fc }
    http-request set-header X-Forwarded-Proto https
    default_backend default_be

backend default_be
    server srv1 127.0.0.1:8080
`

	current, desired := parseTestConfigs(t, currentConfig, desiredConfig)
	comp := New()
	diff, err := comp.Compare(current, desired)
	require.NoError(t, err)

	var creates, updates, deletes int
	for _, op := range diff.Operations {
		if op.Section() != "http_request_rule" {
			continue
		}
		switch op.Type() {
		case sections.OperationCreate:
			creates++
		case sections.OperationUpdate:
			updates++
		case sections.OperationDelete:
			deletes++
		}
	}

	assert.Equal(t, 0, creates, "should have 0 CREATEs")
	assert.Equal(t, 0, updates, "should have 0 UPDATEs (no cascade)")
	assert.Equal(t, 1, deletes, "should have exactly 1 DELETE for the removed rule")
}

func TestCompare_SingleBackendAddedFewOperations(t *testing.T) {
	currentConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request deny if { path_beg /admin }
    http-request redirect scheme https if !{ ssl_fc }
    use_backend api_be if { path_beg /api }
    default_backend default_be

backend default_be
    http-request set-header X-Backend default
    server srv1 127.0.0.1:8080

backend api_be
    http-request set-header X-Backend api
    http-request set-header X-Request-ID %[unique-id]
    server srv1 127.0.0.1:8081
`

	// Add a new backend with several http-request rules and a use_backend rule
	desiredConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request deny if { path_beg /admin }
    http-request redirect scheme https if !{ ssl_fc }
    use_backend api_be if { path_beg /api }
    use_backend new_be if { path_beg /new }
    default_backend default_be

backend default_be
    http-request set-header X-Backend default
    server srv1 127.0.0.1:8080

backend api_be
    http-request set-header X-Backend api
    http-request set-header X-Request-ID %[unique-id]
    server srv1 127.0.0.1:8081

backend new_be
    http-request set-header X-Backend new
    http-request set-header X-Request-ID %[unique-id]
    http-request deny if { path_beg /blocked }
    server srv1 127.0.0.1:8082
`

	current, desired := parseTestConfigs(t, currentConfig, desiredConfig)
	comp := New()
	diff, err := comp.Compare(current, desired)
	require.NoError(t, err)

	totalOps := len(diff.Operations)
	t.Logf("Total operations: %d", totalOps)
	for i, op := range diff.Operations {
		t.Logf("  %d: %v %s - %s", i, op.Type(), op.Section(), op.Describe())
	}

	// Adding one backend with 3 http-request rules, 1 server, 1 backend, and 1 use_backend
	// should produce a small number of operations (well under 10)
	assert.Less(t, totalOps, 10, "single backend addition should produce fewer than 10 total operations")

	// Verify no cascade UPDATEs for existing rules
	var ruleUpdates int
	for _, op := range diff.Operations {
		if op.Section() == "http_request_rule" && op.Type() == sections.OperationUpdate {
			ruleUpdates++
		}
	}
	assert.Equal(t, 0, ruleUpdates, "existing http-request rules should not have cascade UPDATEs")
}

func TestCompare_HTTPRequestRuleContentChange(t *testing.T) {
	currentConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request deny if { path_beg /admin }
    http-request redirect scheme https if !{ ssl_fc }
    http-request set-header X-Forwarded-Proto https
    default_backend default_be

backend default_be
    server srv1 127.0.0.1:8080
`

	// Change action of the first rule from deny to allow
	desiredConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http
    bind :80
    http-request allow if { path_beg /admin }
    http-request redirect scheme https if !{ ssl_fc }
    http-request set-header X-Forwarded-Proto https
    default_backend default_be

backend default_be
    server srv1 127.0.0.1:8080
`

	current, desired := parseTestConfigs(t, currentConfig, desiredConfig)
	comp := New()
	diff, err := comp.Compare(current, desired)
	require.NoError(t, err)

	var creates, updates, deletes int
	for _, op := range diff.Operations {
		if op.Section() != "http_request_rule" {
			continue
		}
		switch op.Type() {
		case sections.OperationCreate:
			creates++
		case sections.OperationUpdate:
			updates++
		case sections.OperationDelete:
			deletes++
		}
	}

	assert.Equal(t, 0, creates, "should have 0 CREATEs")
	assert.Equal(t, 1, updates, "should have exactly 1 UPDATE for the changed rule")
	assert.Equal(t, 0, deletes, "should have 0 DELETEs")
}

// --- Index Verification Tests ---

func TestCompare_HTTPRequestRuleInsertIndex(t *testing.T) {
	// Insert a rule at position 1 (between two existing rules).
	// The CREATE operation should target desired-config index 1.
	comp := New()
	ops := comp.compareHTTPRequestRules(
		"frontend", "http",
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "deny", CondTest: "{ path_beg /secret }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
	)

	require.Len(t, ops, 1, "should produce exactly 1 operation")
	assert.Equal(t, sections.OperationCreate, ops[0].Type())
	assert.Equal(t, 1, extractIndex(t, ops[0]), "CREATE should target desired-config index 1")
}

func TestCompare_HTTPRequestRuleDeleteIndex(t *testing.T) {
	// Delete the rule at position 1 (middle of three rules).
	// The DELETE operation should target current-config index 1.
	comp := New()
	ops := comp.compareHTTPRequestRules(
		"frontend", "http",
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "deny", CondTest: "{ path_beg /secret }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
	)

	require.Len(t, ops, 1, "should produce exactly 1 operation")
	assert.Equal(t, sections.OperationDelete, ops[0].Type())
	assert.Equal(t, 1, extractIndex(t, ops[0]), "DELETE should target current-config index 1")
}

func TestCompare_HTTPRequestRuleUpdateIndex(t *testing.T) {
	// Change the rule at position 1 (content change, not a shift).
	// The UPDATE operation should target old-config index 1.
	comp := New()
	ops := comp.compareHTTPRequestRules(
		"frontend", "http",
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "deny", CondTest: "{ path_beg /secret }", DenyStatus: ptrInt64Fn(403)},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "deny", CondTest: "{ path_beg /secret }", DenyStatus: ptrInt64Fn(404)},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
	)

	require.Len(t, ops, 1, "should produce exactly 1 operation")
	assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	assert.Equal(t, 1, extractIndex(t, ops[0]), "UPDATE should target old-config index 1")
}

func TestCompare_HTTPRequestRuleMultipleInsertIndexes(t *testing.T) {
	// Insert rules at two different positions. Verify both indexes.
	comp := New()
	ops := comp.compareHTTPRequestRules(
		"frontend", "http",
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "deny", CondTest: "{ path_beg /secret }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
			{Type: "set-header", HdrName: "X-Test", HdrFormat: "1"},
		},
	)

	require.Len(t, ops, 2, "should produce exactly 2 operations")

	// Both should be CREATEs
	for _, op := range ops {
		assert.Equal(t, sections.OperationCreate, op.Type())
	}

	// Collect indexes
	indexes := make([]int, len(ops))
	for i, op := range ops {
		indexes[i] = extractIndex(t, op)
	}
	assert.Contains(t, indexes, 1, "should have CREATE at index 1")
	assert.Contains(t, indexes, 3, "should have CREATE at index 3")
}

func TestCompare_HTTPRequestRuleDeleteAndInsertIndexes(t *testing.T) {
	// Delete at position 0 and insert at position 2 (in desired).
	comp := New()
	ops := comp.compareHTTPRequestRules(
		"frontend", "http",
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /old }"},
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
		},
		models.HTTPRequestRules{
			{Type: "deny", CondTest: "{ path_beg /admin }"},
			{Type: "redirect", RedirCode: ptrInt64Fn(301)},
			{Type: "set-header", HdrName: "X-New", HdrFormat: "1"},
		},
	)

	require.Len(t, ops, 2, "should produce exactly 2 operations")

	var deleteOp, createOp Operation
	for _, op := range ops {
		switch op.Type() {
		case sections.OperationDelete:
			deleteOp = op
		case sections.OperationCreate:
			createOp = op
		}
	}

	require.NotNil(t, deleteOp, "should have a DELETE operation")
	require.NotNil(t, createOp, "should have a CREATE operation")
	assert.Equal(t, 0, extractIndex(t, deleteOp), "DELETE should target current-config index 0")
	assert.Equal(t, 2, extractIndex(t, createOp), "CREATE should target desired-config index 2")
}

// ptrInt64Fn is a helper to create *int64 pointers in test data.
//
//go:fix inline
func ptrInt64Fn(v int64) *int64 { return new(v) }

// --- Cascade Elimination Tests for All 8 Rule Types ---

func TestCompare_CascadeEliminationAllRuleTypes(t *testing.T) {
	comp := New()

	t.Run("HTTP request rules", func(t *testing.T) {
		current := makeHTTPRequestRules(10)
		desired := insertHTTPRequestRule(current, 5)
		ops := comp.compareHTTPRequestRules("frontend", "http", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	t.Run("HTTP response rules", func(t *testing.T) {
		current := makeHTTPResponseRules(10)
		desired := insertHTTPResponseRule(current, 5)
		ops := comp.compareHTTPResponseRules("frontend", "http", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	t.Run("TCP request rules", func(t *testing.T) {
		current := makeTCPRequestRules(10)
		desired := insertTCPRequestRule(current, 5)
		ops := comp.compareTCPRequestRules("frontend", "http", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	t.Run("TCP response rules", func(t *testing.T) {
		current := makeTCPResponseRules(10)
		desired := insertTCPResponseRule(current, 5)
		ops := comp.compareTCPResponseRules("api-backend", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	t.Run("stick rules", func(t *testing.T) {
		current := makeStickRules(10)
		desired := insertStickRule(current, 5)
		ops := comp.compareStickRules("api-backend", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	t.Run("HTTP after-response rules", func(t *testing.T) {
		current := makeHTTPAfterResponseRules(10)
		desired := insertHTTPAfterResponseRule(current, 5)
		ops := comp.compareHTTPAfterResponseRules("api-backend", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	t.Run("backend switching rules", func(t *testing.T) {
		current := makeBackendSwitchingRules(10)
		desired := insertBackendSwitchingRule(current, 5)
		ops := comp.compareBackendSwitchingRules("http", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	t.Run("server switching rules", func(t *testing.T) {
		current := makeServerSwitchingRules(10)
		desired := insertServerSwitchingRule(current, 5)
		ops := comp.compareServerSwitchingRules("api-backend", current, desired)
		assertNoCascade(t, ops, 1, 0)
	})

	// Also test deletions to verify no cascade in the other direction
	t.Run("HTTP request rules delete", func(t *testing.T) {
		current := makeHTTPRequestRules(10)
		desired := deleteRule(current, 5)
		ops := comp.compareHTTPRequestRules("frontend", "http", current, desired)
		assertNoCascade(t, ops, 0, 1)
	})

	t.Run("backend switching rules delete", func(t *testing.T) {
		current := makeBackendSwitchingRules(10)
		desired := deleteRule(current, 5)
		ops := comp.compareBackendSwitchingRules("http", current, desired)
		assertNoCascade(t, ops, 0, 1)
	})
}

func assertNoCascade(t *testing.T, ops []Operation, wantCreates, wantDeletes int) {
	t.Helper()
	var creates, updates, deletes int
	for _, op := range ops {
		switch op.Type() {
		case sections.OperationCreate:
			creates++
		case sections.OperationUpdate:
			updates++
		case sections.OperationDelete:
			deletes++
		}
	}
	assert.Equal(t, wantCreates, creates, "CREATE count")
	assert.Equal(t, 0, updates, "UPDATE count (no cascade)")
	assert.Equal(t, wantDeletes, deletes, "DELETE count")
}

// deleteRule removes the element at position pos from a slice, returning a new slice.
func deleteRule[T any](rules []T, pos int) []T {
	dst := make([]T, 0, len(rules)-1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, rules[pos+1:]...)
	return dst
}

// --- Rule slice builders for cascade tests ---

func makeHTTPRequestRules(n int) models.HTTPRequestRules {
	rules := make(models.HTTPRequestRules, n)
	for i := range n {
		rules[i] = &models.HTTPRequestRule{Type: "set-header", HdrName: fmt.Sprintf("X-Rule-%d", i), HdrFormat: fmt.Sprintf("val%d", i)}
	}
	return rules
}

func insertHTTPRequestRule(rules models.HTTPRequestRules, pos int) models.HTTPRequestRules {
	dst := make(models.HTTPRequestRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.HTTPRequestRule{Type: "deny", CondTest: "{ path_beg /new }"})
	dst = append(dst, rules[pos:]...)
	return dst
}

func makeHTTPResponseRules(n int) models.HTTPResponseRules {
	rules := make(models.HTTPResponseRules, n)
	for i := range n {
		rules[i] = &models.HTTPResponseRule{Type: "set-header", HdrName: fmt.Sprintf("X-Resp-%d", i), HdrFormat: fmt.Sprintf("val%d", i)}
	}
	return rules
}

func insertHTTPResponseRule(rules models.HTTPResponseRules, pos int) models.HTTPResponseRules {
	dst := make(models.HTTPResponseRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.HTTPResponseRule{Type: "set-header", HdrName: "X-New", HdrFormat: "new"})
	dst = append(dst, rules[pos:]...)
	return dst
}

func makeTCPRequestRules(n int) models.TCPRequestRules {
	rules := make(models.TCPRequestRules, n)
	for i := range n {
		rules[i] = &models.TCPRequestRule{Type: "inspect-delay", Timeout: new(int64(1000 + i))}
	}
	return rules
}

func insertTCPRequestRule(rules models.TCPRequestRules, pos int) models.TCPRequestRules {
	dst := make(models.TCPRequestRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.TCPRequestRule{Type: "inspect-delay", Timeout: ptrInt64Fn(9999)})
	dst = append(dst, rules[pos:]...)
	return dst
}

func makeTCPResponseRules(n int) models.TCPResponseRules {
	rules := make(models.TCPResponseRules, n)
	for i := range n {
		rules[i] = &models.TCPResponseRule{Type: "inspect-delay", Timeout: new(int64(2000 + i))}
	}
	return rules
}

func insertTCPResponseRule(rules models.TCPResponseRules, pos int) models.TCPResponseRules {
	dst := make(models.TCPResponseRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.TCPResponseRule{Type: "inspect-delay", Timeout: ptrInt64Fn(9999)})
	dst = append(dst, rules[pos:]...)
	return dst
}

func makeStickRules(n int) models.StickRules {
	rules := make(models.StickRules, n)
	for i := range n {
		rules[i] = &models.StickRule{Type: "store-request", Pattern: fmt.Sprintf("src%d", i)}
	}
	return rules
}

func insertStickRule(rules models.StickRules, pos int) models.StickRules {
	dst := make(models.StickRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.StickRule{Type: "store-request", Pattern: "new-src"})
	dst = append(dst, rules[pos:]...)
	return dst
}

func makeHTTPAfterResponseRules(n int) models.HTTPAfterResponseRules {
	rules := make(models.HTTPAfterResponseRules, n)
	for i := range n {
		rules[i] = &models.HTTPAfterResponseRule{Type: "set-header", HdrName: fmt.Sprintf("X-After-%d", i), HdrFormat: fmt.Sprintf("val%d", i)}
	}
	return rules
}

func insertHTTPAfterResponseRule(rules models.HTTPAfterResponseRules, pos int) models.HTTPAfterResponseRules {
	dst := make(models.HTTPAfterResponseRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.HTTPAfterResponseRule{Type: "set-header", HdrName: "X-New", HdrFormat: "new"})
	dst = append(dst, rules[pos:]...)
	return dst
}

func makeBackendSwitchingRules(n int) models.BackendSwitchingRules {
	rules := make(models.BackendSwitchingRules, n)
	for i := range n {
		rules[i] = &models.BackendSwitchingRule{Name: fmt.Sprintf("be_%d", i), Cond: "if", CondTest: fmt.Sprintf("acl_%d", i)}
	}
	return rules
}

func insertBackendSwitchingRule(rules models.BackendSwitchingRules, pos int) models.BackendSwitchingRules {
	dst := make(models.BackendSwitchingRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.BackendSwitchingRule{Name: "new_be", Cond: "if", CondTest: "new_acl"})
	dst = append(dst, rules[pos:]...)
	return dst
}

func makeServerSwitchingRules(n int) models.ServerSwitchingRules {
	rules := make(models.ServerSwitchingRules, n)
	for i := range n {
		rules[i] = &models.ServerSwitchingRule{TargetServer: fmt.Sprintf("srv_%d", i), Cond: "if", CondTest: fmt.Sprintf("acl_%d", i)}
	}
	return rules
}

func insertServerSwitchingRule(rules models.ServerSwitchingRules, pos int) models.ServerSwitchingRules {
	dst := make(models.ServerSwitchingRules, 0, len(rules)+1)
	dst = append(dst, rules[:pos]...)
	dst = append(dst, &models.ServerSwitchingRule{TargetServer: "new_srv", Cond: "if", CondTest: "new_acl"})
	dst = append(dst, rules[pos:]...)
	return dst
}
