package auxiliaryfiles

import (
	"context"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// newGeneralFileOps wires the generic clientFileOps helper to the DataplaneClient
// methods that operate on general file storage.
func newGeneralFileOps(c *client.DataplaneClient) FileOperations[GeneralFile] {
	return &clientFileOps[GeneralFile]{
		getAll:     c.GetAllGeneralFiles,
		getContent: c.GetGeneralFileContent,
		create:     c.CreateGeneralFile,
		update:     c.UpdateGeneralFile,
		deleteFn:   c.DeleteGeneralFile,
	}
}

// CompareGeneralFiles compares the current state of general files in HAProxy storage
// with the desired state, and returns a diff describing what needs to be created,
// updated, or deleted.
//
// This function:
//  1. Fetches all current file paths from the Dataplane API
//  2. Downloads content for each current file
//  3. Compares with the desired files list
//  4. Returns a FileDiff with operations needed to reach desired state
func CompareGeneralFiles(ctx context.Context, c *client.DataplaneClient, desired []GeneralFile) (*FileDiff, error) {
	return Compare[GeneralFile](ctx, newGeneralFileOps(c), desired, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})
}

// SyncGeneralFiles synchronizes general files to the desired state by applying
// the provided diff. This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// The caller is responsible for splitting the diff into these phases.
// Returns reload IDs from create/update operations that triggered reloads.
func SyncGeneralFiles(ctx context.Context, c *client.DataplaneClient, diff *FileDiff) ([]string, error) {
	if diff == nil {
		return nil, nil
	}
	return Sync[GeneralFile](ctx, newGeneralFileOps(c), diff)
}
