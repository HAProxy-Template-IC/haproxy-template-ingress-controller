package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRuntimeCaFileID(t *testing.T) {
	tests := []struct {
		name    string
		loaded  []string
		caPath  string
		want    string
		wantErr string
	}{
		{
			name:   "exact path match",
			loaded: []string{"general/ca.crt", "ssl/x.pem"},
			caPath: "general/ca.crt",
			want:   "general/ca.crt",
		},
		{
			name:   "basename match when config path differs from loaded path",
			loaded: []string{"/etc/haproxy/general/btls-ns-pol.crt"},
			caPath: "general/btls-ns-pol.crt",
			want:   "/etc/haproxy/general/btls-ns-pol.crt",
		},
		{
			name:    "ambiguous basename errors (caller reloads)",
			loaded:  []string{"general/a/ca.crt", "general/b/ca.crt"},
			caPath:  "general/ca.crt", // sanitized has no exact match
			wantErr: "ambiguous",
		},
		{
			name:    "not loaded errors (caller reloads)",
			loaded:  []string{"general/other.crt"},
			caPath:  "general/ca.crt",
			wantErr: "not loaded",
		},
		{
			name:   "exact match preferred over a basename twin",
			loaded: []string{"general/ca.crt", "general/sub/ca.crt"},
			caPath: "general/ca.crt",
			want:   "general/ca.crt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRuntimeCaFileID(tt.loaded, tt.caPath)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// infoOK answers the version-detection probe so newTestClientWithHandler's
// client initializes as v3.2 (SupportsSslCaFiles=true).
func infoOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"api":{"version":"%s"}}`, testAPIVersion)
}

const caEntriesSuffix = "/entries"

func TestAddRuntimeCaFileEntry(t *testing.T) {
	t.Run("success: 201 with multipart POST", func(t *testing.T) {
		var gotMethod, gotContentType string
		client, cleanup := newTestClientWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v3/info" {
				infoOK(w)
				return
			}
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, caEntriesSuffix) {
				gotMethod = r.Method
				gotContentType = r.Header.Get("Content-Type")
				w.WriteHeader(http.StatusCreated)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		})
		defer cleanup()

		err := client.AddRuntimeCaFileEntry(context.Background(), "ca.crt", "PEM")
		require.NoError(t, err)
		assert.Equal(t, http.MethodPost, gotMethod)
		assert.True(t, strings.HasPrefix(gotContentType, "multipart/form-data"),
			"runtime ca-file add must use multipart form-data, got %q", gotContentType)
	})

	t.Run("error: 500 surfaces so the orchestrator falls back to reload", func(t *testing.T) {
		client, cleanup := newTestClientWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v3/info" {
				infoOK(w)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer cleanup()

		err := client.AddRuntimeCaFileEntry(context.Background(), "ca.crt", "PEM")
		require.Error(t, err)
	})
}

func TestReplaceRuntimeSSLCaFiles(t *testing.T) {
	const listPath = "/services/haproxy/runtime/ssl_ca_files"

	t.Run("fetches the loaded list once, adds each entry", func(t *testing.T) {
		var listCalls, postCalls int64
		client, cleanup := newTestClientWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v3/info":
				infoOK(w)
			case r.Method == http.MethodGet && r.URL.Path == listPath:
				atomic.AddInt64(&listCalls, 1)
				jsonResponse(`[{"storage_name":"ca1.crt"},{"storage_name":"ca2.crt"}]`)(w, r)
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, caEntriesSuffix):
				atomic.AddInt64(&postCalls, 1)
				w.WriteHeader(http.StatusCreated)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
		defer cleanup()

		err := client.ReplaceRuntimeSSLCaFiles(context.Background(), map[string]string{
			"ca1.crt": "PEM-1",
			"ca2.crt": "PEM-2",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(1), atomic.LoadInt64(&listCalls), "loaded list must be fetched exactly once")
		assert.Equal(t, int64(2), atomic.LoadInt64(&postCalls), "one add-entry per ca-file")
	})

	t.Run("unresolvable path surfaces the resolver error (caller reloads)", func(t *testing.T) {
		var postCalls int64
		client, cleanup := newTestClientWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v3/info":
				infoOK(w)
			case r.Method == http.MethodGet && r.URL.Path == listPath:
				jsonResponse(`[{"storage_name":"other.crt"}]`)(w, r)
			case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, caEntriesSuffix):
				atomic.AddInt64(&postCalls, 1)
				w.WriteHeader(http.StatusCreated)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		})
		defer cleanup()

		err := client.ReplaceRuntimeSSLCaFiles(context.Background(), map[string]string{
			"ca-missing.crt": "PEM",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not loaded")
		assert.Equal(t, int64(0), atomic.LoadInt64(&postCalls), "no entry written when the file can't be resolved")
	})
}
