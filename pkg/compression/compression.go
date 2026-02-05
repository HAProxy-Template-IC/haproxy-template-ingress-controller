// Package compression provides zstd compression utilities for large configurations.
package compression

import (
	"encoding/base64"
	"sync"

	"github.com/klauspost/compress/zstd"
)

var (
	// Encoder is reused for compression with level 3 (SpeedDefault).
	// Level 3 provides good compression ratio with fast speed.
	encoder     *zstd.Encoder
	encoderOnce sync.Once

	// Decoder is reused for decompression.
	decoder     *zstd.Decoder
	decoderOnce sync.Once
)

func getEncoder() *zstd.Encoder {
	encoderOnce.Do(func() {
		var err error
		encoder, err = zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedDefault),
			zstd.WithEncoderConcurrency(1), // Single encoder: compression is always sequential
		)
		if err != nil {
			panic("failed to create zstd encoder: " + err.Error())
		}
	})
	return encoder
}

func getDecoder() *zstd.Decoder {
	decoderOnce.Do(func() {
		var err error
		decoder, err = zstd.NewReader(nil)
		if err != nil {
			panic("failed to create zstd decoder: " + err.Error())
		}
	})
	return decoder
}

// Compress compresses data using zstd and returns base64-encoded result.
func Compress(data string) (string, error) {
	enc := getEncoder()
	// Pre-allocate output buffer with estimated capacity.
	// Typical zstd compression achieves ~70% ratio, so estimate 70% of input size.
	dataBytes := []byte(data)
	estimatedSize := len(dataBytes) * 7 / 10
	if estimatedSize < 64 {
		estimatedSize = 64 // Minimum buffer to avoid tiny allocations
	}
	compressed := enc.EncodeAll(dataBytes, make([]byte, 0, estimatedSize))
	return base64.StdEncoding.EncodeToString(compressed), nil
}

// Decompress decodes base64 and decompresses zstd data.
func Decompress(data string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}

	dec := getDecoder()
	// Pre-allocate output buffer with estimated capacity.
	// Typical zstd decompression expands ~3x, so estimate 3x input size.
	estimatedSize := len(decoded) * 3
	if estimatedSize < 256 {
		estimatedSize = 256 // Minimum buffer for small inputs
	}
	decompressed, err := dec.DecodeAll(decoded, make([]byte, 0, estimatedSize))
	if err != nil {
		return "", err
	}
	return string(decompressed), nil
}
