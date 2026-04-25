package auxiliaryfiles

import (
	"context"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// newMapFileOps wires the generic clientFileOps helper to the DataplaneClient
// methods that operate on map file storage.
func newMapFileOps(c *client.DataplaneClient) FileOperations[MapFile] {
	return &clientFileOps[MapFile]{
		getAll:     c.GetAllMapFiles,
		getContent: c.GetMapFileContent,
		create:     c.CreateMapFile,
		update:     c.UpdateMapFile,
		deleteFn:   c.DeleteMapFile,
	}
}

// CompareMapFiles compares the current state of map files in HAProxy storage
// with the desired state, and returns a diff describing what needs to be created,
// updated, or deleted.
//
// This function:
//  1. Fetches all current map file names from the Dataplane API
//  2. Downloads content for each current map file
//  3. Compares with the desired map files list
//  4. Returns a MapFileDiff with operations needed to reach desired state
func CompareMapFiles(ctx context.Context, c *client.DataplaneClient, desired []MapFile) (*MapFileDiff, error) {
	return Compare[MapFile](ctx, newMapFileOps(c), desired, func(id, content string) MapFile {
		return MapFile{Path: id, Content: content}
	})
}

// SyncMapFiles synchronizes map files to the desired state by applying
// the provided diff. This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// The caller is responsible for splitting the diff into these phases.
// Returns reload IDs from create/update operations that triggered reloads.
func SyncMapFiles(ctx context.Context, c *client.DataplaneClient, diff *MapFileDiff) ([]string, error) {
	if diff == nil {
		return nil, nil
	}
	return Sync[MapFile](ctx, newMapFileOps(c), diff)
}
