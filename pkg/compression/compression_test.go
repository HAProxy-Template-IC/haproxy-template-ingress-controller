package compression

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressDecompress(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{
			name: "simple string",
			data: "Hello, World!",
		},
		{
			name: "HAProxy config sample",
			data: `global
    log stdout len 4096 local0 info
    daemon

defaults
    mode http
    log global
    option httplog
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http-in
    bind *:80
    default_backend servers

backend servers
    server srv1 10.0.0.1:8080 check
`,
		},
		{
			name: "large repetitive content",
			data: strings.Repeat("backend server_", 1000) + strings.Repeat("check weight 100\n", 1000),
		},
		{
			name: "empty string",
			data: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := Compress(tt.data)
			require.NoError(t, err)

			decompressed, err := Decompress(compressed)
			require.NoError(t, err)

			assert.Equal(t, tt.data, decompressed)
		})
	}
}

func TestCompressionRatio(t *testing.T) {
	// HAProxy configs are highly compressible due to repetitive patterns
	largeConfig := strings.Repeat(`backend service_`, 100) +
		strings.Repeat(`
    server SRV_1 10.0.0.1:8080 check weight 100
    server SRV_2 10.0.0.2:8080 check weight 100
`, 500)

	compressed, err := Compress(largeConfig)
	require.NoError(t, err)

	originalSize := len(largeConfig)
	compressedSize := len(compressed)
	ratio := float64(compressedSize) / float64(originalSize)

	t.Logf("Original: %d bytes, Compressed: %d bytes, Ratio: %.2f%%", originalSize, compressedSize, ratio*100)

	// zstd should achieve at least 50% compression on repetitive content
	assert.Less(t, ratio, 0.5, "compression ratio should be better than 50%%")
}

func TestDecompressInvalidBase64(t *testing.T) {
	_, err := Decompress("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDecompressInvalidZstd(t *testing.T) {
	// Valid base64 but not valid zstd data
	_, err := Decompress("SGVsbG8gV29ybGQ=") // "Hello World" in base64
	assert.Error(t, err)
}
