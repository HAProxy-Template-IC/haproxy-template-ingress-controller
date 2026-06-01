package dataplane

import (
	"cmp"
	"slices"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// Endpoint represents HAProxy Dataplane API connection information.
type Endpoint struct {
	// URL is the Dataplane API endpoint (e.g., "http://haproxy:5555/v3").
	// A trailing "/v2" or "/v3" is stripped before the v3 version-detection probe;
	// only v3.x operations are supported (no v2-only endpoints).
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

// AuxiliaryFiles contains files to synchronize alongside configuration changes.
// Diffs are applied in two of the three sync phases (see SyncPhase in phases.go):
//   - PhasePreConfig:  creates and updates (files must exist before config references them)
//   - PhasePostConfig: deletes (safe to remove only after config stops referencing them)
type AuxiliaryFiles struct {
	// GeneralFiles contains general-purpose files (error pages, custom response files, etc.)
	GeneralFiles []auxiliaryfiles.GeneralFile

	// SSLCertificates contains SSL certificates to sync to HAProxy SSL storage
	SSLCertificates []auxiliaryfiles.SSLCertificate

	// SSLCaFiles contains SSL CA certificate files for client/backend certificate verification
	SSLCaFiles []auxiliaryfiles.SSLCaFile

	// MapFiles contains map files for backend routing and other map-based features
	MapFiles []auxiliaryfiles.MapFile

	// CRTListFiles contains crt-list files for SSL certificate lists with per-certificate options
	CRTListFiles []auxiliaryfiles.CRTListFile
}

// Sort sorts all auxiliary file slices in-place by their path/filename.
// This establishes a deterministic order so that downstream consumers
// (checksum, diff, etc.) can iterate directly without cloning or sorting.
func (a *AuxiliaryFiles) Sort() {
	slices.SortFunc(a.GeneralFiles, func(x, y auxiliaryfiles.GeneralFile) int {
		return cmp.Compare(x.Filename, y.Filename)
	})
	slices.SortFunc(a.SSLCertificates, func(x, y auxiliaryfiles.SSLCertificate) int {
		return cmp.Compare(x.Path, y.Path)
	})
	slices.SortFunc(a.SSLCaFiles, func(x, y auxiliaryfiles.SSLCaFile) int {
		return cmp.Compare(x.Path, y.Path)
	})
	slices.SortFunc(a.MapFiles, func(x, y auxiliaryfiles.MapFile) int {
		return cmp.Compare(x.Path, y.Path)
	})
	slices.SortFunc(a.CRTListFiles, func(x, y auxiliaryfiles.CRTListFile) int {
		return cmp.Compare(x.Path, y.Path)
	})
}

// SyncOptions configures synchronization behavior.
type SyncOptions struct {
	// Timeout for the entire sync operation (default: 2 minutes)
	Timeout time.Duration

	// VerifyReload enables async reload verification after sync (default: true)
	// When true, polls the reload status endpoint until succeeded/failed/timeout.
	// Disable for dry-run or when reload verification is not needed.
	VerifyReload bool

	// ReloadVerificationTimeout is the maximum time to wait for reload verification (default: 10s)
	// This should be set higher than the DataPlane API's reload-delay setting.
	// Only used when VerifyReload is true.
	ReloadVerificationTimeout time.Duration

	// PreParsedConfig is an optional pre-parsed desired configuration. When
	// non-nil, sync skips parsing the desiredConfig string.
	PreParsedConfig *parserconfig.StructuredConfig

	// CachedCurrentConfig is an optional cached parsed current configuration
	// from a previous sync. When set with CachedConfigVersion, sync calls
	// GetVersion() first and reuses the cached config if the version matches.
	CachedCurrentConfig *parserconfig.StructuredConfig

	// CachedConfigVersion is the expected config version on the pod. Only
	// used when CachedCurrentConfig is also set.
	CachedConfigVersion int64

	// ContentChecksum is the pre-computed checksum of the desired config +
	// aux files. When it matches LastDeployedChecksum, the expensive aux
	// file comparison is skipped because the desired state is identical
	// to what was last deployed.
	ContentChecksum string

	// LastDeployedChecksum is the content checksum from the last successful
	// sync to this endpoint. Drift prevention syncs leave this empty.
	LastDeployedChecksum string
}

// DefaultSyncOptions returns sensible default sync options.
func DefaultSyncOptions() *SyncOptions {
	return &SyncOptions{
		Timeout:                   2 * time.Minute,
		VerifyReload:              true,
		ReloadVerificationTimeout: 10 * time.Second,
	}
}

// DryRunOptions returns options configured for dry-run mode.
func DryRunOptions() *SyncOptions {
	return &SyncOptions{
		Timeout:      1 * time.Minute,
		VerifyReload: false, // No reload happens in dry-run
	}
}

// DefaultAuxiliaryFiles returns an empty auxiliary files struct.
func DefaultAuxiliaryFiles() *AuxiliaryFiles {
	return &AuxiliaryFiles{}
}
