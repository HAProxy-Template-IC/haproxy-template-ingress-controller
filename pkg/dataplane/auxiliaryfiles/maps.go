package auxiliaryfiles

import (
	"context"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
)

// mapFileOps implements FileOperations for MapFile.
type mapFileOps struct {
	client *client.DataplaneClient
}

func (o *mapFileOps) GetAll(ctx context.Context) ([]string, error) {
	return o.client.GetAllMapFiles(ctx)
}

func (o *mapFileOps) GetContent(ctx context.Context, id string) (string, error) {
	return o.client.GetMapFileContent(ctx, id)
}

func (o *mapFileOps) Create(ctx context.Context, id, content string) (string, error) {
	return o.client.CreateMapFile(ctx, id, content)
}

func (o *mapFileOps) Update(ctx context.Context, id, content string) (string, error) {
	return o.client.UpdateMapFile(ctx, id, content)
}

func (o *mapFileOps) Delete(ctx context.Context, id string) error {
	return o.client.DeleteMapFile(ctx, id)
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
	ops := &mapFileOps{client: c}

	// Use generic Compare function
	genericDiff, err := Compare[MapFile](
		ctx,
		ops,
		desired,
		func(id, content string) MapFile {
			return MapFile{Path: id, Content: content}
		},
	)
	if err != nil {
		return nil, err
	}

	// Convert generic diff to MapFileDiff
	return &MapFileDiff{
		ToCreate: genericDiff.ToCreate,
		ToUpdate: genericDiff.ToUpdate,
		ToDelete: genericDiff.ToDelete,
	}, nil
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

	ops := &mapFileOps{client: c}

	// Convert MapFileDiff to generic diff
	genericDiff := &FileDiffGeneric[MapFile]{
		ToCreate: diff.ToCreate,
		ToUpdate: diff.ToUpdate,
		ToDelete: diff.ToDelete,
	}

	// Use generic Sync function
	return Sync[MapFile](ctx, ops, genericDiff)
}
