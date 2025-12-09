package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenizeLine(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantParts   []string
		wantComment string
	}{
		{
			name:        "simple directive",
			line:        "mode http",
			wantParts:   []string{"mode", "http"},
			wantComment: "",
		},
		{
			name:        "directive with comment",
			line:        "mode http # HTTP mode",
			wantParts:   []string{"mode", "http"},
			wantComment: "HTTP mode",
		},
		{
			name:        "quoted string",
			line:        `server s1 127.0.0.1:8080 check "my server"`,
			wantParts:   []string{"server", "s1", "127.0.0.1:8080", "check", `"my server"`},
			wantComment: "",
		},
		{
			name:        "hash in quoted string",
			line:        `option httpchk GET "/health#check"`,
			wantParts:   []string{"option", "httpchk", "GET", `"/health#check"`},
			wantComment: "",
		},
		{
			name:        "section header",
			line:        "frontend http-in",
			wantParts:   []string{"frontend", "http-in"},
			wantComment: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parts, comment := tokenizeLine(tc.line)
			assert.Equal(t, tc.wantParts, parts)
			assert.Equal(t, tc.wantComment, comment)
		})
	}
}

func TestReader_ProcessString_SectionDetection(t *testing.T) {
	config := `
global
    daemon

frontend http-in
    mode http
    bind *:80

backend servers
    server s1 127.0.0.1:8080
`

	factory := NewDefaultFactory()
	reader := NewReader(factory)
	err := reader.ProcessString(config)
	require.NoError(t, err)

	parsers := reader.GetParsers()

	// Verify sections were created
	assert.NotNil(t, parsers.Global, "global section should exist")
	assert.Contains(t, parsers.Frontend, "http-in", "frontend http-in should exist")
	assert.Contains(t, parsers.Backend, "servers", "backend servers should exist")
}

func TestReader_ProcessString_EESections(t *testing.T) {
	config := `
global
    daemon

waf-global
    rules-file /etc/haproxy/waf/core.rules

waf-profile security-api
    rules-file /etc/haproxy/waf/api.rules

udp-lb dns-servers
    balance roundrobin
    server dns1 192.168.1.10:53

botmgmt-profile myprofile
    score-version 1

captcha recaptcha
    provider recaptcha
`

	factory := NewDefaultFactory()
	reader := NewReader(factory)
	err := reader.ProcessString(config)
	require.NoError(t, err)

	parsers := reader.GetParsers()

	// Verify EE sections were created
	assert.NotNil(t, parsers.WAFGlobal, "waf-global section should exist")
	assert.Contains(t, parsers.WAFProfile, "security-api", "waf-profile security-api should exist")
	assert.Contains(t, parsers.UDPLB, "dns-servers", "udp-lb dns-servers should exist")
	assert.Contains(t, parsers.BotMgmtProfile, "myprofile", "botmgmt-profile myprofile should exist")
	assert.Contains(t, parsers.Captcha, "recaptcha", "captcha recaptcha should exist")
}

func TestReader_ProcessString_MixedCEandEE(t *testing.T) {
	config := `
global
    daemon

waf-global
    rules-file /etc/haproxy/waf/core.rules

frontend http-in
    mode http
    bind *:80
    default_backend servers

udp-lb dns
    balance roundrobin

backend servers
    server s1 127.0.0.1:8080
`

	factory := NewDefaultFactory()
	reader := NewReader(factory)
	err := reader.ProcessString(config)
	require.NoError(t, err)

	parsers := reader.GetParsers()

	// Verify both CE and EE sections were created
	assert.NotNil(t, parsers.Global, "global section should exist")
	assert.NotNil(t, parsers.WAFGlobal, "waf-global section should exist")
	assert.Contains(t, parsers.Frontend, "http-in", "frontend http-in should exist")
	assert.Contains(t, parsers.UDPLB, "dns", "udp-lb dns should exist")
	assert.Contains(t, parsers.Backend, "servers", "backend servers should exist")
}

func TestSplitFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple",
			input: "mode http",
			want:  []string{"mode", "http"},
		},
		{
			name:  "multiple spaces",
			input: "mode    http",
			want:  []string{"mode", "http"},
		},
		{
			name:  "tabs",
			input: "mode\thttp",
			want:  []string{"mode", "http"},
		},
		{
			name:  "double quoted",
			input: `acl is_api path_beg "/api"`,
			want:  []string{"acl", "is_api", "path_beg", `"/api"`},
		},
		{
			name:  "single quoted",
			input: `acl is_api path_beg '/api'`,
			want:  []string{"acl", "is_api", "path_beg", `'/api'`},
		},
		{
			name:  "quoted with spaces",
			input: `option httpchk GET "/health check"`,
			want:  []string{"option", "httpchk", "GET", `"/health check"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitFields(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsInQuotes(t *testing.T) {
	tests := []struct {
		name string
		line string
		pos  int
		want bool
	}{
		{
			name: "not in quotes",
			line: `mode http # comment`,
			pos:  10,
			want: false,
		},
		{
			name: "in double quotes",
			line: `path "/api#check"`,
			pos:  10,
			want: true,
		},
		{
			name: "after closing quote",
			line: `path "/api" # comment`,
			pos:  12,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isInQuotes(tc.line, tc.pos)
			assert.Equal(t, tc.want, got)
		})
	}
}
