package directives

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBotMgmtProfile_Parse(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		wantErr bool
		assert  func(*testing.T, *BotMgmtProfileData)
	}{
		{
			name:  "score-version",
			parts: []string{"score-version", "2"},
			assert: func(t *testing.T, d *BotMgmtProfileData) {
				t.Helper()
				assert.Equal(t, 2, d.ScoreVersion)
			},
		},
		{
			name:  "track",
			parts: []string{"track", "http_req_rate"},
			assert: func(t *testing.T, d *BotMgmtProfileData) {
				t.Helper()
				assert.Equal(t, "http_req_rate", d.Track)
			},
		},
		{
			name:  "track-peers",
			parts: []string{"track-peers", "mypeers"},
			assert: func(t *testing.T, d *BotMgmtProfileData) {
				t.Helper()
				assert.Equal(t, "mypeers", d.TrackPeers)
			},
		},
		{
			name:    "score-version non-numeric",
			parts:   []string{"score-version", "foo"},
			wantErr: true,
		},
		{
			name:    "unknown directive",
			parts:   []string{"foo", "bar"},
			wantErr: true,
		},
		{
			name:    "too few parts",
			parts:   []string{"track"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewBotMgmtProfile()
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

func TestBotMgmtProfile_GetParserName(t *testing.T) {
	assert.Equal(t, "botmgmt-profile", NewBotMgmtProfile().GetParserName())
}

func TestBotMgmtProfile_ResultAll(t *testing.T) {
	p := NewBotMgmtProfile()
	results, _, err := p.ResultAll()
	require.NoError(t, err)
	assert.Empty(t, results)

	_, err = p.Parse("", []string{"score-version", "3"}, "")
	require.NoError(t, err)
	_, err = p.Parse("", []string{"track", "rate"}, "")
	require.NoError(t, err)
	_, err = p.Parse("", []string{"track-peers", "peers1"}, "")
	require.NoError(t, err)

	results, _, err = p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, "score-version 3", results[0].Data)
	assert.Equal(t, "track rate", results[1].Data)
	assert.Equal(t, "track-peers peers1", results[2].Data)
}

func TestBotMgmtProfile_InsertSetDeleteInit(t *testing.T) {
	p := NewBotMgmtProfile()
	src := &BotMgmtProfileData{ScoreVersion: 5, Track: "x"}
	require.NoError(t, p.Insert(src, 0))
	assert.Same(t, src, p.GetData())

	require.NoError(t, p.Set(&BotMgmtProfileData{ScoreVersion: 9}, 0))
	assert.Equal(t, 9, p.GetData().ScoreVersion)

	require.NoError(t, p.Insert("wrong", 0))
	assert.Equal(t, 9, p.GetData().ScoreVersion)

	require.NoError(t, p.Delete(0))
	assert.Nil(t, p.GetData())

	_, err := p.Parse("", []string{"track", "y"}, "")
	require.NoError(t, err)
	p.Init()
	assert.Nil(t, p.GetData())
}

func TestBotMgmtProfile_PreParseAndGetOne(t *testing.T) {
	p := NewBotMgmtProfile()
	_, err := p.GetOne(0)
	require.Error(t, err)

	_, err = p.Get(false)
	require.Error(t, err)

	pre := []string{"# pre"}
	_, err = p.PreParse("", []string{"track", "rate"}, pre, "")
	require.NoError(t, err)

	got, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, pre, got)

	one, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "rate", one.(*BotMgmtProfileData).Track)

	_, err = p.GetOne(1)
	require.Error(t, err)

	p.SetPreComments([]string{"# other"})
	got, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# other"}, got)
}
