package directives

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDynamicUpdate_Parse(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		wantErr bool
		assert  func(*testing.T, *DynamicUpdateData)
	}{
		{
			name:  "url",
			parts: []string{"url", "https://example.com/update"},
			assert: func(t *testing.T, d *DynamicUpdateData) {
				t.Helper()
				assert.Equal(t, "https://example.com/update", d.URL)
			},
		},
		{
			name:  "delay",
			parts: []string{"delay", "60"},
			assert: func(t *testing.T, d *DynamicUpdateData) {
				t.Helper()
				assert.Equal(t, 60, d.Delay)
			},
		},
		{
			name:  "timeout",
			parts: []string{"timeout", "30"},
			assert: func(t *testing.T, d *DynamicUpdateData) {
				t.Helper()
				assert.Equal(t, 30, d.Timeout)
			},
		},
		{
			name:  "map",
			parts: []string{"map", "hosts.map"},
			assert: func(t *testing.T, d *DynamicUpdateData) {
				t.Helper()
				assert.Equal(t, "hosts.map", d.Map)
			},
		},
		{
			name:    "delay non-numeric",
			parts:   []string{"delay", "abc"},
			wantErr: true,
		},
		{
			name:    "timeout non-numeric",
			parts:   []string{"timeout", "xyz"},
			wantErr: true,
		},
		{
			name:    "unknown directive",
			parts:   []string{"unknown", "value"},
			wantErr: true,
		},
		{
			name:    "too few parts",
			parts:   []string{"url"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewDynamicUpdate()
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

func TestDynamicUpdate_GetParserName(t *testing.T) {
	assert.Equal(t, "dynamic-update", NewDynamicUpdate().GetParserName())
}

func TestDynamicUpdate_ResultAll(t *testing.T) {
	p := NewDynamicUpdate()
	results, _, err := p.ResultAll()
	require.NoError(t, err)
	assert.Empty(t, results)

	for _, parts := range [][]string{
		{"url", "https://x/update"},
		{"delay", "10"},
		{"timeout", "5"},
		{"map", "hosts.map"},
	} {
		_, err := p.Parse("", parts, "")
		require.NoError(t, err)
	}

	results, _, err = p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 4)
	assert.Equal(t, "url https://x/update", results[0].Data)
	assert.Equal(t, "delay 10", results[1].Data)
	assert.Equal(t, "timeout 5", results[2].Data)
	assert.Equal(t, "map hosts.map", results[3].Data)
}

func TestDynamicUpdate_InsertSetDeleteInit(t *testing.T) {
	p := NewDynamicUpdate()
	src := &DynamicUpdateData{URL: "http://a"}
	require.NoError(t, p.Insert(src, 0))
	assert.Same(t, src, p.GetData())

	require.NoError(t, p.Set(&DynamicUpdateData{URL: "http://b"}, 0))
	assert.Equal(t, "http://b", p.GetData().URL)

	require.NoError(t, p.Insert("wrong", 0))
	assert.Equal(t, "http://b", p.GetData().URL)

	require.NoError(t, p.Delete(0))
	assert.Nil(t, p.GetData())

	_, err := p.Parse("", []string{"url", "x"}, "")
	require.NoError(t, err)
	p.Init()
	assert.Nil(t, p.GetData())
}

func TestDynamicUpdate_PreParseAndGetOne(t *testing.T) {
	p := NewDynamicUpdate()
	_, err := p.Get(false)
	require.Error(t, err)

	pre := []string{"# du"}
	_, err = p.PreParse("", []string{"url", "https://x"}, pre, "")
	require.NoError(t, err)

	got, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, pre, got)

	one, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "https://x", one.(*DynamicUpdateData).URL)

	_, err = p.GetOne(2)
	require.Error(t, err)

	p.SetPreComments([]string{"# z"})
	got, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# z"}, got)
}
