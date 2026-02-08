package comparator

import (
	"context"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

func TestCompareMapEntries(t *testing.T) {
	type entry struct {
		Name  string
		Value int
	}

	createOp := func(e *entry) Operation {
		return &testOp{opType: sections.OperationCreate, section: "entry", desc: "Create " + e.Name}
	}
	deleteOp := func(e *entry) Operation {
		return &testOp{opType: sections.OperationDelete, section: "entry", desc: "Delete " + e.Name}
	}
	updateOp := func(e *entry) Operation {
		return &testOp{opType: sections.OperationUpdate, section: "entry", desc: "Update " + e.Name}
	}
	equalFunc := func(e1, e2 *entry) bool {
		return e1.Name == e2.Name && e1.Value == e2.Value
	}

	t.Run("both maps nil", func(t *testing.T) {
		ops := compareMapEntries[entry](nil, nil, createOp, deleteOp, updateOp, equalFunc)
		assert.Empty(t, ops)
	})

	t.Run("current nil desired has entries", func(t *testing.T) {
		desired := map[string]entry{
			"a": {Name: "a", Value: 1},
			"b": {Name: "b", Value: 2},
		}
		ops := compareMapEntries(nil, desired, createOp, deleteOp, updateOp, equalFunc)
		require.Len(t, ops, 2)
		// Both should be creates
		for _, op := range ops {
			assert.Equal(t, sections.OperationCreate, op.Type())
		}
	})

	t.Run("current has entries desired nil", func(t *testing.T) {
		current := map[string]entry{
			"a": {Name: "a", Value: 1},
		}
		ops := compareMapEntries(current, nil, createOp, deleteOp, updateOp, equalFunc)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("add entry", func(t *testing.T) {
		current := map[string]entry{
			"a": {Name: "a", Value: 1},
		}
		desired := map[string]entry{
			"a": {Name: "a", Value: 1},
			"b": {Name: "b", Value: 2},
		}
		ops := compareMapEntries(current, desired, createOp, deleteOp, updateOp, equalFunc)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Contains(t, ops[0].Describe(), "b")
	})

	t.Run("delete entry", func(t *testing.T) {
		current := map[string]entry{
			"a": {Name: "a", Value: 1},
			"b": {Name: "b", Value: 2},
		}
		desired := map[string]entry{
			"a": {Name: "a", Value: 1},
		}
		ops := compareMapEntries(current, desired, createOp, deleteOp, updateOp, equalFunc)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
		assert.Contains(t, ops[0].Describe(), "b")
	})

	t.Run("update entry", func(t *testing.T) {
		current := map[string]entry{
			"a": {Name: "a", Value: 1},
		}
		desired := map[string]entry{
			"a": {Name: "a", Value: 2},
		}
		ops := compareMapEntries(current, desired, createOp, deleteOp, updateOp, equalFunc)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
		assert.Contains(t, ops[0].Describe(), "a")
	})

	t.Run("no changes", func(t *testing.T) {
		current := map[string]entry{
			"a": {Name: "a", Value: 1},
		}
		desired := map[string]entry{
			"a": {Name: "a", Value: 1},
		}
		ops := compareMapEntries(current, desired, createOp, deleteOp, updateOp, equalFunc)
		assert.Empty(t, ops)
	})

	t.Run("multiple operations", func(t *testing.T) {
		current := map[string]entry{
			"a": {Name: "a", Value: 1},
			"b": {Name: "b", Value: 2},
		}
		desired := map[string]entry{
			"a": {Name: "a", Value: 10}, // update
			"c": {Name: "c", Value: 3},  // create
		}
		ops := compareMapEntries(current, desired, createOp, deleteOp, updateOp, equalFunc)
		require.Len(t, ops, 3) // 1 update, 1 create, 1 delete

		creates := 0
		updates := 0
		deletes := 0
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
		assert.Equal(t, 1, creates)
		assert.Equal(t, 1, updates)
		assert.Equal(t, 1, deletes)
	})
}

// testOp for testing.
type testOp struct {
	opType   sections.OperationType
	section  string
	priority int
	desc     string
}

func (m *testOp) Type() sections.OperationType { return m.opType }
func (m *testOp) Section() string              { return m.section }
func (m *testOp) Priority() int                { return m.priority }
func (m *testOp) Describe() string             { return m.desc }
func (m *testOp) Execute(_ context.Context, _ *client.DataplaneClient, _ string) error {
	return nil
}

// Helper functions for test pointers.
func ptrStr(s string) *string    { return &s }
func ptrInt64(i int64) *int64    { return &i }
func ptrString(s string) *string { return &s }

func TestCompareHTTPErrors(t *testing.T) {
	comp := New()

	t.Run("add http-errors section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{},
		}
		desired := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{
				{Name: "my-errors"},
			},
		}

		ops := comp.compareHTTPErrors(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Equal(t, "http_errors", ops[0].Section())
	})

	t.Run("delete http-errors section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{
				{Name: "old-errors"},
			},
		}
		desired := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{},
		}

		ops := comp.compareHTTPErrors(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update http-errors section", func(t *testing.T) {
		// Create two different http-errors sections
		current := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{
				{Name: "my-errors"},
			},
		}
		// Modify the section - add error files
		desired := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{
				{
					Name: "my-errors",
					ErrorFiles: []*models.Errorfile{
						{Code: 500, File: "/etc/haproxy/errors/500.http"},
					},
				},
			},
		}

		ops := comp.compareHTTPErrors(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		section := &models.HTTPErrorsSection{Name: "my-errors"}
		current := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{section},
		}
		desired := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{section},
		}

		ops := comp.compareHTTPErrors(current, desired)
		assert.Empty(t, ops)
	})

	t.Run("empty name ignored", func(t *testing.T) {
		current := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{
				{Name: ""},
			},
		}
		desired := &parser.StructuredConfig{
			HTTPErrors: []*models.HTTPErrorsSection{
				{Name: ""},
			},
		}

		ops := comp.compareHTTPErrors(current, desired)
		assert.Empty(t, ops)
	})
}

func TestHTTPErrorsEqual(t *testing.T) {
	t.Run("equal sections", func(t *testing.T) {
		h1 := &models.HTTPErrorsSection{Name: "errors"}
		h2 := &models.HTTPErrorsSection{Name: "errors"}
		assert.True(t, httpErrorsEqual(h1, h2))
	})

	t.Run("different names", func(t *testing.T) {
		h1 := &models.HTTPErrorsSection{Name: "errors1"}
		h2 := &models.HTTPErrorsSection{Name: "errors2"}
		assert.False(t, httpErrorsEqual(h1, h2))
	})
}

