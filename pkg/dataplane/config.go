package dataplane

import (
	"cmp"
	"path"
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

	// PodUID distinguishes replacements that reuse the same namespace and name.
	PodUID string

	// PodRuntimeID distinguishes container restarts and image changes within one pod UID.
	PodRuntimeID string

	// DataPlane API version info cached after discovery admission.
	// Zero values indicate version not yet detected.
	DetectedMajorVersion int    // Major version (e.g., 3)
	DetectedMinorVersion int    // Minor version (e.g., 2)
	DetectedFullVersion  string // Full version string (e.g., "v3.2.6 87ad0bcf")
}

// HasCachedVersion returns true if version info has been cached on this endpoint.
func (e *Endpoint) HasCachedVersion() bool {
	return e.DetectedMajorVersion > 0
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

// CurrentFiles projects CRD-backed auxiliary output into the currentFiles map.
// Secret-backed certificate and CA content is deliberately excluded.
func (af *AuxiliaryFiles) CurrentFiles() map[string]string {
	if af == nil {
		return nil
	}
	m := make(map[string]string, len(af.MapFiles)+len(af.GeneralFiles)+len(af.CRTListFiles))
	for _, f := range af.MapFiles {
		m[path.Base(f.Path)] = f.Content
	}
	for _, f := range af.GeneralFiles {
		m[path.Base(f.Path)] = f.Content
	}
	for _, f := range af.CRTListFiles {
		m[path.Base(f.Path)] = f.Content
	}
	return m
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
	// from a previous sync. Reuse also requires CachedConfigVersion and matching
	// cached-current and last-activated checksums.
	CachedCurrentConfig *parserconfig.StructuredConfig

	// CachedConfigVersion is the expected config version on the pod. Only
	// used when CachedCurrentConfig is also set.
	CachedConfigVersion int64

	// CachedCurrentConfigChecksum is activationChecksum() of the raw config
	// represented by CachedCurrentConfig. A cache hit also requires this to
	// match LastActivatedConfigChecksum; otherwise sync fetches the raw config.
	CachedCurrentConfigChecksum string

	// ContentChecksum is the pre-computed checksum of the desired config +
	// aux files. When it matches LastDeployedChecksum, the expensive aux
	// file comparison is skipped because the desired state is identical
	// to what was last deployed.
	ContentChecksum string

	// LastDeployedChecksum is the content checksum from the last successful
	// sync to this endpoint. Drift prevention syncs leave this empty.
	LastDeployedChecksum string

	// LastActivatedConfigChecksum is configTextChecksum() of the on-disk config
	// this endpoint was last PROVEN to be running: written by a reload-coupled
	// push whose reload was verified, or by a runtime apply whose actions the
	// live worker accepted. Empty means "never proven", which is not the same as
	// "unchanged".
	//
	// It is what makes an empty diff trustworthy. "Disk == desired" says nothing
	// about the running worker, because a skip_version push writes the body
	// VERBATIM without a reload — and the dataplane writes it even when the
	// accompanying runtime actions fail. Structural content can therefore sit
	// parked on disk that no worker ever loaded, while desired-vs-disk reads
	// empty and the deploy reports success (#112: new TCP listeners parked for
	// 90s, Gateway reported Programmed, every connection refused).
	//
	// The previous guard keyed on the `# _version=N` header instead, which is a
	// proxy for the same question and answers it wrong in both directions: it
	// misses content parked by a VERSIONED skip_reload push whose follow-up
	// force_reload failed, and it fires on a headerless config that a runtime
	// apply did legitimately activate.
	LastActivatedConfigChecksum string

	// RestampVersionHeader (SyncRuntimeFast only) re-writes the pushed body
	// WITH a `# _version=N` header after a successful pure-runtime apply, via
	// one versioned skip_reload push. A skip_version push leaves the on-disk
	// config headerless, and sync() refuses to trust an empty diff against a
	// headerless config (it forces a reload to activate potentially parked
	// content). Re-stamping proves "disk == running state" so the next
	// structural sync stays reload-free.
	//
	// Callers must set this ONLY when no structural deploy can be in flight
	// on the pod (the deployer's authoritative runtime-raw lane dispatch).
	// A fast-track partial apply racing an in-flight structural reload must
	// leave the config headerless: its `set server` actions can land on the
	// worker the reload replaces, and a re-stamped header would let the next
	// sync trust an empty diff over that lost update.
	RestampVersionHeader bool

	// RenderSuperseded (SyncRuntimeFast only) reports that a newer render now
	// exists for this endpoint. The bounded retry-across-reload loop consults
	// it between attempts and abandons the push when it returns true: a body
	// derived from a superseded render must not keep re-pushing across a
	// reload window when a fresher render is already pending — the 50+×
	// identical stale-body storms of issue #84. Nil means never superseded.
	// Must be safe to call from any goroutine.
	RenderSuperseded func() bool
}

// DefaultSyncOptions returns sensible default sync options.
func DefaultSyncOptions() *SyncOptions {
	return &SyncOptions{
		Timeout:                   2 * time.Minute,
		VerifyReload:              true,
		ReloadVerificationTimeout: 10 * time.Second,
	}
}

// DefaultAuxiliaryFiles returns an empty auxiliary files struct.
func DefaultAuxiliaryFiles() *AuxiliaryFiles {
	return &AuxiliaryFiles{}
}
