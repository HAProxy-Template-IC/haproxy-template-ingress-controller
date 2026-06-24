package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveRuntimeSSLCertID(t *testing.T) {
	cert := func(desc, storage string) runtimeCert {
		return runtimeCert{description: desc, storageName: storage}
	}

	tests := []struct {
		name    string
		certs   []runtimeCert
		want    string // input name
		wantID  string
		wantErr string
	}{
		{
			name:   "basename match in single-dir layout",
			certs:  []runtimeCert{cert("example_com.pem", "/etc/haproxy/ssl/example_com.pem")},
			want:   "example.com.pem",
			wantID: "/etc/haproxy/ssl/example_com.pem",
		},
		{
			name:   "description match",
			certs:  []runtimeCert{cert("example_com.pem", "")},
			want:   "example.com.pem",
			wantID: "",
		},
		{
			name: "exact storage path wins over basename twins",
			certs: []runtimeCert{
				cert("tls.pem", "ssl/a/tls.pem"),
				cert("tls.pem", "ssl/b/tls.pem"),
			},
			want:   "ssl/b/tls.pem", // sanitized has no dots to change → exact match
			wantID: "ssl/b/tls.pem",
		},
		{
			name: "ambiguous basename errors instead of guessing",
			certs: []runtimeCert{
				cert("tls.pem", "/etc/haproxy/ssl/a/tls.pem"),
				cert("tls.pem", "/etc/haproxy/ssl/b/tls.pem"),
			},
			want:    "tls.pem",
			wantErr: "ambiguous",
		},
		{
			name:    "not loaded",
			certs:   []runtimeCert{cert("other.pem", "/etc/haproxy/ssl/other.pem")},
			want:    "example.com.pem",
			wantErr: "not loaded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRuntimeSSLCertID(tt.certs, tt.want)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, got)
		})
	}
}
