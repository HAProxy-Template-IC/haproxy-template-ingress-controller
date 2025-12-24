package auxiliaryfiles

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane/client"
)

// crtListFileOps implements FileOperations for CRTListFile.
type crtListFileOps struct {
	client *client.DataplaneClient
}

func (o *crtListFileOps) GetAll(ctx context.Context) ([]string, error) {
	// NOTE: API returns filenames only (e.g., "certificate-list.txt"), not absolute paths.
	// Comparison logic in CompareCRTLists() handles path normalization.
	return o.client.GetAllCRTListFiles(ctx)
}

func (o *crtListFileOps) GetContent(ctx context.Context, id string) (string, error) {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	return o.client.GetCRTListFileContent(ctx, filename)
}

func (o *crtListFileOps) Create(ctx context.Context, id, content string) (string, error) {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	reloadID, err := o.client.CreateCRTListFile(ctx, filename, content)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		// CRT-list file already exists, fall back to update instead of failing.
		// This handles the case where a previous deployment partially succeeded
		// or where path normalization causes comparison mismatches.
		return o.Update(ctx, id, content)
	}
	return reloadID, err
}

func (o *crtListFileOps) Update(ctx context.Context, id, content string) (string, error) {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	return o.client.UpdateCRTListFile(ctx, filename, content)
}

func (o *crtListFileOps) Delete(ctx context.Context, id string) error {
	// Extract filename from path (API expects filename only)
	filename := filepath.Base(id)
	return o.client.DeleteCRTListFile(ctx, filename)
}

// convertCRTListsToGeneralFiles converts CRT-list files to general files for fallback storage.
// This is used when CRT-list storage is not supported (HAProxy <3.2) to store the files
// as general files instead.
func convertCRTListsToGeneralFiles(crtLists []CRTListFile) []GeneralFile {
	generalFiles := make([]GeneralFile, len(crtLists))
	for i, crtList := range crtLists {
		// Use the base filename as the identifier
		filename := filepath.Base(crtList.Path)
		generalFiles[i] = GeneralFile{
			Filename: filename,
			Content:  crtList.Content,
		}
	}
	return generalFiles
}

// convertCRTListDiffToFileDiff converts a CRTListDiff to a FileDiff for general file storage.
func convertCRTListDiffToFileDiff(crtListDiff *CRTListDiff) *FileDiff {
	return &FileDiff{
		ToCreate: convertCRTListsToGeneralFiles(crtListDiff.ToCreate),
		ToUpdate: convertCRTListsToGeneralFiles(crtListDiff.ToUpdate),
		ToDelete: crtListDiff.ToDelete, // Delete paths remain the same
	}
}