func TestCompareMailers(t *testing.T) {
	comp := New()

	t.Run("add mailers section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{},
		}
		desired := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{
				{MailersSectionBase: models.MailersSectionBase{Name: "my-mailers"}},
			},
		}

		ops := comp.compareMailers(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Equal(t, "mailers", ops[0].Section())
	})

	t.Run("delete mailers section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{
				{MailersSectionBase: models.MailersSectionBase{Name: "old-mailers"}},
			},
		}
		desired := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{},
		}

		ops := comp.compareMailers(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("add mailer entry to existing section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{
				{
					MailersSectionBase: models.MailersSectionBase{Name: "my-mailers"},
				},
			},
			MailerEntryIndex: map[string]map[string]*models.MailerEntry{
				"my-mailers": {},
			},
		}
		smtp1 := &models.MailerEntry{Name: "smtp1", Address: "mail.example.com", Port: 25}
		desired := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{
				{
					MailersSectionBase: models.MailersSectionBase{Name: "my-mailers"},
				},
			},
			MailerEntryIndex: map[string]map[string]*models.MailerEntry{
				"my-mailers": {
					"smtp1": smtp1,
				},
			},
		}

		ops := comp.compareMailers(current, desired)
		require.NotEmpty(t, ops)

		// Should have a mailer entry create operation
		hasMailerEntryCreate := false
		for _, op := range ops {
			if op.Section() == "mailer_entry" && op.Type() == sections.OperationCreate {
				hasMailerEntryCreate = true
			}
		}
		assert.True(t, hasMailerEntryCreate, "Expected mailer entry create operation")
	})

	t.Run("no changes", func(t *testing.T) {
		section := &models.MailersSection{
			MailersSectionBase: models.MailersSectionBase{Name: "my-mailers"},
			MailerEntries:      map[string]models.MailerEntry{},
		}
		current := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{section},
		}
		desired := &parser.StructuredConfig{
			Mailers: []*models.MailersSection{section},
		}

		ops := comp.compareMailers(current, desired)
		assert.Empty(t, ops)
	})
}

func TestMailersEqualWithoutMailerEntries(t *testing.T) {
	t.Run("equal sections with different entries", func(t *testing.T) {
		m1 := &models.MailersSection{
			MailersSectionBase: models.MailersSectionBase{Name: "mailers", Timeout: ptrInt64(1000)},
			MailerEntries: map[string]models.MailerEntry{
				"a": {Name: "a"},
			},
		}
		m2 := &models.MailersSection{
			MailersSectionBase: models.MailersSectionBase{Name: "mailers", Timeout: ptrInt64(1000)},
			MailerEntries: map[string]models.MailerEntry{
				"b": {Name: "b"},
			},
		}
		// Should be equal because we ignore mailer entries
		assert.True(t, mailersEqualWithoutMailerEntries(m1, m2))
	})

	t.Run("different timeout", func(t *testing.T) {
		m1 := &models.MailersSection{
			MailersSectionBase: models.MailersSectionBase{Name: "mailers", Timeout: ptrInt64(1000)},
		}
		m2 := &models.MailersSection{
			MailersSectionBase: models.MailersSectionBase{Name: "mailers", Timeout: ptrInt64(2000)},
		}
		assert.False(t, mailersEqualWithoutMailerEntries(m1, m2))
	})
}

func TestMailerEntriesEqual(t *testing.T) {
	t.Run("equal entries", func(t *testing.T) {
		e1 := &models.MailerEntry{Name: "smtp", Address: "mail.example.com", Port: 25}
		e2 := &models.MailerEntry{Name: "smtp", Address: "mail.example.com", Port: 25}
		assert.True(t, e1.Equal(*e2))
	})

	t.Run("different address", func(t *testing.T) {
		e1 := &models.MailerEntry{Name: "smtp", Address: "mail1.example.com"}
		e2 := &models.MailerEntry{Name: "smtp", Address: "mail2.example.com"}
		assert.False(t, e1.Equal(*e2))
	})
}

func TestComparePeers(t *testing.T) {
	comp := New()

	t.Run("add peer section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Peers: []*models.PeerSection{},
		}
		desired := &parser.StructuredConfig{
			Peers: []*models.PeerSection{
				{PeerSectionBase: models.PeerSectionBase{Name: "my-peers"}},
			},
		}

		ops := comp.comparePeers(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Equal(t, "peers", ops[0].Section())
	})

	t.Run("delete peer section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Peers: []*models.PeerSection{
				{PeerSectionBase: models.PeerSectionBase{Name: "old-peers"}},
			},
		}
		desired := &parser.StructuredConfig{
			Peers: []*models.PeerSection{},
		}

		ops := comp.comparePeers(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("add peer entry to existing section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Peers: []*models.PeerSection{
				{
					PeerSectionBase: models.PeerSectionBase{Name: "my-peers"},
				},
			},
			PeerEntryIndex: map[string]map[string]*models.PeerEntry{
				"my-peers": {},
			},
		}
		peer1 := &models.PeerEntry{Name: "peer1", Address: ptrStr("192.168.1.1"), Port: ptrInt64(10000)}
		desired := &parser.StructuredConfig{
			Peers: []*models.PeerSection{
				{
					PeerSectionBase: models.PeerSectionBase{Name: "my-peers"},
				},
			},
			PeerEntryIndex: map[string]map[string]*models.PeerEntry{
				"my-peers": {
					"peer1": peer1,
				},
			},
		}

		ops := comp.comparePeers(current, desired)
		require.NotEmpty(t, ops)

		hasPeerEntryCreate := false
		for _, op := range ops {
			if op.Section() == "peer_entry" && op.Type() == sections.OperationCreate {
				hasPeerEntryCreate = true
			}
		}
		assert.True(t, hasPeerEntryCreate, "Expected peer entry create operation")
	})
}

func TestPeersEqualWithoutPeerEntries(t *testing.T) {
	t.Run("equal sections with different entries", func(t *testing.T) {
		p1 := &models.PeerSection{
			PeerSectionBase: models.PeerSectionBase{Name: "peers"},
			PeerEntries: map[string]models.PeerEntry{
				"a": {Name: "a"},
			},
		}
		p2 := &models.PeerSection{
			PeerSectionBase: models.PeerSectionBase{Name: "peers"},
			PeerEntries: map[string]models.PeerEntry{
				"b": {Name: "b"},
			},
		}
		assert.True(t, peersEqualWithoutPeerEntries(p1, p2))
	})

	t.Run("different names", func(t *testing.T) {
		p1 := &models.PeerSection{
			PeerSectionBase: models.PeerSectionBase{Name: "peers1"},
		}
		p2 := &models.PeerSection{
			PeerSectionBase: models.PeerSectionBase{Name: "peers2"},
		}
		assert.False(t, peersEqualWithoutPeerEntries(p1, p2))
	})
}

func TestPeerEntriesEqual(t *testing.T) {
	t.Run("equal entries", func(t *testing.T) {
		e1 := &models.PeerEntry{Name: "peer1", Address: ptrStr("192.168.1.1"), Port: ptrInt64(10000)}
		e2 := &models.PeerEntry{Name: "peer1", Address: ptrStr("192.168.1.1"), Port: ptrInt64(10000)}
		assert.True(t, e1.Equal(*e2))
	})

	t.Run("different port", func(t *testing.T) {
		e1 := &models.PeerEntry{Name: "peer1", Port: ptrInt64(10000)}
		e2 := &models.PeerEntry{Name: "peer1", Port: ptrInt64(10001)}
		assert.False(t, e1.Equal(*e2))
	})
}

func TestCompareCaches(t *testing.T) {
	comp := New()

	t.Run("add cache section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Caches: []*models.Cache{},
		}
		desired := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: ptrStr("my-cache")},
			},
		}

		ops := comp.compareCaches(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Equal(t, "cache", ops[0].Section())
	})

	t.Run("delete cache section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: ptrStr("old-cache")},
			},
		}
		desired := &parser.StructuredConfig{
			Caches: []*models.Cache{},
		}

		ops := comp.compareCaches(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update cache section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: ptrStr("my-cache"), MaxAge: 60},
			},
		}
		desired := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: ptrStr("my-cache"), MaxAge: 120},
			},
		}

		ops := comp.compareCaches(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("empty name ignored", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: ptrStr("")},
			},
		}
		desired := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: ptrStr("")},
			},
		}

		ops := comp.compareCaches(current, desired)
		assert.Empty(t, ops)
	})

	t.Run("nil name ignored", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: nil},
			},
		}
		desired := &parser.StructuredConfig{
			Caches: []*models.Cache{
				{Name: nil},
			},
		}

		ops := comp.compareCaches(current, desired)
		assert.Empty(t, ops)
	})
}

