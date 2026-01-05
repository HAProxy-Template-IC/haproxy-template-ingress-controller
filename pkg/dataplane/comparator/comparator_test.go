package comparator

import (
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// TestCompare_UserlistWithHTTPAuthRule tests that when a backend references a userlist
// via http_auth, both the userlist creation and the http_request_rule creation are detected.
//
// This reproduces the bug where fine-grained sync fails because the userlist is not
// created in the transaction before the http_request_rule that references it.
func TestCompare_UserlistWithHTTPAuthRule(t *testing.T) {
	currentConfig := testConfigWithoutAuth()
	desiredConfig := testConfigWithAuth()

	current, desired := parseTestConfigs(t, currentConfig, desiredConfig)

	// Run comparator
	comp := New()
	diff, err := comp.Compare(current, desired)
	if err != nil {
		t.Fatalf("Compare() failed: %v", err)
	}

	// Verify operations were generated
	verifyMinimumOperations(t, diff.Operations, 2)
	verifyUserlistCreationExists(t, diff.Operations)
	verifyOperationOrdering(t, diff.Operations)
}

func testConfigWithoutAuth() string {
	return `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

backend test_backend
    server srv1 127.0.0.1:8080
`
}

func testConfigWithAuth() string {
	return `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

userlist auth_users
    user admin password $6$hash1
    user user password $6$hash2

backend test_backend
    http-request auth realm "Protected" unless { http_auth(auth_users) }
    server srv1 127.0.0.1:8080
`
}

func parseTestConfigs(t *testing.T, currentConfig, desiredConfig string) (current, desired *parser.StructuredConfig) {
	t.Helper()
	p, err := parser.New()
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}

	current, err = p.ParseFromString(currentConfig)
	if err != nil {
		t.Fatalf("Failed to parse current config: %v", err)
	}

	desired, err = p.ParseFromString(desiredConfig)
	if err != nil {
		t.Fatalf("Failed to parse desired config: %v", err)
	}

	return
}

func verifyMinimumOperations(t *testing.T, operations []Operation, minCount int) {
	t.Helper()
	if len(operations) == 0 {
		t.Fatal("Expected operations to be generated, got none")
	}

	if len(operations) < minCount {
		t.Errorf("Expected at least %d operations, got %d", minCount, len(operations))
		logOperations(t, operations)
	}
}

func verifyUserlistCreationExists(t *testing.T, operations []Operation) {
	t.Helper()
	for _, op := range operations {
		if op.Type() == sections.OperationCreate && op.Section() == "userlist" {
			return
		}
	}

	t.Error("Expected CREATE userlist operation, but it was not found")
	t.Log("Operations generated:")
	logOperations(t, operations)
}

func verifyOperationOrdering(t *testing.T, operations []Operation) {
	t.Helper()
	userlistIdx := findOperationIndex(operations, "userlist")
	httpRuleIdx := findOperationIndex(operations, "backend", "http_request_rule")

	if userlistIdx != -1 && httpRuleIdx != -1 && userlistIdx > httpRuleIdx {
		t.Errorf("Userlist operation (index %d) should come before http rule operation (index %d)", userlistIdx, httpRuleIdx)
	}
}

func findOperationIndex(operations []Operation, sectionNames ...string) int {
	for i, op := range operations {
		for _, section := range sectionNames {
			if op.Section() == section {
				return i
			}
		}
	}
	return -1
}

func logOperations(t *testing.T, operations []Operation) {
	t.Helper()
	for i, op := range operations {
		t.Logf("  %d: %v %s - %s (priority: %d)", i, op.Type(), op.Section(), op.Describe(), op.Priority())
	}
}

