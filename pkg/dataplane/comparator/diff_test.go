package comparator

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDiffSummary(t *testing.T) {
	summary := NewDiffSummary()

	assert.NotNil(t, summary.ServersAdded)
	assert.NotNil(t, summary.ServersModified)
	assert.NotNil(t, summary.ServersDeleted)
	assert.NotNil(t, summary.OtherChanges)
	assert.Equal(t, 0, summary.TotalCreates)
	assert.Equal(t, 0, summary.TotalUpdates)
	assert.Equal(t, 0, summary.TotalDeletes)
}

func TestDiffSummary_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		summary  DiffSummary
		expected bool
	}{
		{
			name:     "no changes",
			summary:  DiffSummary{},
			expected: false,
		},
		{
			name:     "has creates",
			summary:  DiffSummary{TotalCreates: 1},
			expected: true,
		},
		{
			name:     "has updates",
			summary:  DiffSummary{TotalUpdates: 1},
			expected: true,
		},
		{
			name:     "has deletes",
			summary:  DiffSummary{TotalDeletes: 1},
			expected: true,
		},
		{
			name:     "has all",
			summary:  DiffSummary{TotalCreates: 1, TotalUpdates: 2, TotalDeletes: 3},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.summary.HasChanges())
		})
	}
}

func TestDiffSummary_TotalOperations(t *testing.T) {
	tests := []struct {
		name     string
		summary  DiffSummary
		expected int
	}{
		{
			name:     "no operations",
			summary:  DiffSummary{},
			expected: 0,
		},
		{
			name:     "creates only",
			summary:  DiffSummary{TotalCreates: 5},
			expected: 5,
		},
		{
			name:     "mixed operations",
			summary:  DiffSummary{TotalCreates: 2, TotalUpdates: 3, TotalDeletes: 4},
			expected: 9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.summary.TotalOperations())
		})
	}
}

// This method should exclude server UPDATE operations (runtime-eligible) from the count.
func TestDiffSummary_StructuralOperations(t *testing.T) {
	tests := []struct {
		name     string
		summary  DiffSummary
		expected int
	}{
		{
			name:     "no operations",
			summary:  DiffSummary{},
			expected: 0,
		},
		{
			name:     "creates only - all structural",
			summary:  DiffSummary{TotalCreates: 5},
			expected: 5,
		},
		{
			name:     "deletes only - all structural",
			summary:  DiffSummary{TotalDeletes: 3},
			expected: 3,
		},
		{
			name: "server modifications excluded",
			summary: DiffSummary{
				TotalUpdates: 10, // 10 updates total
				ServersModified: map[string][]string{
					"backend1": {"srv1", "srv2", "srv3"}, // 3 server mods
					"backend2": {"srv4", "srv5"},         // 2 server mods
				},
			},
			expected: 5, // 10 - 5 server mods = 5 structural
		},
		{
			name: "only server modifications - zero structural",
			summary: DiffSummary{
				TotalUpdates: 100, // 100 updates total
				ServersModified: map[string][]string{
					"backend1": make([]string, 50), // 50 server mods
					"backend2": make([]string, 50), // 50 server mods
				},
			},
			expected: 0, // 100 - 100 server mods = 0 structural
		},
		{
			name: "mixed operations with server modifications",
			summary: DiffSummary{
				TotalCreates: 10,  // 10 creates (structural)
				TotalUpdates: 150, // 150 updates (partially server mods)
				TotalDeletes: 5,   // 5 deletes (structural)
				ServersModified: map[string][]string{
					"backend1": make([]string, 100), // 100 server mods
				},
			},
			expected: 65, // 10 + (150-100) + 5 = 65 structural
		},
		{
			name: "server adds and deletes are structural",
			summary: DiffSummary{
				TotalCreates: 5,
				TotalDeletes: 3,
				ServersAdded: map[string][]string{
					"backend1": {"srv1", "srv2"}, // counted in TotalCreates
				},
				ServersDeleted: map[string][]string{
					"backend1": {"srv3"}, // counted in TotalDeletes
				},
				ServersModified: map[string][]string{}, // empty - no modifications
			},
			expected: 8, // creates + deletes = structural (server add/delete require reload)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.summary.StructuralOperations())
		})
	}
}

func TestDiffSummary_String(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		summary := DiffSummary{}
		assert.Equal(t, "No changes", summary.String())
	})

	t.Run("basic changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates: 2,
			TotalUpdates: 1,
			TotalDeletes: 0,
		}
		str := summary.String()
		assert.Contains(t, str, "Total: 3 operations")
		assert.Contains(t, str, "2 creates")
		assert.Contains(t, str, "1 updates")
	})

	t.Run("global changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalUpdates:  1,
			GlobalChanged: true,
		}
		str := summary.String()
		assert.Contains(t, str, "Global settings modified")
	})

	t.Run("defaults changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalUpdates:    1,
			DefaultsChanged: true,
		}
		str := summary.String()
		assert.Contains(t, str, "Defaults modified")
	})

	t.Run("frontend changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates:      2,
			TotalDeletes:      1,
			FrontendsAdded:    []string{"http", "https"},
			FrontendsDeleted:  []string{"old-frontend"},
			FrontendsModified: []string{"main"},
		}
		str := summary.String()
		assert.Contains(t, str, "Frontends added: http, https")
		assert.Contains(t, str, "Frontends deleted: old-frontend")
	})

	t.Run("backend changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates:     1,
			BackendsAdded:    []string{"api"},
			BackendsModified: []string{"web"},
		}
		str := summary.String()
		assert.Contains(t, str, "Backends added: api")
	})

	t.Run("server changes", func(t *testing.T) {
		summary := DiffSummary{
			TotalCreates: 2,
			ServersAdded: map[string][]string{
				"backend1": {"srv1", "srv2"},
			},
			ServersModified: make(map[string][]string),
			ServersDeleted:  make(map[string][]string),
		}
		str := summary.String()
		assert.Contains(t, str, "Servers added")
		assert.Contains(t, str, "backend1: 2")
	})
}
