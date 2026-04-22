package directives

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWAFGlobal_Parse(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		wantErr bool
		assert  func(*testing.T, *WAFGlobalData)
	}{
		{
			name:  "rules-file",
			parts: []string{"rules-file", "/etc/haproxy/waf.rules"},
			assert: func(t *testing.T, d *WAFGlobalData) {
				t.Helper()
				assert.Equal(t, "/etc/haproxy/waf.rules", d.RulesFile)
			},
		},
		{
			name:  "body-limit",
			parts: []string{"body-limit", "5000"},
			assert: func(t *testing.T, d *WAFGlobalData) {
				t.Helper()
				assert.Equal(t, 5000, d.BodyLimit)
			},
		},
		{
			name:  "json-levels",
			parts: []string{"json-levels", "10"},
			assert: func(t *testing.T, d *WAFGlobalData) {
				t.Helper()
				assert.Equal(t, 10, d.JSONLevels)
			},
		},
		{
			name:  "analyzer-cache",
			parts: []string{"analyzer-cache", "200"},
			assert: func(t *testing.T, d *WAFGlobalData) {
				t.Helper()
				assert.Equal(t, 200, d.AnalyzerCache)
			},
		},
		{
			name:    "body-limit non-numeric",
			parts:   []string{"body-limit", "not-a-number"},
			wantErr: true,
		},
		{
			name:    "json-levels non-numeric",
			parts:   []string{"json-levels", "abc"},
			wantErr: true,
		},
		{
			name:    "analyzer-cache non-numeric",
			parts:   []string{"analyzer-cache", "xyz"},
			wantErr: true,
		},
		{
			name:    "unknown directive",
			parts:   []string{"unknown-directive", "value"},
			wantErr: true,
		},
		{
			name:    "too few parts",
			parts:   []string{"rules-file"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewWAFGlobal()
			_, err := p.Parse("line", tt.parts, "")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, p.GetData())
			tt.assert(t, p.GetData())
		})
	}
}

func TestWAFGlobal_InitAndDelete(t *testing.T) {
	p := NewWAFGlobal()
	_, err := p.Parse("", []string{"rules-file", "/x"}, "")
	require.NoError(t, err)
	require.NotNil(t, p.GetData())

	p.Init()
	assert.Nil(t, p.GetData())

	// Delete on empty parser should not error
	require.NoError(t, p.Delete(0))
}

func TestWAFGlobal_GetParserName(t *testing.T) {
	assert.Equal(t, "waf-global", NewWAFGlobal().GetParserName())
}

func TestWAFGlobal_GetOne_NotFound(t *testing.T) {
	p := NewWAFGlobal()
	_, err := p.GetOne(0)
	require.Error(t, err)
}

func TestWAFGlobal_Get_CreateIfNotExist(t *testing.T) {
	p := NewWAFGlobal()
	_, err := p.Get(false)
	require.Error(t, err)
	data, err := p.Get(true)
	require.NoError(t, err)
	assert.Nil(t, data)
}

func TestWAFGlobal_InsertSet(t *testing.T) {
	p := NewWAFGlobal()
	src := &WAFGlobalData{RulesFile: "/a", BodyLimit: 1, JSONLevels: 2, AnalyzerCache: 3}
	require.NoError(t, p.Insert(src, 0))
	assert.Same(t, src, p.GetData())

	replacement := &WAFGlobalData{RulesFile: "/b"}
	require.NoError(t, p.Set(replacement, 0))
	assert.Same(t, replacement, p.GetData())

	// Non-matching type must not mutate state
	require.NoError(t, p.Insert("wrong-type", 0))
	assert.Same(t, replacement, p.GetData())
	require.NoError(t, p.Set("wrong-type", 0))
	assert.Same(t, replacement, p.GetData())
}

