package auxiliaryfiles

import (
	"context"
	"log/slog"
	"path"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// CompareSSLCaFiles compares the current state of SSL CA files in HAProxy storage
// with the desired state, and returns a diff describing what needs to be created,
// updated, or deleted.
//
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
// If the API version doesn't support CA file storage, returns an empty diff.
//
// Strategy:
//  1. Check if SSL CA file storage is supported
//  2. Fetch current CA file names from the Dataplane API
//  3. Download content for each current CA file
//  4. Compare content with desired CA files
//  5. Return diff with create, update, and delete operations
//
// Path normalization: The API returns filenames only (e.g., "ca-bundle.pem"), but SSLCaFile.Path
// may contain full paths (e.g., "/etc/haproxy/ssl/ca/ca-bundle.pem"). We normalize using path.Base()
// for comparison (slash-only — these are HAProxy target paths regardless of host OS).
func CompareSSLCaFiles(ctx context.Context, c *client.DataplaneClient, desired []SSLCaFile) (*SSLCaFileDiff, error) {
	// Check if SSL CA file storage is supported.
	if !c.Capabilities().SupportsSslCaFiles {
		slog.Debug(sslCAFileType+" storage not supported, skipping comparison",
			"haproxy_version", c.DetectedVersion())
		return &SSLCaFileDiff{}, nil
	}

	// Normalize desired files to use filenames for identifiers.
	normalizedDesired := make([]SSLCaFile, len(desired))
	for i, file := range desired {
		normalizedDesired[i] = SSLCaFile{
			Path:    path.Base(file.Path),
			Content: file.Content,
		}
	}

	// Use generic Compare function.
	genericDiff, err := Compare(ctx, newSSLCaOps(c), normalizedDesired,
		func(id, content string) SSLCaFile {
			return SSLCaFile{Path: id, Content: content}
		})
	if err != nil {
		return nil, err
	}

	// Build map of original desired files keyed by basename so the normalised
	// entries returned from the generic diff can be re-keyed back to the
	// originals (which carry the full caller-supplied paths).
	desiredMap := make(map[string]SSLCaFile)
	for _, file := range desired {
		desiredMap[path.Base(file.Path)] = file
	}

	getPath := func(f SSLCaFile) string { return f.Path }
	return &SSLCaFileDiff{
		ToCreate: restoreOriginals(genericDiff.ToCreate, desiredMap, getPath),
		ToUpdate: restoreOriginals(genericDiff.ToUpdate, desiredMap, getPath),
		ToDelete: genericDiff.ToDelete,
	}, nil
}

// SyncSSLCaFiles synchronizes SSL CA files to the desired state by applying
// the provided diff. This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// SSL CA file storage is only available in HAProxy DataPlane API v3.2+.
// If the API version doesn't support CA file storage, operations are skipped with a warning.
//
// The caller is responsible for splitting the diff into these phases.
// Returns reload IDs from create/update operations that triggered reloads.
func SyncSSLCaFiles(ctx context.Context, c *client.DataplaneClient, diff *SSLCaFileDiff) ([]string, error) {
	if diff == nil {
		return nil, nil
	}

	// Check if SSL CA file storage is supported.
	if !c.Capabilities().SupportsSslCaFiles {
		if len(diff.ToCreate) > 0 || len(diff.ToUpdate) > 0 || len(diff.ToDelete) > 0 {
			slog.Warn(sslCAFileType+" storage not supported, skipping sync operations",
				"haproxy_version", c.DetectedVersion(),
				"creates", len(diff.ToCreate),
				"updates", len(diff.ToUpdate),
				"deletes", len(diff.ToDelete))
		}
		return nil, nil
	}

	return Sync(ctx, newSSLCaOps(c), diff)
}