func TestCacheEqual(t *testing.T) {
	t.Run("equal caches", func(t *testing.T) {
		c1 := &models.Cache{Name: ptrStr("cache")}
		c2 := &models.Cache{Name: ptrStr("cache")}
		assert.True(t, cacheEqual(c1, c2))
	})

	t.Run("different names", func(t *testing.T) {
		c1 := &models.Cache{Name: ptrStr("cache1")}
		c2 := &models.Cache{Name: ptrStr("cache2")}
		assert.False(t, cacheEqual(c1, c2))
	})
}

func TestCompareRings(t *testing.T) {
	comp := New()

	t.Run("add ring section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Rings: []*models.Ring{},
		}
		desired := &parser.StructuredConfig{
			Rings: []*models.Ring{
				{RingBase: models.RingBase{Name: "my-ring"}},
			},
		}

		ops := comp.compareRings(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Equal(t, "ring", ops[0].Section())
	})

	t.Run("delete ring section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Rings: []*models.Ring{
				{RingBase: models.RingBase{Name: "old-ring"}},
			},
		}
		desired := &parser.StructuredConfig{
			Rings: []*models.Ring{},
		}

		ops := comp.compareRings(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update ring section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Rings: []*models.Ring{
				{RingBase: models.RingBase{Name: "my-ring", Size: ptrInt64(1024)}},
			},
		}
		desired := &parser.StructuredConfig{
			Rings: []*models.Ring{
				{RingBase: models.RingBase{Name: "my-ring", Size: ptrInt64(2048)}},
			},
		}

		ops := comp.compareRings(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})
}

func TestRingEqual(t *testing.T) {
	t.Run("equal rings", func(t *testing.T) {
		r1 := &models.Ring{RingBase: models.RingBase{Name: "ring"}}
		r2 := &models.Ring{RingBase: models.RingBase{Name: "ring"}}
		assert.True(t, ringEqual(r1, r2))
	})

	t.Run("different names", func(t *testing.T) {
		r1 := &models.Ring{RingBase: models.RingBase{Name: "ring1"}}
		r2 := &models.Ring{RingBase: models.RingBase{Name: "ring2"}}
		assert.False(t, ringEqual(r1, r2))
	})
}

func TestComparePrograms(t *testing.T) {
	comp := New()

	t.Run("add program section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Programs: []*models.Program{},
		}
		desired := &parser.StructuredConfig{
			Programs: []*models.Program{
				{Name: "my-program", Command: ptrStr("/usr/bin/test")},
			},
		}

		ops := comp.comparePrograms(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Equal(t, "program", ops[0].Section())
	})

	t.Run("delete program section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Programs: []*models.Program{
				{Name: "old-program"},
			},
		}
		desired := &parser.StructuredConfig{
			Programs: []*models.Program{},
		}

		ops := comp.comparePrograms(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update program section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Programs: []*models.Program{
				{Name: "my-program", Command: ptrStr("/usr/bin/old")},
			},
		}
		desired := &parser.StructuredConfig{
			Programs: []*models.Program{
				{Name: "my-program", Command: ptrStr("/usr/bin/new")},
			},
		}

		ops := comp.comparePrograms(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})
}

func TestProgramEqual(t *testing.T) {
	t.Run("equal programs", func(t *testing.T) {
		p1 := &models.Program{Name: "prog", Command: ptrStr("/usr/bin/test")}
		p2 := &models.Program{Name: "prog", Command: ptrStr("/usr/bin/test")}
		assert.True(t, programEqual(p1, p2))
	})

	t.Run("different commands", func(t *testing.T) {
		p1 := &models.Program{Name: "prog", Command: ptrStr("/usr/bin/old")}
		p2 := &models.Program{Name: "prog", Command: ptrStr("/usr/bin/new")}
		assert.False(t, programEqual(p1, p2))
	})
}

func TestCompareFCGIApps(t *testing.T) {
	comp := New()

	t.Run("add fcgi-app section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			FCGIApps: []*models.FCGIApp{},
		}
		desired := &parser.StructuredConfig{
			FCGIApps: []*models.FCGIApp{
				{FCGIAppBase: models.FCGIAppBase{Name: "my-fcgi"}},
			},
		}

		ops := comp.compareFCGIApps(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
		assert.Equal(t, "fcgi_app", ops[0].Section())
	})

	t.Run("delete fcgi-app section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			FCGIApps: []*models.FCGIApp{
				{FCGIAppBase: models.FCGIAppBase{Name: "old-fcgi"}},
			},
		}
		desired := &parser.StructuredConfig{
			FCGIApps: []*models.FCGIApp{},
		}

		ops := comp.compareFCGIApps(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update fcgi-app section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			FCGIApps: []*models.FCGIApp{
				{FCGIAppBase: models.FCGIAppBase{Name: "my-fcgi", Docroot: ptrStr("/var/www/old")}},
			},
		}
		desired := &parser.StructuredConfig{
			FCGIApps: []*models.FCGIApp{
				{FCGIAppBase: models.FCGIAppBase{Name: "my-fcgi", Docroot: ptrStr("/var/www/new")}},
			},
		}

		ops := comp.compareFCGIApps(current, desired)
		require.Len(t, ops, 1)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})
}

func TestFCGIAppEqual(t *testing.T) {
	t.Run("equal fcgi-apps", func(t *testing.T) {
		f1 := &models.FCGIApp{FCGIAppBase: models.FCGIAppBase{Name: "fcgi", Docroot: ptrStr("/var/www")}}
		f2 := &models.FCGIApp{FCGIAppBase: models.FCGIAppBase{Name: "fcgi", Docroot: ptrStr("/var/www")}}
		assert.True(t, fcgiAppEqual(f1, f2))
	})

	t.Run("different docroot", func(t *testing.T) {
		f1 := &models.FCGIApp{FCGIAppBase: models.FCGIAppBase{Name: "fcgi", Docroot: ptrStr("/var/www/old")}}
		f2 := &models.FCGIApp{FCGIAppBase: models.FCGIAppBase{Name: "fcgi", Docroot: ptrStr("/var/www/new")}}
		assert.False(t, fcgiAppEqual(f1, f2))
	})
}

func TestCompareGlobal(t *testing.T) {
	comp := New()

	t.Run("nil to non-nil global skips comparison", func(t *testing.T) {
		// The global section is a singleton - if either is nil, comparison is skipped
		current := &parser.StructuredConfig{
			Global: nil,
		}
		desired := &parser.StructuredConfig{
			Global: &models.Global{GlobalBase: models.GlobalBase{Daemon: true}},
		}
		summary := &DiffSummary{}
		ops := comp.compareGlobal(current, desired, summary)
		assert.Empty(t, ops, "Comparison skipped when current.Global is nil")
	})

	t.Run("non-nil to nil global skips comparison", func(t *testing.T) {
		// The global section is a singleton - if either is nil, comparison is skipped
		current := &parser.StructuredConfig{
			Global: &models.Global{GlobalBase: models.GlobalBase{Daemon: true}},
		}
		desired := &parser.StructuredConfig{
			Global: nil,
		}
		summary := &DiffSummary{}
		ops := comp.compareGlobal(current, desired, summary)
		assert.Empty(t, ops, "Comparison skipped when desired.Global is nil")
	})

	t.Run("different global values", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Global: &models.Global{GlobalBase: models.GlobalBase{Daemon: true}},
		}
		desired := &parser.StructuredConfig{
			Global: &models.Global{GlobalBase: models.GlobalBase{Daemon: false}},
		}
		summary := &DiffSummary{}
		ops := comp.compareGlobal(current, desired, summary)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
		assert.True(t, summary.GlobalChanged)
	})

	t.Run("no change", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Global: &models.Global{GlobalBase: models.GlobalBase{Daemon: true}},
		}
		desired := &parser.StructuredConfig{
			Global: &models.Global{GlobalBase: models.GlobalBase{Daemon: true}},
		}
		summary := &DiffSummary{}
		ops := comp.compareGlobal(current, desired, summary)
		assert.Empty(t, ops)
		assert.False(t, summary.GlobalChanged)
	})

	t.Run("both nil", func(t *testing.T) {
		current := &parser.StructuredConfig{Global: nil}
		desired := &parser.StructuredConfig{Global: nil}
		summary := &DiffSummary{}
		ops := comp.compareGlobal(current, desired, summary)
		assert.Empty(t, ops)
	})
}