// TestCompare_ExistingUserlistNoChange tests that when both current and desired configs
// have the same userlist, no operations are generated for it.
func TestCompare_ExistingUserlistNoChange(t *testing.T) {
	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

userlist auth_users
    user admin password $6$hash1

backend test_backend
    http-request auth realm "Protected" unless { http_auth(auth_users) }
    server srv1 127.0.0.1:8080
`

	// Parse both configs (identical)
	p, err := parser.New()
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(config)
	if err != nil {
		t.Fatalf("Failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(config)
	if err != nil {
		t.Fatalf("Failed to parse desired config: %v", err)
	}

	// Run comparator
	comp := New()
	diff, err := comp.Compare(current, desired)
	if err != nil {
		t.Fatalf("Compare() failed: %v", err)
	}

	// Should have no operations for identical configs
	if len(diff.Operations) != 0 {
		t.Errorf("Expected no operations for identical configs, got %d", len(diff.Operations))
		for i, op := range diff.Operations {
			t.Logf("Operation %d: %v %s - %s", i, op.Type(), op.Section(), op.Describe())
		}
	}
}

// TestCompare_UserlistPriority tests that userlist operations have correct priority.
func TestCompare_UserlistPriority(t *testing.T) {
	currentConfig := `
global
    daemon

defaults
    mode http
`

	desiredConfig := `
global
    daemon

defaults
    mode http

userlist auth_users
    user admin password $6$hash1

frontend test_frontend
    bind :80

backend test_backend
    server srv1 127.0.0.1:8080
`

	// Parse configs
	p, err := parser.New()
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(currentConfig)
	if err != nil {
		t.Fatalf("Failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(desiredConfig)
	if err != nil {
		t.Fatalf("Failed to parse desired config: %v", err)
	}

	// Run comparator
	comp := New()
	diff, err := comp.Compare(current, desired)
	if err != nil {
		t.Fatalf("Compare() failed: %v", err)
	}

	// Find userlist operation
	for _, op := range diff.Operations {
		if op.Section() == "userlist" {
			// Userlist priority should be 10 * PriorityMultiplier (same as other foundational sections)
			expectedPriority := 10 * sections.PriorityMultiplier
			if op.Priority() != expectedPriority {
				t.Errorf("Expected userlist priority to be %d, got %d", expectedPriority, op.Priority())
			}
			return
		}
	}

	t.Error("No userlist operation found in diff")
}

// TestCompare_BackendHTTPRequestRuleOrderPreservation tests that http-request rules
// maintain their order when comparing configs.
func TestCompare_BackendHTTPRequestRuleOrderPreservation(t *testing.T) {
	currentConfig := `
global
    daemon

defaults
    mode http

userlist auth_users
    user admin password $6$hash1

backend test_backend
    server srv1 127.0.0.1:8080
`

	desiredConfig := `
global
    daemon

defaults
    mode http

userlist auth_users
    user admin password $6$hash1

backend test_backend
    http-request auth realm "Protected" unless { http_auth(auth_users) }
    server srv1 127.0.0.1:8080
`

	// Parse configs
	p, err := parser.New()
	if err != nil {
		t.Fatalf("Failed to create parser: %v", err)
	}

	current, err := p.ParseFromString(currentConfig)
	if err != nil {
		t.Fatalf("Failed to parse current config: %v", err)
	}

	desired, err := p.ParseFromString(desiredConfig)
	if err != nil {
		t.Fatalf("Failed to parse desired config: %v", err)
	}

	// Verify desired config has the http_request_rule
	if len(desired.Backends) != 1 {
		t.Fatalf("Expected 1 backend in desired config, got %d", len(desired.Backends))
	}

	backend := desired.Backends[0]
	if len(backend.HTTPRequestRuleList) == 0 {
		t.Fatal("Expected http_request_rule in desired backend, got none")
	}

	rule := backend.HTTPRequestRuleList[0]
	if rule.Type != "auth" {
		t.Errorf("Expected rule type 'auth', got %q", rule.Type)
	}

	// Run comparator
	comp := New()
	diff, err := comp.Compare(current, desired)
	if err != nil {
		t.Fatalf("Compare() failed: %v", err)
	}

	// Should have operation for the backend (with the new rule) or just the rule
	if len(diff.Operations) == 0 {
		t.Fatal("Expected at least one operation for adding http_request_rule")
	}

	// Verify no userlist operations (it already exists in both)
	for _, op := range diff.Operations {
		if op.Section() == "userlist" {
			t.Errorf("Unexpected userlist operation: %s", op.Describe())
		}
	}
}

// TestCompare_UserlistModification tests userlist update detection.
// This test verifies that user changes within a userlist generate fine-grained
// user operations (CreateUser, ReplaceUser) rather than recreating the entire userlist.
func TestCompare_UserlistModification(t *testing.T) {
	currentConfig := testUserlistConfigSingleUser()
	desiredConfig := testUserlistConfigModifiedUsers()

	current, desired := parseTestConfigs(t, currentConfig, desiredConfig)

	// Run comparator
	comp := New()
	diff, err := comp.Compare(current, desired)
	if err != nil {
		t.Fatalf("Compare() failed: %v", err)
	}

	// Verify operations
	verifyFineGrainedUserOperations(t, diff.Operations)
}

func testUserlistConfigSingleUser() string {
	return `
global
    daemon

defaults
    mode http

userlist auth_users
    user admin password $6$hash1
`
}

func testUserlistConfigModifiedUsers() string {
	return `
global
    daemon

defaults
    mode http

userlist auth_users
    user admin password $6$newhash
    user newuser password $6$hash3
`
}

func verifyFineGrainedUserOperations(t *testing.T, operations []Operation) {
	t.Helper()

	if len(operations) == 0 {
		t.Fatal("Expected operations for userlist modification")
	}

	hasCreateUser, hasReplaceUser, hasUserlistOps := analyzeUserOperations(operations)

	if !hasCreateUser {
		t.Error("Expected CreateUser operation for new user")
		logOperations(t, operations)
	}

	if !hasReplaceUser {
		t.Error("Expected ReplaceUser operation for password change")
		logOperations(t, operations)
	}

	if hasUserlistOps {
		t.Error("Did not expect userlist-level operations, only user-level operations")
		logOperations(t, operations)
	}
}

func analyzeUserOperations(operations []Operation) (hasCreateUser, hasReplaceUser, hasUserlistOps bool) {
	for _, op := range operations {
		if op.Section() == "user" {
			if op.Type() == sections.OperationCreate {
				hasCreateUser = true
			}
			if op.Type() == sections.OperationUpdate {
				hasReplaceUser = true
			}
		}
		if op.Section() == "userlist" {
			hasUserlistOps = true
		}
	}
	return
}

// userlistUserOperationsTestCase defines a test case for userlist user operations.
type userlistUserOperationsTestCase struct {
	name              string
	currentConfig     string
	desiredConfig     string
	expectCreateUser  []string // usernames that should be created
	expectReplaceUser []string // usernames that should be replaced
	expectDeleteUser  []string // usernames that should be deleted
	expectUserlistOps bool     // whether userlist-level operations are expected
}

// TestCompare_UserlistUserOperations tests fine-grained user operations within userlists.
// This test verifies that when users are added, modified, or removed from a userlist,
// the comparator generates appropriate CreateUser, ReplaceUser, and DeleteUser operations
// rather than recreating the entire userlist.
func TestCompare_UserlistUserOperations(t *testing.T) {
	tests := []userlistUserOperationsTestCase{
		{
			name: "add new user to existing userlist",
			currentConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$hash1
`,
			desiredConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$hash1
    user newuser password $6$hash2
`,
			expectCreateUser:  []string{"newuser"},
			expectReplaceUser: nil,
			expectDeleteUser:  nil,
			expectUserlistOps: false, // userlist itself doesn't change
		},
		{
			name: "remove user from existing userlist",
			currentConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$hash1
    user olduser password $6$hash2
`,
			desiredConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$hash1
`,
			expectCreateUser:  nil,
			expectReplaceUser: nil,
			expectDeleteUser:  []string{"olduser"},
			expectUserlistOps: false,
		},
		{
			name: "modify user password",
			currentConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$oldhash
`,
			desiredConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$newhash
`,
			expectCreateUser:  nil,
			expectReplaceUser: []string{"admin"},
			expectDeleteUser:  nil,
			expectUserlistOps: false,
		},
		{
			name: "multiple user changes",
			currentConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$hash1
    user olduser password $6$hash2
`,
			desiredConfig: `
global
    daemon
defaults
    mode http
userlist auth_users
    user admin password $6$newhash
    user newuser password $6$hash3
`,
			expectCreateUser:  []string{"newuser"},
			expectReplaceUser: []string{"admin"},
			expectDeleteUser:  []string{"olduser"},
			expectUserlistOps: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current, desired := parseTestConfigs(t, tt.currentConfig, tt.desiredConfig)

			comp := New()
			diff, err := comp.Compare(current, desired)
			if err != nil {
				t.Fatalf("Compare() failed: %v", err)
			}

			verifyUserOperationsDetailed(t, diff.Operations, &tt)
		})
	}
}

