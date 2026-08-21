package dataplane

import (
	"testing"

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

func TestDefaultAuxiliaryFiles(t *testing.T) {
	aux := DefaultAuxiliaryFiles()

	require.NotNil(t, aux)
	assert.Nil(t, aux.GeneralFiles)
	assert.Nil(t, aux.SSLCertificates)
	assert.Nil(t, aux.MapFiles)
	assert.Nil(t, aux.CRTListFiles)
}
