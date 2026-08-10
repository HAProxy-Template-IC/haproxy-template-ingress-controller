package comparator

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

func TestBackendBaseDiffFields_Equal(t *testing.T) {
	b1 := &models.Backend{
		BackendBase: models.BackendBase{
			Name: "test",
			Mode: "http",
		},
	}
	b2 := &models.Backend{
		BackendBase: models.BackendBase{
			Name: "test",
			Mode: "http",
		},
	}

	fields := backendBaseDiffFields(b1, b2)
	assert.Nil(t, fields)
}

func TestBackendBaseDiffFields_ModeDiffers(t *testing.T) {
	b1 := &models.Backend{
		BackendBase: models.BackendBase{
			Name: "test",
			Mode: "http",
		},
	}
	b2 := &models.Backend{
		BackendBase: models.BackendBase{
			Name: "test",
			Mode: "tcp",
		},
	}

	fields := backendBaseDiffFields(b1, b2)
	assert.NotNil(t, fields)
	assert.Contains(t, fields, "Mode")
}

func TestBackendBaseDiffFields_IgnoresNestedCollections(t *testing.T) {
	b1 := &models.Backend{
		BackendBase: models.BackendBase{
			Name: "test",
			Mode: "http",
		},
		Servers: map[string]models.Server{
			"srv1": {},
		},
	}
	b2 := &models.Backend{
		BackendBase: models.BackendBase{
			Name: "test",
			Mode: "http",
		},
		Servers: map[string]models.Server{
			"srv2": {},
		},
	}

	// Servers differ but should be ignored (nested collections cleared)
	fields := backendBaseDiffFields(b1, b2)
	assert.Nil(t, fields)
}

func TestBackendBaseDiffFields_MultipleFields(t *testing.T) {
	balance := &models.Balance{Algorithm: new(string)}
	b1 := &models.Backend{
		BackendBase: models.BackendBase{
			Name:    "test",
			Mode:    "http",
			Balance: balance,
		},
	}
	b2 := &models.Backend{
		BackendBase: models.BackendBase{
			Name: "test",
			Mode: "tcp",
		},
	}

	fields := backendBaseDiffFields(b1, b2)
	assert.NotNil(t, fields)
	assert.Contains(t, fields, "Mode")
	// Balance is a nested struct; the diff key includes the subfield path.
	assert.GreaterOrEqual(t, len(fields), 2, "expected at least 2 diff fields, got %v", fields)
}

// http_error_rule_list lives on Backend, not BackendBase, and has no comparator
// of its own. Gating the backend update on backendBaseDiffFields meant Diff named
// nothing, the field list came back empty, and the change was silently dropped —
// the backend was never deployed.
func TestBackendsEqual_HTTPErrorRuleOnlyChangeIsNotEqual(t *testing.T) {
	current := &models.Backend{
		BackendBase: models.BackendBase{Name: "be1", Mode: "http"},
	}
	desired := &models.Backend{
		BackendBase: models.BackendBase{Name: "be1", Mode: "http"},
		HTTPErrorRuleList: models.HTTPErrorRules{
			{Type: "status", Status: 503},
		},
	}

	assert.Empty(t, backendBaseDiffFields(current, desired),
		"premise: BackendBase.Diff cannot name this field")
	assert.False(t, backendsEqualWithoutNestedCollections(current, desired),
		"an http-error-only change must still be treated as a backend update")
}

func TestBackendBaseDiffFields_PopulatesSummary(t *testing.T) {
	summary := NewDiffSummary()

	current := &models.Backend{
		BackendBase: models.BackendBase{Name: "be1", Mode: "http"},
	}
	desired := &models.Backend{
		BackendBase: models.BackendBase{Name: "be1", Mode: "tcp"},
	}

	diffFields := backendBaseDiffFields(current, desired)
	if len(diffFields) > 0 {
		summary.BackendDiffFields["be1"] = diffFields
	}

	assert.Contains(t, summary.BackendDiffFields, "be1")
	assert.Contains(t, summary.BackendDiffFields["be1"], "Mode")
}
