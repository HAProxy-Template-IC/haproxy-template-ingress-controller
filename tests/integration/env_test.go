//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestGenerateSafeNamespaceName(t *testing.T) {
	tests := []struct {
		name      string
		testName  string
		maxLength int
	}{
		{
			name:      "short test name",
			testName:  "TestSimple",
			maxLength: maxK8sNameLength,
		},
		{
			name:      "medium test name with subtest",
			testName:  "TestSync/add-backend",
			maxLength: maxK8sNameLength,
		},
		{
			name:      "long test name that needs truncation",
			testName:  "TestSync/backend-add-http-response-rule",
			maxLength: maxK8sNameLength,
		},
		{
			name:      "very long test name",
			testName:  "TestVeryLongTestName/with-a-very-long-subtest-name-that-definitely-exceeds-limits",
			maxLength: maxK8sNameLength,
		},
		{
			name:      "test name with multiple slashes",
			testName:  "TestParent/TestChild/TestGrandchild/backend-add-acl",
			maxLength: maxK8sNameLength,
		},
		{
			name:      "test name with uppercase and special chars",
			testName:  "TestCamelCase/Add-Backend-With-HTTP",
			maxLength: maxK8sNameLength,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateSafeNamespaceName(tt.testName)

			// Verify length constraint
			if len(result) > tt.maxLength {
				t.Errorf("Generated namespace name '%s' exceeds max length %d (actual: %d)",
					result, tt.maxLength, len(result))
			}

			// Verify RFC 1123 compliance: lowercase alphanumeric with hyphens
			if !isValidK8sName(result) {
				t.Errorf("Generated namespace name '%s' is not RFC 1123 compliant", result)
			}

			// Verify it starts with expected prefix
			if !strings.HasPrefix(result, "test-") {
				t.Errorf("Generated namespace name '%s' doesn't start with 'test-'", result)
			}

			// Verify uniqueness: generate multiple names for same test
			// They should be different due to timestamp in hash
			result2 := generateSafeNamespaceName(tt.testName)
			if result == result2 {
				t.Logf("Note: Generated names are identical (this is rare but possible): %s", result)
			}

			t.Logf("Test name: %s -> Namespace: %s (length: %d)",
				tt.testName, result, len(result))
		})
	}
}

// isValidK8sName checks if a name follows RFC 1123 label requirements:
// - lowercase alphanumeric characters or '-'
// - start with alphanumeric character
// - end with alphanumeric character.
func isValidK8sName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}

	// Check first character (must be alphanumeric)
	first := name[0]
	if !isNameAlphanumeric(rune(first)) {
		return false
	}

	// Check last character (must be alphanumeric)
	last := name[len(name)-1]
	if !isNameAlphanumeric(rune(last)) {
		return false
	}

	// Check all characters (must be alphanumeric or hyphen)
	for _, ch := range name {
		if !isNameAlphanumeric(ch) && ch != '-' {
			return false
		}
	}

	return true
}

func TestIsValidK8sName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"valid simple", "test", true},
		{"valid with hyphen", "test-name", true},
		{"valid with number", "test123", true},
		{"invalid uppercase", "TestName", false},
		{"invalid starts with hyphen", "-test", false},
		{"invalid ends with hyphen", "test-", false},
		{"invalid underscore", "test_name", false},
		{"invalid dot", "test.name", false},
		{"invalid slash", "test/name", false},
		{"invalid too long", strings.Repeat("a", 64), false},
		{"valid max length", strings.Repeat("a", 63), true},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidK8sName(tt.input)
			if result != tt.valid {
				t.Errorf("isValidK8sName(%q) = %v, want %v", tt.input, result, tt.valid)
			}
		})
	}
}

func isNameAlphanumeric(ch rune) bool { return ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' }
