package httpstore

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactURL(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		want string
	}{
		{name: "public", url: "https://example.com/rules", want: "https://example.com/rules"},
		{name: "credentials", url: "https://user:password@example.com/rules?token=value#fragment", want: "https://example.com/rules"},
		{name: "username token", url: "https://token@example.com/rules", want: "https://example.com/rules"},
		{name: "encoded path", url: "https://example.com/a%2Fb?key=value", want: "https://example.com/a%2Fb"},
		{name: "malformed", url: "https://user:password@%xx?key=value", want: "[invalid URL]"},
		{name: "relative", url: "/rules?key=value", want: "[invalid URL]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, RedactURL(tc.url))
		})
	}
}

func TestRedactURLError(t *testing.T) {
	source := "https://user:password@example.com/rules?token=value"
	cause := &url.Error{Op: "Get", URL: source, Err: context.DeadlineExceeded}
	err := redactURLError(fmt.Errorf("fetching source: %w", cause), source)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var transportError *url.Error
	require.ErrorAs(t, err, &transportError)
	assert.Same(t, cause, transportError)
	assert.Equal(t, `fetching source: Get "https://example.com/rules": context deadline exceeded`, err.Error())
	assert.Nil(t, redactURLError(nil, source))

	err = redactURLError(fmt.Errorf("redirect to %q rejected", "https://redirect:password@other.example/rules?token=other"), source)
	assert.EqualError(t, err, `redirect to "https://other.example/rules" rejected`)
}

func TestRedactQuotedURLErrors(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		{`https://user:password@example.com/rules?token=private"quoted-value`, "https://example.com/rules"},
		{`https://user:password@example.com/rules?token=private\escaped-value`, "https://example.com/rules"},
		{"https://user:password@example.com/rules?token=private\nnewline-value", "[invalid URL]"},
	} {
		cause := &url.Error{Op: "Get", URL: tc.source, Err: context.DeadlineExceeded}
		assert.EqualError(t, redactURLError(cause, tc.source), fmt.Sprintf("Get %q: context deadline exceeded", tc.want))
		assert.EqualError(t, redactURLError(fmt.Errorf("redirect to %q rejected", tc.source), "https://origin.example/rules"),
			fmt.Sprintf("redirect to %q rejected", tc.want))
	}
}