// CompareCRTLists compares the current state of crt-list files in HAProxy storage
// with the desired state, and returns a diff describing what needs to be created,
// updated, or deleted.
//
// This function:
//  1. Fetches all current crt-list file names from the Dataplane API
//  2. Downloads content for each current crt-list file
//  3. Compares with the desired crt-list files list
//  4. Returns a CRTListDiff with operations needed to reach desired state
//
// Path normalization: The API returns filenames only (e.g., "certificate-list.txt"), but
// CRTListFile.Path may contain full paths (e.g., "/etc/haproxy/certs/certificate-list.txt").
// We normalize using filepath.Base() for comparison.
//
// Version compatibility: CRT-list storage is only available in HAProxy DataPlane API v3.2+.
// On older versions (3.0, 3.1), this function automatically falls back to general file storage.
func CompareCRTLists(ctx context.Context, c *client.DataplaneClient, desired []CRTListFile) (*CRTListDiff, error) {
	// Check if CRT-list storage is supported by this HAProxy version
	if !c.Capabilities().SupportsCrtList {
		slog.Info("CRT-list storage not supported, using general file storage fallback",
			"haproxy_version", c.DetectedVersion())

		// Convert CRT-list files to general files for fallback storage
		generalFiles := convertCRTListsToGeneralFiles(desired)

		// Compare using general file storage
		generalDiff, err := CompareGeneralFiles(ctx, c, generalFiles)
		if err != nil {
			return nil, err
		}

		// Convert general file diff back to CRT-list diff format
		crtListDiff := &CRTListDiff{
			ToCreate: make([]CRTListFile, len(generalDiff.ToCreate)),
			ToUpdate: make([]CRTListFile, len(generalDiff.ToUpdate)),
			ToDelete: generalDiff.ToDelete,
		}

		// Convert general files back to CRT-list files (restore Path format)
		for i, gf := range generalDiff.ToCreate {
			crtListDiff.ToCreate[i] = CRTListFile{
				Path:    gf.Filename, // Use filename as path
				Content: gf.Content,
			}
		}
		for i, gf := range generalDiff.ToUpdate {
			crtListDiff.ToUpdate[i] = CRTListFile{
				Path:    gf.Filename,
				Content: gf.Content,
			}
		}

		return crtListDiff, nil
	}

	// CRT-list storage is supported - use native CRT-list API
	// Normalize desired crt-lists to use filenames for identifiers
	normalizedDesired := make([]CRTListFile, len(desired))
	for i, crtList := range desired {
		normalizedDesired[i] = CRTListFile{
			Path:    filepath.Base(crtList.Path),
			Content: crtList.Content,
		}
	}

	ops := &crtListFileOps{client: c}

	// Use generic Compare function with normalized paths
	genericDiff, err := Compare[CRTListFile](
		ctx,
		ops,
		normalizedDesired,
		func(id, content string) CRTListFile {
			return CRTListFile{Path: id, Content: content}
		},
	)
	if err != nil {
		return nil, err
	}

	// Convert generic diff to CRTListDiff
	// Note: For CRT-list files, we need to use original desired files (with full paths)
	// for Create/Update operations, but use normalized paths for Delete operations
	desiredMap := make(map[string]CRTListFile)
	for _, crtList := range desired {
		desiredMap[filepath.Base(crtList.Path)] = crtList
	}

	diff := &CRTListDiff{
		ToCreate: make([]CRTListFile, 0, len(genericDiff.ToCreate)),
		ToUpdate: make([]CRTListFile, 0, len(genericDiff.ToUpdate)),
		ToDelete: genericDiff.ToDelete,
	}

	// Restore original paths for create operations
	for _, crtList := range genericDiff.ToCreate {
		if original, exists := desiredMap[crtList.Path]; exists {
			diff.ToCreate = append(diff.ToCreate, original)
		}
	}

	// Restore original paths for update operations
	for _, crtList := range genericDiff.ToUpdate {
		if original, exists := desiredMap[crtList.Path]; exists {
			diff.ToUpdate = append(diff.ToUpdate, original)
		}
	}

	return diff, nil
}

// SyncCRTLists synchronizes crt-list files to the desired state by applying
// the provided diff. This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// The caller is responsible for splitting the diff into these phases.
// Returns reload IDs from create/update operations that triggered reloads.
//
// Version compatibility: CRT-list storage is only available in HAProxy DataPlane API v3.2+.
// On older versions (3.0, 3.1), this function automatically falls back to general file storage.
func SyncCRTLists(ctx context.Context, c *client.DataplaneClient, diff *CRTListDiff) ([]string, error) {
	if diff == nil {
		return nil, nil
	}

	// Check if CRT-list storage is supported by this HAProxy version
	if !c.Capabilities().SupportsCrtList {
		slog.Info("CRT-list storage not supported, using general file storage fallback for sync",
			"haproxy_version", c.DetectedVersion())

		// Convert CRT-list diff to general file diff
		generalDiff := convertCRTListDiffToFileDiff(diff)

		// Sync using general file storage
		return SyncGeneralFiles(ctx, c, generalDiff)
	}

	// CRT-list storage is supported - use native CRT-list API
	ops := &crtListFileOps{client: c}

	// Convert CRTListDiff to generic diff
	genericDiff := &FileDiffGeneric[CRTListFile]{
		ToCreate: diff.ToCreate,
		ToUpdate: diff.ToUpdate,
		ToDelete: diff.ToDelete,
	}

	// Use generic Sync function
	return Sync[CRTListFile](ctx, ops, genericDiff)
}
