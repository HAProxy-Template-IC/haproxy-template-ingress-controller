package client

import (
	"context"
	"fmt"
	"path"
)

// ReplaceRuntimeSSLCaFiles replaces the live (in-memory) contents of one or more
// already-loaded SSL CA files (mTLS trust bundles) on the worker via the runtime
// API (`add ssl ca-file` + `commit ssl ca-file`, which replaces the file with the
// payload — see AddRuntimeCaFileEntry for why `add` is used instead of `set`),
// WITHOUT a reload. contentByPath maps each ca-file's config path (the
// `ca-file <path>` argument HAProxy loaded) to its new bundle. Available on
// DataPlane API v3.2+ only — callers must gate on Capabilities().SupportsSslCaFiles.
//
// The loaded-ca-file list is fetched ONCE and reused to resolve every file's
// runtime identifier, so N rotations cost a single list fetch. Like
// ReplaceRuntimeMap / ReplaceRuntimeSSLCerts, disk durability is left to the
// orchestrator's pre-config general-storage write (skip_reload); this call only
// updates the live worker's memory.
//
// This is the controller-side equivalent of what the SPIFFE cert-reloader
// sidecar does over the raw stats socket (`set ssl ca-file <bundle>`), routed
// through the DataPlane API instead.
func (c *DataplaneClient) ReplaceRuntimeSSLCaFiles(ctx context.Context, contentByPath map[string]string) error {
	if len(contentByPath) == 0 {
		return nil
	}

	loaded, err := c.GetAllSSLCaFiles(ctx)
	if err != nil {
		return err
	}
	for caPath, content := range contentByPath {
		ident, err := resolveRuntimeCaFileID(loaded, caPath)
		if err != nil {
			return err
		}
		// AddRuntimeCaFileEntry (POST /runtime/ssl_ca_files/{name}/entries) does
		// `add ssl ca-file` + commit, which replaces the live file with the
		// payload (empty starting transaction). We deliberately do NOT use
		// set ssl ca-file — its DataPlane API runtime path returns 500 under the
		// master-worker socket wrapping (see AddRuntimeCaFileEntry).
		if err := c.AddRuntimeCaFileEntry(ctx, ident, content); err != nil {
			return fmt.Errorf("replacing runtime ssl ca-file '%s': %w", caPath, err)
		}
	}
	return nil
}

// resolveRuntimeCaFileID returns the identifier HAProxy reports for the loaded
// ca-file matching the desired config path. An exact match against the loaded
// list wins; otherwise an UNAMBIGUOUS basename match is the fallback; otherwise
// it errors so the caller reloads (which converges via the pre-config disk
// write) rather than addressing the wrong file. Pure function so the matching is
// unit-tested directly.
func resolveRuntimeCaFileID(loaded []string, caPath string) (string, error) {
	for _, name := range loaded {
		if name == caPath {
			return name, nil
		}
	}

	want := path.Base(caPath)
	var match string
	matches := 0
	for _, name := range loaded {
		if path.Base(name) == want {
			match = name
			matches++
		}
	}
	switch {
	case matches == 1:
		return match, nil
	case matches > 1:
		return "", fmt.Errorf("runtime ssl ca-file %q is ambiguous: %d loaded files share that basename", caPath, matches)
	default:
		return "", fmt.Errorf("runtime ssl ca-file %q is not loaded", caPath)
	}
}
