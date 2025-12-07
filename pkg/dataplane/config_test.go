package dataplane

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEndpoint_HasCachedVersion(t *testing.T) {
	tests := []struct {
		name     string
		endpoint Endpoint
		want     bool
	}{
		{
			name:     "empty endpoint has no cached version",
			endpoint: Endpoint{},
			want:     false,
		},
		{
			name: "endpoint with zero major version has no cached version",
			endpoint: Endpoint{
				DetectedMajorVersion: 0,
				DetectedMinorVersion: 2,
			},
			want: false,
		},
		{
			name: "endpoint with major version has cached version",
			endpoint: Endpoint{
				DetectedMajorVersion: 3,
				DetectedMinorVersion: 2,
				DetectedFullVersion:  "v3.2.6 87ad0bcf",
			},
			want: true,
		},
		{
			name: "endpoint with major version 1 has cached version",
			endpoint: Endpoint{
				DetectedMajorVersion: 1,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.endpoint.HasCachedVersion()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEndpoint_Redacted(t *testing.T) {
	endpoint := Endpoint{
		URL:          "http://haproxy:5555",
		Username:     "admin",
		Password:     "secretpassword123",
		PodName:      "haproxy-pod-0",
		PodNamespace: "default",
	}

	redacted := endpoint.Redacted()

	require.NotNil(t, redacted)
	assert.Equal(t, "http://haproxy:5555", redacted["url"])
	assert.Equal(t, "admin", redacted["username"])
	assert.Equal(t, "***REDACTED***", redacted["password"])
	assert.Equal(t, "haproxy-pod-0", redacted["pod"])
	assert.NotContains(t, redacted["password"], "secret")
}

func TestDefaultSyncOptions(t *testing.T) {
	opts := DefaultSyncOptions()

	require.NotNil(t, opts)
	assert.Equal(t, 3, opts.MaxRetries)
	assert.Equal(t, 2*time.Minute, opts.Timeout)
	assert.False(t, opts.ContinueOnError)
	assert.True(t, opts.FallbackToRaw)
}

func TestDryRunOptions(t *testing.T) {
	opts := DryRunOptions()

	require.NotNil(t, opts)
	assert.Equal(t, 0, opts.MaxRetries)
	assert.Equal(t, 1*time.Minute, opts.Timeout)
	assert.False(t, opts.ContinueOnError)
	assert.False(t, opts.FallbackToRaw)
}

func TestDefaultAuxiliaryFiles(t *testing.T) {
	aux := DefaultAuxiliaryFiles()

	require.NotNil(t, aux)
	assert.Nil(t, aux.GeneralFiles)
	assert.Nil(t, aux.SSLCertificates)
	assert.Nil(t, aux.MapFiles)
	assert.Nil(t, aux.CRTListFiles)
}
