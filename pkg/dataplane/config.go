package dataplane

import (
	"time"

	"haproxy-template-ic/pkg/dataplane/auxiliaryfiles"
)

// Endpoint represents HAProxy Dataplane API connection information.
type Endpoint struct {
	// URL is the Dataplane API endpoint (e.g., "http://haproxy:5555/v2")
	URL string

	// Username for basic authentication
	Username string

	// Password for basic authentication
	Password string

	// PodName is the Kubernetes pod name (for observability)
	PodName string

	// PodNamespace is the Kubernetes pod namespace (for observability)
	PodNamespace string

	// Version info (cached after discovery admission, avoids redundant /v3/info calls)
	// Zero values indicate version not yet detected.
	DetectedMajorVersion int    // Major version (e.g., 3)
	DetectedMinorVersion int    // Minor version (e.g., 2)
	DetectedFullVersion  string // Full version string (e.g., "v3.2.6 87ad0bcf")
}

// HasCachedVersion returns true if version info has been cached on this endpoint.
func (e *Endpoint) HasCachedVersion() bool {
	return e.DetectedMajorVersion > 0
}

// Redacted returns a redacted version of the endpoint for safe logging.
// Credentials are masked to prevent exposure in logs.
func (e *Endpoint) Redacted() map[string]string {
	return map[string]string{
		"url":      e.URL,
		"username": e.Username,
		"password": "***REDACTED***",
		"pod":      e.PodName,
	}
}

// AuxiliaryFiles contains files to synchronize before configuration changes.
// These files are synced in two phases:
//   - Phase 1 (pre-config): Creates and updates are applied before config sync
//   - Phase 2 (post-config): Deletes are applied after successful config sync
type AuxiliaryFiles struct {
	// GeneralFiles contains general-purpose files (error pages, custom response files, etc.)
	GeneralFiles []auxiliaryfiles.GeneralFile

	// SSLCertificates contains SSL certificates to sync to HAProxy SSL storage
	SSLCertificates []auxiliaryfiles.SSLCertificate

	// MapFiles contains map files for backend routing and other map-based features
	MapFiles []auxiliaryfiles.MapFile

	// CRTListFiles contains crt-list files for SSL certificate lists with per-certificate options
	CRTListFiles []auxiliaryfiles.CRTListFile
}

// SyncOptions configures synchronization behavior.
type SyncOptions struct {
	// MaxRetries for 409 version conflict errors (default: 3)
	// These are always retried as they're recoverable errors.
	MaxRetries int

	// Timeout for the entire sync operation (default: 2 minutes)
	Timeout time.Duration

	// ContinueOnError continues applying operations even if some fail (default: false)
	// When false, the first error stops execution.
	ContinueOnError bool

	// FallbackToRaw enables automatic fallback to raw config push on non-409 errors (default: true)
	// When enabled, if fine-grained sync fails with non-recoverable errors,
	// the library automatically falls back to pushing the complete raw configuration.
	FallbackToRaw bool

	// VerifyReload enables async reload verification after sync (default: true)
	// When true, polls the reload status endpoint until succeeded/failed/timeout.
	// Disable for dry-run or when reload verification is not needed.
	VerifyReload bool

	// ReloadVerificationTimeout is the maximum time to wait for reload verification (default: 10s)
	// This should be set higher than the DataPlane API's reload-delay setting.
	// Only used when VerifyReload is true.
	ReloadVerificationTimeout time.Duration
}

// DefaultSyncOptions returns sensible default sync options.
func DefaultSyncOptions() *SyncOptions {
	return &SyncOptions{
		MaxRetries:                3,
		Timeout:                   2 * time.Minute,
		ContinueOnError:           false,
		FallbackToRaw:             true,
		VerifyReload:              true,
		ReloadVerificationTimeout: 10 * time.Second,
	}
}

// DryRunOptions returns options configured for dry-run mode.
func DryRunOptions() *SyncOptions {
	return &SyncOptions{
		MaxRetries:      0,
		Timeout:         1 * time.Minute,
		ContinueOnError: false,
		FallbackToRaw:   false,
		VerifyReload:    false, // No reload happens in dry-run
	}
}

// DefaultAuxiliaryFiles returns an empty auxiliary files struct.
func DefaultAuxiliaryFiles() *AuxiliaryFiles {
	return &AuxiliaryFiles{}
}