func verifyUserOperationsDetailed(t *testing.T, operations []Operation, tt *userlistUserOperationsTestCase) {
	t.Helper()

	createUsers, replaceUsers, deleteUsers, hasUserlistOps := collectUserOpsFromDescriptions(operations, tt)

	verifyUserCreates(t, operations, createUsers, tt.expectCreateUser)
	verifyUserReplaces(t, replaceUsers, tt.expectReplaceUser)
	verifyUserDeletes(t, deleteUsers, tt.expectDeleteUser)
	verifyUserlistOpsMatch(t, operations, hasUserlistOps, tt.expectUserlistOps)
}

func collectUserOpsFromDescriptions(operations []Operation, tt *userlistUserOperationsTestCase) (createUsers, replaceUsers, deleteUsers map[string]bool, hasUserlistOps bool) {
	createUsers = make(map[string]bool)
	replaceUsers = make(map[string]bool)
	deleteUsers = make(map[string]bool)

	for _, op := range operations {
		if op.Section() == "user" {
			desc := op.Describe()
			matchUsersByType(op.Type(), desc, createUsers, replaceUsers, deleteUsers, tt)
		} else if op.Section() == "userlist" {
			hasUserlistOps = true
		}
	}

	return
}

func matchUsersByType(opType sections.OperationType, desc string, createUsers, replaceUsers, deleteUsers map[string]bool, tt *userlistUserOperationsTestCase) {
	switch opType {
	case sections.OperationCreate:
		for _, username := range tt.expectCreateUser {
			if stringContains(desc, username) {
				createUsers[username] = true
			}
		}
	case sections.OperationUpdate:
		for _, username := range tt.expectReplaceUser {
			if stringContains(desc, username) {
				replaceUsers[username] = true
			}
		}
	case sections.OperationDelete:
		for _, username := range tt.expectDeleteUser {
			if stringContains(desc, username) {
				deleteUsers[username] = true
			}
		}
	}
}

