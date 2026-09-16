package compression

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecompressSizeBoundary(t *testing.T) {
	const limit = 64 << 20
	for _, size := range []int{limit, limit + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			content := strings.Repeat("x", size)
			got, err := Decompress(Compress(content))
			if size > limit {
				require.ErrorIs(t, err, zstd.ErrDecoderSizeExceeded)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, content, got)
		})
	}
}

func TestDecompressLimitsCombinedFrames(t *testing.T) {
	var frame bytes.Buffer
	stream, err := zstd.NewWriter(&frame, zstd.WithEncoderConcurrency(1))
	require.NoError(t, err)
	_, err = stream.Write([]byte(strings.Repeat("x", 32<<20)))
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	var header zstd.Header
	require.NoError(t, header.Decode(frame.Bytes()))
	require.False(t, header.HasFCS)
	joined := bytes.Repeat(frame.Bytes(), 3)
	got, err := Decompress(base64.StdEncoding.EncodeToString(joined))
	require.ErrorIs(t, err, zstd.ErrDecoderSizeExceeded)
	assert.Empty(t, got)
}

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
			compressed := Compress(tt.data)

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

	compressed := Compress(largeConfig)

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