func TestCompareDefaults(t *testing.T) {
	comp := New()

	t.Run("add new defaults section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Defaults: []*models.Defaults{},
		}
		desired := &parser.StructuredConfig{
			Defaults: []*models.Defaults{
				{DefaultsBase: models.DefaultsBase{Name: "http-defaults"}},
			},
		}
		summary := &DiffSummary{}
		ops := comp.compareDefaults(current, desired, summary)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate && op.Section() == "defaults" {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected defaults create operation")
	})

	t.Run("delete defaults section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Defaults: []*models.Defaults{
				{DefaultsBase: models.DefaultsBase{Name: "old-defaults"}},
			},
		}
		desired := &parser.StructuredConfig{
			Defaults: []*models.Defaults{},
		}
		summary := &DiffSummary{}
		ops := comp.compareDefaults(current, desired, summary)
		require.NotEmpty(t, ops)
		hasDelete := false
		for _, op := range ops {
			if op.Type() == sections.OperationDelete {
				hasDelete = true
				break
			}
		}
		assert.True(t, hasDelete, "Expected defaults delete operation")
	})

	t.Run("update defaults section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Defaults: []*models.Defaults{
				{DefaultsBase: models.DefaultsBase{Name: "my-defaults", Mode: "http"}},
			},
		}
		desired := &parser.StructuredConfig{
			Defaults: []*models.Defaults{
				{DefaultsBase: models.DefaultsBase{Name: "my-defaults", Mode: "tcp"}},
			},
		}
		summary := &DiffSummary{}
		ops := comp.compareDefaults(current, desired, summary)
		require.NotEmpty(t, ops)
	})

	t.Run("no changes", func(t *testing.T) {
		defaults := &models.Defaults{DefaultsBase: models.DefaultsBase{Name: "my-defaults"}}
		current := &parser.StructuredConfig{
			Defaults: []*models.Defaults{defaults},
		}
		desired := &parser.StructuredConfig{
			Defaults: []*models.Defaults{defaults},
		}
		summary := &DiffSummary{}
		ops := comp.compareDefaults(current, desired, summary)
		assert.Empty(t, ops)
	})

	t.Run("empty name section ignored", func(t *testing.T) {
		// Defaults sections with empty names are not tracked individually
		current := &parser.StructuredConfig{
			Defaults: []*models.Defaults{},
		}
		desired := &parser.StructuredConfig{
			Defaults: []*models.Defaults{
				{DefaultsBase: models.DefaultsBase{Name: "", Mode: "http"}},
			},
		}
		summary := &DiffSummary{}
		ops := comp.compareDefaults(current, desired, summary)
		assert.Empty(t, ops, "Defaults sections with empty names are ignored")
	})
}

func TestCompareFrontends(t *testing.T) {
	comp := New()

	t.Run("add new frontend", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Frontends: []*models.Frontend{},
		}
		desired := &parser.StructuredConfig{
			Frontends: []*models.Frontend{
				{FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: "http"}},
			},
		}
		summary := &DiffSummary{}
		ops := comp.compareFrontends(current, desired, summary)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate && op.Section() == "frontend" {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected frontend create operation")
	})

	t.Run("delete frontend", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Frontends: []*models.Frontend{
				{FrontendBase: models.FrontendBase{Name: "old-frontend"}},
			},
		}
		desired := &parser.StructuredConfig{
			Frontends: []*models.Frontend{},
		}
		summary := &DiffSummary{}
		ops := comp.compareFrontends(current, desired, summary)
		require.NotEmpty(t, ops)
		hasDelete := false
		for _, op := range ops {
			if op.Type() == sections.OperationDelete {
				hasDelete = true
				break
			}
		}
		assert.True(t, hasDelete, "Expected frontend delete operation")
	})
}

