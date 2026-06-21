package client

import "errors"

// Sentinel errors for capability checks.
// These are returned when an operation is attempted on a DataPlane API version
// that does not support the required feature.

var (
	// ErrCrtListRequiresV32 is returned when crt-list storage operations are attempted
	// on DataPlane API versions below v3.2.
	ErrCrtListRequiresV32 = errors.New("crt-list storage requires DataPlane API v3.2+")

	// ErrSSLCaFilesRequireV32 is returned when SSL CA file operations are attempted
	// on DataPlane API versions below v3.2.
	ErrSSLCaFilesRequireV32 = errors.New("SSL CA file storage requires DataPlane API v3.2+")
)
