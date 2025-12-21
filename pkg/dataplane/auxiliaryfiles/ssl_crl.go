package auxiliaryfiles

import (
	"context"
	"path/filepath"

	"haproxy-template-ic/pkg/dataplane/client"
)

// CompareSSLCrlFiles compares the current state of SSL CRL files in HAProxy storage
// with the desired state, and returns a diff describing what needs to be created,
// updated, or deleted.
//
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
// If the API version doesn't support CRL file storage, returns an empty diff.
//
// Strategy:
//  1. Check if SSL CRL file storage is supported
//  2. Fetch current CRL file names from the Dataplane API
//  3. Download content for each current CRL file
//  4. Compare content with desired CRL files
//  5. Return diff with create, update, and delete operations
//
// Path normalization: The API returns filenames only (e.g., "revoked.crl"), but SSLCrlFile.Path
// may contain full paths (e.g., "/etc/haproxy/ssl/crl/revoked.crl"). We normalize using filepath.Base()
// for comparison.
func CompareSSLCrlFiles(ctx context.Context, c *client.DataplaneClient, desired []SSLCrlFile) (*SSLCrlFileDiff, error) {
	ops := newSSLCrlOps(c)
	config := newSSLCrlConfig(c)

	genericDiff, err := compareSSLStorageFiles(
		ctx,
		desired,
		ops,
		config,
		func(f SSLCrlFile) SSLCrlFile {
			return SSLCrlFile{
				Path:    filepath.Base(f.Path),
				Content: f.Content,
			}
		},
		func(id, content string) SSLCrlFile {
			return SSLCrlFile{Path: id, Content: content}
		},
		func(f SSLCrlFile) string { return f.Path },
	)
	if err != nil {
		return nil, err
	}

	return &SSLCrlFileDiff{
		ToCreate: genericDiff.ToCreate,
		ToUpdate: genericDiff.ToUpdate,
		ToDelete: genericDiff.ToDelete,
	}, nil
}

// SyncSSLCrlFiles synchronizes SSL CRL files to the desired state by applying
// the provided diff. This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// SSL CRL file storage is only available in HAProxy DataPlane API v3.2+.
// If the API version doesn't support CRL file storage, operations are skipped with a warning.
//
// The caller is responsible for splitting the diff into these phases.
// Returns reload IDs from create/update operations that triggered reloads.
func SyncSSLCrlFiles(ctx context.Context, c *client.DataplaneClient, diff *SSLCrlFileDiff) ([]string, error) {
	if diff == nil {
		return nil, nil
	}

	ops := newSSLCrlOps(c)
	config := newSSLCrlConfig(c)

	genericDiff := &FileDiffGeneric[SSLCrlFile]{
		ToCreate: diff.ToCreate,
		ToUpdate: diff.ToUpdate,
		ToDelete: diff.ToDelete,
	}

	return syncSSLStorageFiles(ctx, genericDiff, ops, config)
}