func verifyUserCreates(t *testing.T, operations []Operation, createUsers map[string]bool, expected []string) {
	t.Helper()
	for _, username := range expected {
		if !createUsers[username] {
			t.Errorf("Expected CreateUser operation for %q, but not found", username)
			logOperations(t, operations)
		}
	}
}

func verifyUserReplaces(t *testing.T, replaceUsers map[string]bool, expected []string) {
	t.Helper()
	for _, username := range expected {
		if !replaceUsers[username] {
			t.Errorf("Expected ReplaceUser operation for %q, but not found", username)
		}
	}
}

func verifyUserDeletes(t *testing.T, deleteUsers map[string]bool, expected []string) {
	t.Helper()
	for _, username := range expected {
		if !deleteUsers[username] {
			t.Errorf("Expected DeleteUser operation for %q, but not found", username)
		}
	}
}

func verifyUserlistOpsMatch(t *testing.T, operations []Operation, hasUserlistOps, expectUserlistOps bool) {
	t.Helper()
	if hasUserlistOps != expectUserlistOps {
		if expectUserlistOps {
			t.Error("Expected userlist-level operations, but none found")
		} else {
			t.Error("Did not expect userlist-level operations, but found some")
			logOperations(t, operations)
		}
	}
}

// stringContains is a helper function for checking if a string contains a substring.
func stringContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