func TestWAFGlobal_ResultAll(t *testing.T) {
	p := NewWAFGlobal()

	// Empty parser yields no results
	results, _, err := p.ResultAll()
	require.NoError(t, err)
	assert.Empty(t, results)

	// Populate all fields
	_, err = p.Parse("", []string{"rules-file", "/etc/waf.rules"}, "")
	require.NoError(t, err)
	_, err = p.Parse("", []string{"body-limit", "5000"}, "")
	require.NoError(t, err)
	_, err = p.Parse("", []string{"json-levels", "10"}, "")
	require.NoError(t, err)
	_, err = p.Parse("", []string{"analyzer-cache", "200"}, "")
	require.NoError(t, err)

	results, _, err = p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 4)
	assert.Equal(t, "rules-file /etc/waf.rules", results[0].Data)
	assert.Equal(t, "body-limit 5000", results[1].Data)
	assert.Equal(t, "json-levels 10", results[2].Data)
	assert.Equal(t, "analyzer-cache 200", results[3].Data)
}

func TestWAFGlobal_PreParseSetsComments(t *testing.T) {
	p := NewWAFGlobal()
	pre := []string{"# comment"}
	_, err := p.PreParse("", []string{"rules-file", "/x"}, pre, "")
	require.NoError(t, err)
	got, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, pre, got)

	// SetPreComments updates them directly
	p.SetPreComments([]string{"# other"})
	got, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# other"}, got)
}

func TestWAFProfile_Parse(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		wantErr bool
		assert  func(*testing.T, *WAFProfileData)
	}{
		{
			name:  "rules-file",
			parts: []string{"rules-file", "/etc/waf.rules"},
			assert: func(t *testing.T, d *WAFProfileData) {
				t.Helper()
				assert.Equal(t, "/etc/waf.rules", d.RulesFile)
			},
		},
		{
			name:  "body-limit",
			parts: []string{"body-limit", "1000"},
			assert: func(t *testing.T, d *WAFProfileData) {
				t.Helper()
				assert.Equal(t, 1000, d.BodyLimit)
			},
		},
		{
			name:    "body-limit non-numeric",
			parts:   []string{"body-limit", "nope"},
			wantErr: true,
		},
		{
			name:    "unknown directive",
			parts:   []string{"json-levels", "5"},
			wantErr: true,
		},
		{
			name:    "too few parts",
			parts:   []string{"rules-file"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewWAFProfile()
			_, err := p.Parse("line", tt.parts, "")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.assert(t, p.GetData())
		})
	}
}

func TestWAFProfile_GetParserName(t *testing.T) {
	assert.Equal(t, "waf-profile", NewWAFProfile().GetParserName())
}

func TestWAFProfile_ResultAll(t *testing.T) {
	p := NewWAFProfile()
	results, _, err := p.ResultAll()
	require.NoError(t, err)
	assert.Empty(t, results)

	_, err = p.Parse("", []string{"rules-file", "/a"}, "")
	require.NoError(t, err)
	_, err = p.Parse("", []string{"body-limit", "42"}, "")
	require.NoError(t, err)

	results, _, err = p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "rules-file /a", results[0].Data)
	assert.Equal(t, "body-limit 42", results[1].Data)
}

func TestWAFProfile_InsertSetDelete(t *testing.T) {
	p := NewWAFProfile()
	src := &WAFProfileData{RulesFile: "/x", BodyLimit: 7}
	require.NoError(t, p.Insert(src, 0))
	assert.Same(t, src, p.GetData())

	require.NoError(t, p.Set(&WAFProfileData{RulesFile: "/y"}, 0))
	assert.Equal(t, "/y", p.GetData().RulesFile)

	require.NoError(t, p.Insert("wrong", 0))
	assert.Equal(t, "/y", p.GetData().RulesFile)

	require.NoError(t, p.Delete(0))
	assert.Nil(t, p.GetData())
}

func TestWAFProfile_PreParseAndGetOne(t *testing.T) {
	p := NewWAFProfile()
	_, err := p.GetOne(0)
	require.Error(t, err)

	_, err = p.PreParse("", []string{"rules-file", "/a"}, []string{"# c"}, "")
	require.NoError(t, err)

	got, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "/a", got.(*WAFProfileData).RulesFile)

	_, err = p.GetOne(1)
	require.Error(t, err)
}
