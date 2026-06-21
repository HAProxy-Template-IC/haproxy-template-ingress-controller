// Package compression provides zstd compression utilities for large configurations.
package compression

import (
	"encoding/base64"

	"github.com/klauspost/compress/zstd"
)

// encoder is reused for compression with level 3 (SpeedDefault), which provides
// a good compression ratio at fast speed. decoder is reused for decompression.
// Both are safe for concurrent use by the zstd package.
var (
	encoder *zstd.Encoder
	decoder *zstd.Decoder
)

func init() {
	var err error
	encoder, err = zstd.NewWriter(nil,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1), // Single encoder: compression is always sequential
	)
	if err != nil {
		panic("failed to create zstd encoder: " + err.Error())
	}

	decoder, err = zstd.NewReader(nil)
	if err != nil {
		panic("failed to create zstd decoder: " + err.Error())
	}
}

// Compress compresses data using zstd and returns the base64-encoded result.
func Compress(data string) string {
	compressed := encoder.EncodeAll([]byte(data), nil)
	return base64.StdEncoding.EncodeToString(compressed)
}

// Decompress decodes base64 and decompresses zstd data.
func Decompress(data string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}

	decompressed, err := decoder.DecodeAll(decoded, nil)
	if err != nil {
		return "", err
	}
	return string(decompressed), nil
}