func TestCompareBinds(t *testing.T) {
	comp := New()

	t.Run("add new bind", func(t *testing.T) {
		ops := comp.compareBindsWithIndex(
			"http-frontend",
			map[string]*models.Bind{},
			map[string]*models.Bind{
				"http-bind": {BindParams: models.BindParams{Name: "http-bind"}},
			},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete bind", func(t *testing.T) {
		ops := comp.compareBindsWithIndex(
			"http-frontend",
			map[string]*models.Bind{
				"old-bind": {BindParams: models.BindParams{Name: "old-bind"}},
			},
			map[string]*models.Bind{},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update bind", func(t *testing.T) {
		ops := comp.compareBindsWithIndex(
			"http-frontend",
			map[string]*models.Bind{
				"http-bind": {BindParams: models.BindParams{Name: "http-bind"}, Port: ptrInt64(80)},
			},
			map[string]*models.Bind{
				"http-bind": {BindParams: models.BindParams{Name: "http-bind"}, Port: ptrInt64(8080)},
			},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		bind := &models.Bind{BindParams: models.BindParams{Name: "http-bind"}}
		ops := comp.compareBindsWithIndex("http-frontend",
			map[string]*models.Bind{"http-bind": bind},
			map[string]*models.Bind{"http-bind": bind})
		assert.Empty(t, ops)
	})
}

func TestCompareACLs(t *testing.T) {
	comp := New()

	t.Run("add ACL to frontend", func(t *testing.T) {
		summary := &DiffSummary{}
		ops := comp.compareACLs(
			"frontend",
			"http-frontend",
			models.Acls{},
			models.Acls{
				{ACLName: "is_api", Criterion: "path_beg", Value: "/api"},
			},
			summary,
		)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected ACL create operation")
	})

	t.Run("delete ACL from frontend", func(t *testing.T) {
		summary := &DiffSummary{}
		ops := comp.compareACLs(
			"frontend",
			"http-frontend",
			models.Acls{
				{ACLName: "old_acl", Criterion: "path", Value: "/old"},
			},
			models.Acls{},
			summary,
		)
		require.NotEmpty(t, ops)
		hasDelete := false
		for _, op := range ops {
			if op.Type() == sections.OperationDelete {
				hasDelete = true
				break
			}
		}
		assert.True(t, hasDelete, "Expected ACL delete operation")
	})

	t.Run("update ACL on frontend", func(t *testing.T) {
		summary := &DiffSummary{}
		ops := comp.compareACLs(
			"frontend",
			"http-frontend",
			models.Acls{
				{ACLName: "is_api", Criterion: "path_beg", Value: "/api"},
			},
			models.Acls{
				{ACLName: "is_api", Criterion: "path_beg", Value: "/api/v2"},
			},
			summary,
		)
		require.NotEmpty(t, ops)
	})

	t.Run("add ACL to backend", func(t *testing.T) {
		summary := &DiffSummary{}
		ops := comp.compareACLs(
			"backend",
			"api-backend",
			models.Acls{},
			models.Acls{
				{ACLName: "is_health", Criterion: "path", Value: "/health"},
			},
			summary,
		)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected ACL create operation")
	})

	t.Run("no changes", func(t *testing.T) {
		summary := &DiffSummary{}
		acls := models.Acls{
			{ACLName: "is_api", Criterion: "path_beg", Value: "/api"},
		}
		ops := comp.compareACLs("frontend", "http-frontend", acls, acls, summary)
		assert.Empty(t, ops)
	})
}

func TestCompareHTTPRequestRules(t *testing.T) {
	comp := New()

	t.Run("add HTTP request rule to frontend", func(t *testing.T) {
		ops := comp.compareHTTPRequestRules(
			"frontend",
			"http-frontend",
			models.HTTPRequestRules{},
			models.HTTPRequestRules{
				{Type: "deny", DenyStatus: ptrInt64(403)},
			},
		)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected HTTP request rule create operation")
	})

	t.Run("delete HTTP request rule", func(t *testing.T) {
		ops := comp.compareHTTPRequestRules(
			"frontend",
			"http-frontend",
			models.HTTPRequestRules{
				{Type: "deny", DenyStatus: ptrInt64(403)},
			},
			models.HTTPRequestRules{},
		)
		require.NotEmpty(t, ops)
		hasDelete := false
		for _, op := range ops {
			if op.Type() == sections.OperationDelete {
				hasDelete = true
				break
			}
		}
		assert.True(t, hasDelete, "Expected HTTP request rule delete operation")
	})

	t.Run("update HTTP request rule", func(t *testing.T) {
		ops := comp.compareHTTPRequestRules(
			"frontend",
			"http-frontend",
			models.HTTPRequestRules{
				{Type: "deny", DenyStatus: ptrInt64(403)},
			},
			models.HTTPRequestRules{
				{Type: "deny", DenyStatus: ptrInt64(404)},
			},
		)
		require.NotEmpty(t, ops)
	})

	t.Run("backend HTTP request rules", func(t *testing.T) {
		ops := comp.compareHTTPRequestRules(
			"backend",
			"api-backend",
			models.HTTPRequestRules{},
			models.HTTPRequestRules{
				{Type: "set-header", HdrName: "X-Backend", HdrFormat: "api"},
			},
		)
		require.NotEmpty(t, ops)
	})
}

func TestCompareHTTPResponseRules(t *testing.T) {
	comp := New()

	t.Run("add HTTP response rule", func(t *testing.T) {
		ops := comp.compareHTTPResponseRules(
			"frontend",
			"http-frontend",
			models.HTTPResponseRules{},
			models.HTTPResponseRules{
				{Type: "set-header", HdrName: "X-Frame-Options", HdrFormat: "DENY"},
			},
		)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected HTTP response rule create operation")
	})

	t.Run("delete HTTP response rule", func(t *testing.T) {
		ops := comp.compareHTTPResponseRules(
			"backend",
			"api-backend",
			models.HTTPResponseRules{
				{Type: "set-header", HdrName: "Server", HdrFormat: "HAProxy"},
			},
			models.HTTPResponseRules{},
		)
		require.NotEmpty(t, ops)
		hasDelete := false
		for _, op := range ops {
			if op.Type() == sections.OperationDelete {
				hasDelete = true
				break
			}
		}
		assert.True(t, hasDelete, "Expected HTTP response rule delete operation")
	})
}

func TestCompareBackendSwitchingRules(t *testing.T) {
	comp := New()

	t.Run("add backend switching rule", func(t *testing.T) {
		ops := comp.compareBackendSwitchingRules(
			"http-frontend",
			models.BackendSwitchingRules{},
			models.BackendSwitchingRules{
				{Name: "api-backend", Cond: "if", CondTest: "is_api"},
			},
		)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected backend switching rule create operation")
	})

	t.Run("delete backend switching rule", func(t *testing.T) {
		ops := comp.compareBackendSwitchingRules(
			"http-frontend",
			models.BackendSwitchingRules{
				{Name: "old-backend", Cond: "if", CondTest: "old_acl"},
			},
			models.BackendSwitchingRules{},
		)
		require.NotEmpty(t, ops)
		hasDelete := false
		for _, op := range ops {
			if op.Type() == sections.OperationDelete {
				hasDelete = true
				break
			}
		}
		assert.True(t, hasDelete, "Expected backend switching rule delete operation")
	})

	t.Run("update backend switching rule", func(t *testing.T) {
		ops := comp.compareBackendSwitchingRules(
			"http-frontend",
			models.BackendSwitchingRules{
				{Name: "api-backend", Cond: "if", CondTest: "is_api"},
			},
			models.BackendSwitchingRules{
				{Name: "api-backend-v2", Cond: "if", CondTest: "is_api"},
			},
		)
		require.NotEmpty(t, ops)
	})
}

func TestCompareServers(t *testing.T) {
	comp := New()

	t.Run("add server to backend", func(t *testing.T) {
		summary := &DiffSummary{
			ServersAdded:    make(map[string][]string),
			ServersModified: make(map[string][]string),
			ServersDeleted:  make(map[string][]string),
		}
		ops := comp.compareServersWithIndex("api-backend",
			map[string]*models.Server{},
			map[string]*models.Server{
				"srv1": {Name: "srv1", Address: "127.0.0.1", Port: ptrInt64(8080)},
			},
			summary)
		require.NotEmpty(t, ops)
		hasCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate {
				hasCreate = true
				break
			}
		}
		assert.True(t, hasCreate, "Expected server create operation")
	})

	t.Run("delete server from backend", func(t *testing.T) {
		summary := &DiffSummary{
			ServersAdded:    make(map[string][]string),
			ServersModified: make(map[string][]string),
			ServersDeleted:  make(map[string][]string),
		}
		ops := comp.compareServersWithIndex("api-backend",
			map[string]*models.Server{
				"old-srv": {Name: "old-srv", Address: "10.0.0.1", Port: ptrInt64(80)},
			},
			map[string]*models.Server{},
			summary)
		require.NotEmpty(t, ops)
		hasDelete := false
		for _, op := range ops {
			if op.Type() == sections.OperationDelete {
				hasDelete = true
				break
			}
		}
		assert.True(t, hasDelete, "Expected server delete operation")
	})

	t.Run("update server", func(t *testing.T) {
		summary := &DiffSummary{
			ServersAdded:    make(map[string][]string),
			ServersModified: make(map[string][]string),
			ServersDeleted:  make(map[string][]string),
		}
		ops := comp.compareServersWithIndex("api-backend",
			map[string]*models.Server{
				"srv1": {Name: "srv1", Address: "127.0.0.1", Port: ptrInt64(8080)},
			},
			map[string]*models.Server{
				"srv1": {Name: "srv1", Address: "127.0.0.1", Port: ptrInt64(8081)},
			},
			summary)
		require.NotEmpty(t, ops)
		hasUpdate := false
		for _, op := range ops {
			if op.Type() == sections.OperationUpdate {
				hasUpdate = true
				break
			}
		}
		assert.True(t, hasUpdate, "Expected server update operation")
	})

	t.Run("no changes", func(t *testing.T) {
		summary := &DiffSummary{
			ServersAdded:    make(map[string][]string),
			ServersModified: make(map[string][]string),
			ServersDeleted:  make(map[string][]string),
		}
		server := &models.Server{Name: "srv1", Address: "127.0.0.1", Port: ptrInt64(8080)}
		ops := comp.compareServersWithIndex("api-backend",
			map[string]*models.Server{"srv1": server},
			map[string]*models.Server{"srv1": server},
			summary)
		assert.Empty(t, ops)
	})
}

func TestCompareFilters(t *testing.T) {
	comp := New()

	t.Run("add filter to frontend", func(t *testing.T) {
		ops := comp.compareFilters(
			"frontend",
			"http-frontend",
			nil,
			models.Filters{{Type: "compression"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("add filter to backend", func(t *testing.T) {
		ops := comp.compareFilters(
			"backend",
			"api-backend",
			nil,
			models.Filters{{Type: "spoe"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete filter from frontend", func(t *testing.T) {
		ops := comp.compareFilters(
			"frontend",
			"http-frontend",
			models.Filters{{Type: "compression"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("delete filter from backend", func(t *testing.T) {
		ops := comp.compareFilters(
			"backend",
			"api-backend",
			models.Filters{{Type: "spoe"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update filter in frontend", func(t *testing.T) {
		ops := comp.compareFilters(
			"frontend",
			"http-frontend",
			models.Filters{{Type: "compression"}},
			models.Filters{{Type: "spoe"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("update filter in backend", func(t *testing.T) {
		ops := comp.compareFilters(
			"backend",
			"api-backend",
			models.Filters{{Type: "compression"}},
			models.Filters{{Type: "spoe"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		filter := &models.Filter{Type: "compression"}
		ops := comp.compareFilters(
			"frontend",
			"http-frontend",
			models.Filters{filter},
			models.Filters{filter},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareHTTPChecks(t *testing.T) {
	comp := New()

	t.Run("add HTTP check", func(t *testing.T) {
		ops := comp.compareHTTPChecks(
			"api-backend",
			nil,
			models.HTTPChecks{{Type: "send"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete HTTP check", func(t *testing.T) {
		ops := comp.compareHTTPChecks(
			"api-backend",
			models.HTTPChecks{{Type: "send"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update HTTP check", func(t *testing.T) {
		ops := comp.compareHTTPChecks(
			"api-backend",
			models.HTTPChecks{{Type: "send"}},
			models.HTTPChecks{{Type: "expect"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		check := &models.HTTPCheck{Type: "send"}
		ops := comp.compareHTTPChecks(
			"api-backend",
			models.HTTPChecks{check},
			models.HTTPChecks{check},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareTCPChecks(t *testing.T) {
	comp := New()

	t.Run("add TCP check", func(t *testing.T) {
		ops := comp.compareTCPChecks(
			"api-backend",
			nil,
			models.TCPChecks{{Action: "send"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete TCP check", func(t *testing.T) {
		ops := comp.compareTCPChecks(
			"api-backend",
			models.TCPChecks{{Action: "send"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update TCP check", func(t *testing.T) {
		ops := comp.compareTCPChecks(
			"api-backend",
			models.TCPChecks{{Action: "send"}},
			models.TCPChecks{{Action: "connect"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		check := &models.TCPCheck{Action: "send"}
		ops := comp.compareTCPChecks(
			"api-backend",
			models.TCPChecks{check},
			models.TCPChecks{check},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareServerTemplates(t *testing.T) {
	comp := New()

	t.Run("add server template", func(t *testing.T) {
		ops := comp.compareServerTemplatesWithIndex("api-backend",
			map[string]*models.ServerTemplate{},
			map[string]*models.ServerTemplate{
				"srv": {Prefix: "srv", Fqdn: "service.local", NumOrRange: "1-5", Port: ptrInt64(8080)},
			})
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete server template", func(t *testing.T) {
		ops := comp.compareServerTemplatesWithIndex("api-backend",
			map[string]*models.ServerTemplate{
				"srv": {Prefix: "srv", Fqdn: "service.local", NumOrRange: "1-5", Port: ptrInt64(8080)},
			},
			map[string]*models.ServerTemplate{})
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update server template", func(t *testing.T) {
		ops := comp.compareServerTemplatesWithIndex("api-backend",
			map[string]*models.ServerTemplate{
				"srv": {Prefix: "srv", Fqdn: "service.local", NumOrRange: "1-5", Port: ptrInt64(8080)},
			},
			map[string]*models.ServerTemplate{
				"srv": {Prefix: "srv", Fqdn: "new-service.local", NumOrRange: "1-10", Port: ptrInt64(8080)},
			})
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		template := &models.ServerTemplate{Prefix: "srv", Fqdn: "service.local", NumOrRange: "1-5", Port: ptrInt64(8080)}
		ops := comp.compareServerTemplatesWithIndex("api-backend",
			map[string]*models.ServerTemplate{"srv": template},
			map[string]*models.ServerTemplate{"srv": template})
		assert.Empty(t, ops)
	})
}

func TestCompareCaptures(t *testing.T) {
	comp := New()

	t.Run("add capture", func(t *testing.T) {
		ops := comp.compareCaptures(
			"http-frontend",
			nil,
			models.Captures{{Type: "request", Length: 100}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete capture", func(t *testing.T) {
		ops := comp.compareCaptures(
			"http-frontend",
			models.Captures{{Type: "request", Length: 100}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update capture", func(t *testing.T) {
		ops := comp.compareCaptures(
			"http-frontend",
			models.Captures{{Type: "request", Length: 100}},
			models.Captures{{Type: "response", Length: 200}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		capture := &models.Capture{Type: "request", Length: 100}
		ops := comp.compareCaptures(
			"http-frontend",
			models.Captures{capture},
			models.Captures{capture},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareTCPRequestRules(t *testing.T) {
	comp := New()

	t.Run("add TCP request rule to frontend", func(t *testing.T) {
		ops := comp.compareTCPRequestRules(
			"frontend",
			"http-frontend",
			nil,
			models.TCPRequestRules{{Type: "content", Action: "accept"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("add TCP request rule to backend", func(t *testing.T) {
		ops := comp.compareTCPRequestRules(
			"backend",
			"api-backend",
			nil,
			models.TCPRequestRules{{Type: "content", Action: "reject"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete TCP request rule from frontend", func(t *testing.T) {
		ops := comp.compareTCPRequestRules(
			"frontend",
			"http-frontend",
			models.TCPRequestRules{{Type: "content", Action: "accept"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("delete TCP request rule from backend", func(t *testing.T) {
		ops := comp.compareTCPRequestRules(
			"backend",
			"api-backend",
			models.TCPRequestRules{{Type: "content", Action: "reject"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update TCP request rule in frontend", func(t *testing.T) {
		ops := comp.compareTCPRequestRules(
			"frontend",
			"http-frontend",
			models.TCPRequestRules{{Type: "content", Action: "accept"}},
			models.TCPRequestRules{{Type: "content", Action: "reject"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("update TCP request rule in backend", func(t *testing.T) {
		ops := comp.compareTCPRequestRules(
			"backend",
			"api-backend",
			models.TCPRequestRules{{Type: "content", Action: "accept"}},
			models.TCPRequestRules{{Type: "content", Action: "reject"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		rule := &models.TCPRequestRule{Type: "content", Action: "accept"}
		ops := comp.compareTCPRequestRules(
			"frontend",
			"http-frontend",
			models.TCPRequestRules{rule},
			models.TCPRequestRules{rule},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareTCPResponseRules(t *testing.T) {
	comp := New()

	t.Run("add TCP response rule", func(t *testing.T) {
		ops := comp.compareTCPResponseRules(
			"api-backend",
			nil,
			models.TCPResponseRules{{Type: "content", Action: "accept"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete TCP response rule", func(t *testing.T) {
		ops := comp.compareTCPResponseRules(
			"api-backend",
			models.TCPResponseRules{{Type: "content", Action: "accept"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update TCP response rule", func(t *testing.T) {
		ops := comp.compareTCPResponseRules(
			"api-backend",
			models.TCPResponseRules{{Type: "content", Action: "accept"}},
			models.TCPResponseRules{{Type: "content", Action: "reject"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		rule := &models.TCPResponseRule{Type: "content", Action: "accept"}
		ops := comp.compareTCPResponseRules(
			"api-backend",
			models.TCPResponseRules{rule},
			models.TCPResponseRules{rule},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareStickRules(t *testing.T) {
	comp := New()

	t.Run("add stick rule", func(t *testing.T) {
		ops := comp.compareStickRules(
			"api-backend",
			nil,
			models.StickRules{{Type: "match", Pattern: "src"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete stick rule", func(t *testing.T) {
		ops := comp.compareStickRules(
			"api-backend",
			models.StickRules{{Type: "match", Pattern: "src"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update stick rule", func(t *testing.T) {
		ops := comp.compareStickRules(
			"api-backend",
			models.StickRules{{Type: "match", Pattern: "src"}},
			models.StickRules{{Type: "store-request", Pattern: "src"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		rule := &models.StickRule{Type: "match", Pattern: "src"}
		ops := comp.compareStickRules(
			"api-backend",
			models.StickRules{rule},
			models.StickRules{rule},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareHTTPAfterResponseRules(t *testing.T) {
	comp := New()

	t.Run("add HTTP after response rule", func(t *testing.T) {
		ops := comp.compareHTTPAfterResponseRules(
			"api-backend",
			nil,
			models.HTTPAfterResponseRules{{Type: "set-header", HdrName: "X-Custom", HdrFormat: "value"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete HTTP after response rule", func(t *testing.T) {
		ops := comp.compareHTTPAfterResponseRules(
			"api-backend",
			models.HTTPAfterResponseRules{{Type: "set-header", HdrName: "X-Custom", HdrFormat: "value"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update HTTP after response rule", func(t *testing.T) {
		ops := comp.compareHTTPAfterResponseRules(
			"api-backend",
			models.HTTPAfterResponseRules{{Type: "set-header", HdrName: "X-Custom", HdrFormat: "old-value"}},
			models.HTTPAfterResponseRules{{Type: "set-header", HdrName: "X-Custom", HdrFormat: "new-value"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		rule := &models.HTTPAfterResponseRule{Type: "set-header", HdrName: "X-Custom", HdrFormat: "value"}
		ops := comp.compareHTTPAfterResponseRules(
			"api-backend",
			models.HTTPAfterResponseRules{rule},
			models.HTTPAfterResponseRules{rule},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareServerSwitchingRules(t *testing.T) {
	comp := New()

	t.Run("add server switching rule", func(t *testing.T) {
		ops := comp.compareServerSwitchingRules(
			"api-backend",
			nil,
			models.ServerSwitchingRules{{TargetServer: "srv1", Cond: "if", CondTest: "is_api"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete server switching rule", func(t *testing.T) {
		ops := comp.compareServerSwitchingRules(
			"api-backend",
			models.ServerSwitchingRules{{TargetServer: "srv1", Cond: "if", CondTest: "is_api"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update server switching rule", func(t *testing.T) {
		ops := comp.compareServerSwitchingRules(
			"api-backend",
			models.ServerSwitchingRules{{TargetServer: "srv1", Cond: "if", CondTest: "is_api"}},
			models.ServerSwitchingRules{{TargetServer: "srv2", Cond: "if", CondTest: "is_web"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		rule := &models.ServerSwitchingRule{TargetServer: "srv1", Cond: "if", CondTest: "is_api"}
		ops := comp.compareServerSwitchingRules(
			"api-backend",
			models.ServerSwitchingRules{rule},
			models.ServerSwitchingRules{rule},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareLogTargets(t *testing.T) {
	comp := New()

	t.Run("add log target to frontend", func(t *testing.T) {
		ops := comp.compareLogTargets(
			"frontend",
			"http-frontend",
			nil,
			models.LogTargets{{Address: "127.0.0.1:514", Facility: "local0"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("add log target to backend", func(t *testing.T) {
		ops := comp.compareLogTargets(
			"backend",
			"api-backend",
			nil,
			models.LogTargets{{Address: "127.0.0.1:514", Facility: "local1"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete log target from frontend", func(t *testing.T) {
		ops := comp.compareLogTargets(
			"frontend",
			"http-frontend",
			models.LogTargets{{Address: "127.0.0.1:514", Facility: "local0"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("delete log target from backend", func(t *testing.T) {
		ops := comp.compareLogTargets(
			"backend",
			"api-backend",
			models.LogTargets{{Address: "127.0.0.1:514", Facility: "local1"}},
			nil,
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update log target in frontend", func(t *testing.T) {
		ops := comp.compareLogTargets(
			"frontend",
			"http-frontend",
			models.LogTargets{{Address: "127.0.0.1:514", Facility: "local0"}},
			models.LogTargets{{Address: "10.0.0.1:514", Facility: "local0"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("update log target in backend", func(t *testing.T) {
		ops := comp.compareLogTargets(
			"backend",
			"api-backend",
			models.LogTargets{{Address: "127.0.0.1:514", Facility: "local1"}},
			models.LogTargets{{Address: "10.0.0.1:514", Facility: "local1"}},
		)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		logTarget := &models.LogTarget{Address: "127.0.0.1:514", Facility: "local0"}
		ops := comp.compareLogTargets(
			"frontend",
			"http-frontend",
			models.LogTargets{logTarget},
			models.LogTargets{logTarget},
		)
		assert.Empty(t, ops)
	})
}

func TestCompareLogForwards(t *testing.T) {
	comp := New()

	t.Run("add log-forward section", func(t *testing.T) {
		current := &parser.StructuredConfig{}
		desired := &parser.StructuredConfig{
			LogForwards: []*models.LogForward{
				{LogForwardBase: models.LogForwardBase{Name: "log-fwd1"}},
			},
		}
		ops := comp.compareLogForwards(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete log-forward section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			LogForwards: []*models.LogForward{
				{LogForwardBase: models.LogForwardBase{Name: "log-fwd1"}},
			},
		}
		desired := &parser.StructuredConfig{}
		ops := comp.compareLogForwards(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update log-forward section", func(t *testing.T) {
		maxconn1 := int64(100)
		maxconn2 := int64(200)
		current := &parser.StructuredConfig{
			LogForwards: []*models.LogForward{
				{LogForwardBase: models.LogForwardBase{Name: "log-fwd1", Maxconn: &maxconn1}},
			},
		}
		desired := &parser.StructuredConfig{
			LogForwards: []*models.LogForward{
				{LogForwardBase: models.LogForwardBase{Name: "log-fwd1", Maxconn: &maxconn2}},
			},
		}
		ops := comp.compareLogForwards(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		config := &parser.StructuredConfig{
			LogForwards: []*models.LogForward{
				{LogForwardBase: models.LogForwardBase{Name: "log-fwd1"}},
			},
		}
		ops := comp.compareLogForwards(config, config)
		assert.Empty(t, ops)
	})
}

func TestCompareResolvers(t *testing.T) {
	comp := New()

	t.Run("add resolver section", func(t *testing.T) {
		current := &parser.StructuredConfig{}
		desired := &parser.StructuredConfig{
			Resolvers: []*models.Resolver{
				{ResolverBase: models.ResolverBase{Name: "mydns"}},
			},
		}
		ops := comp.compareResolvers(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete resolver section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Resolvers: []*models.Resolver{
				{ResolverBase: models.ResolverBase{Name: "mydns"}},
			},
		}
		desired := &parser.StructuredConfig{}
		ops := comp.compareResolvers(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update resolver section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Resolvers: []*models.Resolver{
				{
					ResolverBase: models.ResolverBase{Name: "mydns", ResolveRetries: 3, TimeoutResolve: 1000},
					Nameservers:  make(map[string]models.Nameserver),
				},
			},
		}
		desired := &parser.StructuredConfig{
			Resolvers: []*models.Resolver{
				{
					ResolverBase: models.ResolverBase{Name: "mydns", ResolveRetries: 5, TimeoutResolve: 2000},
					Nameservers:  make(map[string]models.Nameserver),
				},
			},
		}
		ops := comp.compareResolvers(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("add nameserver to resolver", func(t *testing.T) {
		current := &parser.StructuredConfig{
			Resolvers: []*models.Resolver{
				{
					ResolverBase: models.ResolverBase{Name: "mydns"},
				},
			},
			NameserverIndex: map[string]map[string]*models.Nameserver{
				"mydns": {},
			},
		}
		dns1 := &models.Nameserver{Name: "dns1", Address: ptrString("8.8.8.8"), Port: ptrInt64(53)}
		desired := &parser.StructuredConfig{
			Resolvers: []*models.Resolver{
				{
					ResolverBase: models.ResolverBase{Name: "mydns"},
				},
			},
			NameserverIndex: map[string]map[string]*models.Nameserver{
				"mydns": {
					"dns1": dns1,
				},
			},
		}
		ops := comp.compareResolvers(current, desired)
		require.NotEmpty(t, ops)
		// Should have create operation for nameserver
		hasNameserverCreate := false
		for _, op := range ops {
			if op.Type() == sections.OperationCreate && op.Section() == "nameserver" {
				hasNameserverCreate = true
				break
			}
		}
		assert.True(t, hasNameserverCreate, "Expected nameserver create operation")
	})

	t.Run("no changes", func(t *testing.T) {
		config := &parser.StructuredConfig{
			Resolvers: []*models.Resolver{
				{
					ResolverBase: models.ResolverBase{Name: "mydns"},
					Nameservers:  make(map[string]models.Nameserver),
				},
			},
		}
		ops := comp.compareResolvers(config, config)
		assert.Empty(t, ops)
	})
}

func TestCompareCrtStores(t *testing.T) {
	comp := New()

	t.Run("add crt-store section", func(t *testing.T) {
		current := &parser.StructuredConfig{}
		desired := &parser.StructuredConfig{
			CrtStores: []*models.CrtStore{
				{Name: "mystore"},
			},
		}
		ops := comp.compareCrtStores(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationCreate, ops[0].Type())
	})

	t.Run("delete crt-store section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			CrtStores: []*models.CrtStore{
				{Name: "mystore"},
			},
		}
		desired := &parser.StructuredConfig{}
		ops := comp.compareCrtStores(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationDelete, ops[0].Type())
	})

	t.Run("update crt-store section", func(t *testing.T) {
		current := &parser.StructuredConfig{
			CrtStores: []*models.CrtStore{
				{Name: "mystore", CrtBase: "/old/path"},
			},
		}
		desired := &parser.StructuredConfig{
			CrtStores: []*models.CrtStore{
				{Name: "mystore", CrtBase: "/new/path"},
			},
		}
		ops := comp.compareCrtStores(current, desired)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
	})

	t.Run("no changes", func(t *testing.T) {
		config := &parser.StructuredConfig{
			CrtStores: []*models.CrtStore{
				{Name: "mystore"},
			},
		}
		ops := comp.compareCrtStores(config, config)
		assert.Empty(t, ops)
	})
}

func TestCompareModifiedFrontends(t *testing.T) {
	comp := New()

	t.Run("modify frontend mode", func(t *testing.T) {
		summary := &DiffSummary{
			FrontendsModified: make([]string, 0),
		}
		mode1 := "http"
		mode2 := "tcp"
		currentFrontends := map[string]*models.Frontend{
			"http-frontend": {
				FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: mode1},
			},
		}
		desiredFrontends := map[string]*models.Frontend{
			"http-frontend": {
				FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: mode2},
			},
		}
		currentConfig := &parser.StructuredConfig{
			BindIndex: make(map[string]map[string]*models.Bind),
		}
		desiredConfig := &parser.StructuredConfig{
			BindIndex: make(map[string]map[string]*models.Bind),
		}
		ops := comp.compareModifiedFrontendsWithIndexes(desiredFrontends, currentFrontends, currentConfig, desiredConfig, summary)
		require.NotEmpty(t, ops)
		assert.Equal(t, sections.OperationUpdate, ops[0].Type())
		assert.Contains(t, summary.FrontendsModified, "http-frontend")
	})

	t.Run("modify frontend with ACL changes", func(t *testing.T) {
		summary := &DiffSummary{
			FrontendsModified: make([]string, 0),
		}
		currentFrontends := map[string]*models.Frontend{
			"http-frontend": {
				FrontendBase: models.FrontendBase{Name: "http-frontend"},
				ACLList:      nil,
			},
		}
		desiredFrontends := map[string]*models.Frontend{
			"http-frontend": {
				FrontendBase: models.FrontendBase{Name: "http-frontend"},
				ACLList:      models.Acls{{ACLName: "is_api", Criterion: "path_beg", Value: "/api"}},
			},
		}
		currentConfig := &parser.StructuredConfig{
			BindIndex: make(map[string]map[string]*models.Bind),
		}
		desiredConfig := &parser.StructuredConfig{
			BindIndex: make(map[string]map[string]*models.Bind),
		}
		ops := comp.compareModifiedFrontendsWithIndexes(desiredFrontends, currentFrontends, currentConfig, desiredConfig, summary)
		require.NotEmpty(t, ops)
		assert.Contains(t, summary.FrontendsModified, "http-frontend")
	})

	t.Run("no changes to frontend", func(t *testing.T) {
		summary := &DiffSummary{
			FrontendsModified: make([]string, 0),
		}
		frontend := &models.Frontend{
			FrontendBase: models.FrontendBase{Name: "http-frontend"},
		}
		currentFrontends := map[string]*models.Frontend{
			"http-frontend": frontend,
		}
		desiredFrontends := map[string]*models.Frontend{
			"http-frontend": frontend,
		}
		currentConfig := &parser.StructuredConfig{
			BindIndex: make(map[string]map[string]*models.Bind),
		}
		desiredConfig := &parser.StructuredConfig{
			BindIndex: make(map[string]map[string]*models.Bind),
		}
		ops := comp.compareModifiedFrontendsWithIndexes(desiredFrontends, currentFrontends, currentConfig, desiredConfig, summary)
		assert.Empty(t, ops)
	})
}

func TestFrontendsEqualWithoutNestedCollections(t *testing.T) {
	t.Run("equal frontends", func(t *testing.T) {
		f1 := &models.Frontend{
			FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: "http"},
		}
		f2 := &models.Frontend{
			FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: "http"},
		}
		assert.True(t, frontendsEqualWithoutNestedCollections(f1, f2))
	})

	t.Run("different mode", func(t *testing.T) {
		f1 := &models.Frontend{
			FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: "http"},
		}
		f2 := &models.Frontend{
			FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: "tcp"},
		}
		assert.False(t, frontendsEqualWithoutNestedCollections(f1, f2))
	})

	t.Run("equal with different nested collections", func(t *testing.T) {
		f1 := &models.Frontend{
			FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: "http"},
			ACLList:      models.Acls{{ACLName: "acl1"}},
		}
		f2 := &models.Frontend{
			FrontendBase: models.FrontendBase{Name: "http-frontend", Mode: "http"},
			ACLList:      models.Acls{{ACLName: "acl2"}},
		}
		// Should be equal because nested collections are ignored
		assert.True(t, frontendsEqualWithoutNestedCollections(f1, f2))
	})
}
