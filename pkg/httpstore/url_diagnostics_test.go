package httpstore

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPSourceDiagnosticsRedactCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "source-user", username)
		assert.Equal(t, "source-password", password)
		assert.Equal(t, "query-token", r.URL.Query().Get("token"))
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	source, err := url.Parse(server.URL + "/rules?token=query-token#private-fragment")
	require.NoError(t, err)
	source.User = url.UserPassword("source-user", "source-password")

	for _, critical := range []bool{false, true} {
		t.Run(map[bool]string{false: "optional", true: "critical"}[critical], func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: levelTrace}))
			store := New(logger, 0)
			_, err := store.Fetch(t.Context(), source.String(), FetchOptions{
				Critical: critical, Retries: 1, RetryDelay: time.Nanosecond,
			}, nil)
			if critical {
				require.Error(t, err)
				output.WriteString(err.Error())
			} else {
				require.NoError(t, err)
			}
			for _, private := range []string{"source-user", "source-password", "query-token", "private-fragment"} {
				assert.NotContains(t, output.String(), private)
			}
			assert.Contains(t, output.String(), server.URL+"/rules")
		})
	}
}

func TestHTTPTransportErrorsRedactSourceURL(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	source := server.URL + "/rules?token=transport-token"
	store := New(slog.Default(), 0)
	content, etag, lastModified, err := store.doFetch(t.Context(), source, FetchOptions{Timeout: time.Second}, nil, "", "")
	require.Error(t, err)
	assert.Empty(t, content)
	assert.Empty(t, etag)
	assert.Empty(t, lastModified)
	assert.NotContains(t, err.Error(), "transport-token")
	assert.Contains(t, err.Error(), "connection refused")
	var transportError *url.Error
	assert.ErrorAs(t, err, &transportError)
}
