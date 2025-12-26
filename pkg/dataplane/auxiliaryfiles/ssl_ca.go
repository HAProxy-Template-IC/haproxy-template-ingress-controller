package auxiliaryfiles

import (
	"context"
	"path/filepath"

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
// may contain full paths (e.g., "/etc/haproxy/ssl/ca/ca-bundle.pem"). We normalize using filepath.Base()
// for comparison.
func CompareSSLCaFiles(ctx context.Context, c *client.DataplaneClient, desired []SSLCaFile) (*SSLCaFileDiff, error) {
	ops := newSSLCaOps(c)
	config := newSSLCaConfig(c)

	genericDiff, err := compareSSLStorageFiles(
		ctx,
		desired,
		ops,
		config,
		func(f SSLCaFile) SSLCaFile {
			return SSLCaFile{
				Path:    filepath.Base(f.Path),
				Content: f.Content,
			}
		},
		func(id, content string) SSLCaFile {
			return SSLCaFile{Path: id, Content: content}
		},
		func(f SSLCaFile) string { return f.Path },
	)
	if err != nil {
		return nil, err
	}

	return &SSLCaFileDiff{
		ToCreate: genericDiff.ToCreate,
		ToUpdate: genericDiff.ToUpdate,
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

	ops := newSSLCaOps(c)
	config := newSSLCaConfig(c)

	genericDiff := &FileDiffGeneric[SSLCaFile]{
		ToCreate: diff.ToCreate,
		ToUpdate: diff.ToUpdate,
		ToDelete: diff.ToDelete,
	}

	return syncSSLStorageFiles(ctx, genericDiff, ops, config)
}
