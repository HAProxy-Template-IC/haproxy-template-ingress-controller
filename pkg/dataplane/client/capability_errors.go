package client

import "errors"

// Sentinel errors for capability checks.
// These are returned when an operation is attempted on a DataPlane API version
// that does not support the required feature.

var (
	// ErrCrtListRequiresV32 is returned when crt-list storage operations are attempted
	// on DataPlane API versions below v3.2.
	ErrCrtListRequiresV32 = errors.New("crt-list storage requires DataPlane API v3.2+")

	// ErrCrtListEntriesRequireV32 is returned when crt-list entry operations are attempted
	// on DataPlane API versions below v3.2.
	ErrCrtListEntriesRequireV32 = errors.New("crt-list entries require DataPlane API v3.2+")

	// ErrCrtLoadsRequireV32 is returned when crt-load operations are attempted
	// on DataPlane API versions below v3.2.
	ErrCrtLoadsRequireV32 = errors.New("crt-loads require DataPlane API v3.2+")

	// ErrSSLCaFilesRequireV32 is returned when SSL CA file operations are attempted
	// on DataPlane API versions below v3.2.
	ErrSSLCaFilesRequireV32 = errors.New("SSL CA file storage requires DataPlane API v3.2+")

	// ErrSSLCrlFilesRequireV32 is returned when SSL CRL file operations are attempted
	// on DataPlane API versions below v3.2.
	ErrSSLCrlFilesRequireV32 = errors.New("SSL CRL file storage requires DataPlane API v3.2+")

	// ErrLogProfilesRequireV31 is returned when log profile operations are attempted
	// on DataPlane API versions below v3.1.
	ErrLogProfilesRequireV31 = errors.New("log profiles require DataPlane API v3.1+")

	// ErrTracesRequireV31 is returned when trace operations are attempted
	// on DataPlane API versions below v3.1.
	ErrTracesRequireV31 = errors.New("traces require DataPlane API v3.1+")

	// ErrFeatureRequiresV32 is returned when a v3.2+ only feature is dispatched
	// to an older API version.
	ErrFeatureRequiresV32 = errors.New("this feature requires DataPlane API v3.2+")

	// ErrFeatureRequiresV31 is returned when a v3.1+ only feature is dispatched
	// to an older API version.
	ErrFeatureRequiresV31 = errors.New("this feature requires DataPlane API v3.1+")
)
