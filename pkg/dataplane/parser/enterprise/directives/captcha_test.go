package directives

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptcha_Parse(t *testing.T) {
	tests := []struct {
		name    string
		parts   []string
		wantErr bool
		assert  func(*testing.T, *CaptchaData)
	}{
		{
			name:  "provider",
			parts: []string{"provider", "recaptcha"},
			assert: func(t *testing.T, d *CaptchaData) {
				t.Helper()
				assert.Equal(t, "recaptcha", d.Provider)
			},
		},
		{
			name:  "public-key",
			parts: []string{"public-key", "pubkey123"},
			assert: func(t *testing.T, d *CaptchaData) {
				t.Helper()
				assert.Equal(t, "pubkey123", d.PublicKey)
			},
		},
		{
			name:  "secret-key",
			parts: []string{"secret-key", "sec456"},
			assert: func(t *testing.T, d *CaptchaData) {
				t.Helper()
				assert.Equal(t, "sec456", d.SecretKey)
			},
		},
		{
			name:  "html-file",
			parts: []string{"html-file", "/etc/haproxy/captcha.html"},
			assert: func(t *testing.T, d *CaptchaData) {
				t.Helper()
				assert.Equal(t, "/etc/haproxy/captcha.html", d.HTMLFile)
			},
		},
		{
			name:    "unknown directive",
			parts:   []string{"weird", "value"},
			wantErr: true,
		},
		{
			name:    "too few parts",
			parts:   []string{"provider"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewCaptcha()
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

func TestCaptcha_GetParserName(t *testing.T) {
	assert.Equal(t, "captcha", NewCaptcha().GetParserName())
}

func TestCaptcha_ResultAll(t *testing.T) {
	p := NewCaptcha()
	results, _, err := p.ResultAll()
	require.NoError(t, err)
	assert.Empty(t, results)

	for _, parts := range [][]string{
		{"provider", "recaptcha"},
		{"public-key", "pub"},
		{"secret-key", "sec"},
		{"html-file", "/captcha.html"},
	} {
		_, err := p.Parse("", parts, "")
		require.NoError(t, err)
	}

	results, _, err = p.ResultAll()
	require.NoError(t, err)
	require.Len(t, results, 4)
	assert.Equal(t, "provider recaptcha", results[0].Data)
	assert.Equal(t, "public-key pub", results[1].Data)
	assert.Equal(t, "secret-key sec", results[2].Data)
	assert.Equal(t, "html-file /captcha.html", results[3].Data)
}

func TestCaptcha_InsertSetDeleteInit(t *testing.T) {
	p := NewCaptcha()
	src := &CaptchaData{Provider: "hcaptcha"}
	require.NoError(t, p.Insert(src, 0))
	assert.Same(t, src, p.GetData())

	require.NoError(t, p.Set(&CaptchaData{Provider: "other"}, 0))
	assert.Equal(t, "other", p.GetData().Provider)

	require.NoError(t, p.Insert("wrong", 0))
	assert.Equal(t, "other", p.GetData().Provider)

	require.NoError(t, p.Delete(0))
	assert.Nil(t, p.GetData())

	_, err := p.Parse("", []string{"provider", "x"}, "")
	require.NoError(t, err)
	p.Init()
	assert.Nil(t, p.GetData())
}

func TestCaptcha_PreParseAndGetOne(t *testing.T) {
	p := NewCaptcha()
	_, err := p.Get(false)
	require.Error(t, err)

	pre := []string{"# cap"}
	_, err = p.PreParse("", []string{"provider", "recaptcha"}, pre, "")
	require.NoError(t, err)

	got, err := p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, pre, got)

	one, err := p.GetOne(0)
	require.NoError(t, err)
	assert.Equal(t, "recaptcha", one.(*CaptchaData).Provider)

	_, err = p.GetOne(5)
	require.Error(t, err)

	p.SetPreComments([]string{"# y"})
	got, err = p.GetPreComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"# y"}, got)
}
