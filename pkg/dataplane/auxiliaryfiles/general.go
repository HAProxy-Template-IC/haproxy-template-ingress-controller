package auxiliaryfiles

import (
	"context"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// generalFileOps implements FileOperations for GeneralFile.
type generalFileOps struct {
	client *client.DataplaneClient
}

func (o *generalFileOps) GetAll(ctx context.Context) ([]string, error) {
	return o.client.GetAllGeneralFiles(ctx)
}

func (o *generalFileOps) GetContent(ctx context.Context, id string) (string, error) {
	return o.client.GetGeneralFileContent(ctx, id)
}

func (o *generalFileOps) Create(ctx context.Context, id, content string) (string, error) {
	return o.client.CreateGeneralFile(ctx, id, content)
}

func (o *generalFileOps) Update(ctx context.Context, id, content string) (string, error) {
	return o.client.UpdateGeneralFile(ctx, id, content)
}

func (o *generalFileOps) Delete(ctx context.Context, id string) error {
	return o.client.DeleteGeneralFile(ctx, id)
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
	ops := &generalFileOps{client: c}

	// Use generic Compare function
	genericDiff, err := Compare[GeneralFile](
		ctx,
		ops,
		desired,
		func(id, content string) GeneralFile {
			return GeneralFile{Filename: id, Content: content}
		},
	)
	if err != nil {
		return nil, err
	}

	// Convert generic diff to FileDiff
	return &FileDiff{
		ToCreate: genericDiff.ToCreate,
		ToUpdate: genericDiff.ToUpdate,
		ToDelete: genericDiff.ToDelete,
	}, nil
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

	ops := &generalFileOps{client: c}

	// Convert FileDiff to generic diff
	genericDiff := &FileDiffGeneric[GeneralFile]{
		ToCreate: diff.ToCreate,
		ToUpdate: diff.ToUpdate,
		ToDelete: diff.ToDelete,
	}

	// Use generic Sync function
	return Sync[GeneralFile](ctx, ops, genericDiff)
}
