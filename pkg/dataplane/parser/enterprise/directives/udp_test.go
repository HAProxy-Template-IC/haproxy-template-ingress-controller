package directives

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUDPLB_Parse(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		wantErr bool
		assert  func(*testing.T, *UDPLBData)
	}{
		{
			name:  "balance",
			parts: []string{"balance", "roundrobin"},
			assert: func(t *testing.T, d *UDPLBData) {
				t.Helper()
				assert.Equal(t, "roundrobin", d.Balance)
			},
		},
		{
			name:  "proxy-requests",
			parts: []string{"proxy-requests", "100"},
			assert: func(t *testing.T, d *UDPLBData) {
				t.Helper()
				assert.Equal(t, 100, d.ProxyRequests)
			},
		},
		{
			name:  "proxy-responses",
			parts: []string{"proxy-responses", "200"},
			assert: func(t *testing.T, d *UDPLBData) {
				t.Helper()
				assert.Equal(t, 200, d.ProxyResponses)
			},
		},
		{
			name:    "proxy-requests non-numeric",
			parts:   []string{"proxy-requests", "abc"},
			wantErr: true,
		},
		{
			name:    "proxy-responses non-numeric",
			parts:   []string{"proxy-responses", "xyz"},
			wantErr: true,
		},
		{
			name:    "unknown directive",
			parts:   []string{"dgram-bind", ":5353"},
			wantErr: true,
		},
		{
			name:    "too few parts",
			parts:   []string{"balance"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewUDPLB()
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

func TestUDPLB_GetParserName(t *testing.T) {
	assert.Equal(t, "udp-lb", NewUDPLB().GetParserName())
}

func TestUDPLB_ResultAll(t *testing.T) {
	p := NewUDPLB()
	results, _, err := p.ResultAll()
	require.NoError(t, err)
	assert.Empty(t, results)

	for _, parts := range [][]string{
		{"balance", "source"},
		{"proxy-requests", "50"},
		{"proxy-responses", "150"},
	} {
		_, err := p.Parse("", parts, "")
		require.NoError(t, err)
	}

	results, _, err = p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "balance source", results[0].Data)
	assert.Equal(t, "proxy-requests 50", results[1].Data)
	assert.Equal(t, "proxy-responses 150", results[2].Data)
}

func TestUDPLB_InsertSetDeleteInit(t *testing.T) {
	p := NewUDPLB()
	src := &UDPLBData{Balance: "roundrobin", ProxyRequests: 1, ProxyResponses: 2}
	require.NoError(t, p.Insert(src, 0))
	assert.Same(t, src, p.GetData())

	require.NoError(t, p.Set(&UDPLBData{Balance: "source"}, 0))
	assert.Equal(t, "source", p.GetData().Balance)

	require.NoError(t, p.Insert("wrong", 0))
	assert.Equal(t, "source", p.GetData().Balance)

	require.NoError(t, p.Delete(0))
	assert.Nil(t, p.GetData())

	_, err := p.Parse("", []string{"balance", "x"}, "")
	require.NoError(t, err)
	p.Init()
	assert.Nil(t, p.GetData())
}

func TestUDPLB_PreParseAndGetOne(t *testing.T) {
	p := NewUDPLB()
	_, err := p.Get(false)
	require.Error(t, err)

	pre := []string{"# udp"}
	_, err = p.PreParse("", []string{"balance", "source"}, pre, "")
	require.NoError(t, err)

	got, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, pre, got)

	one, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "source", one.(*UDPLBData).Balance)

	_, err = p.GetOne(3)
	require.Error(t, err)

	p.SetPreComments([]string{"# other"})
	got, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# other"}, got)
}
