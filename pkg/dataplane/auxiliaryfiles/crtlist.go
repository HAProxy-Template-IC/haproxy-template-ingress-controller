package auxiliaryfiles

import (
	"context"
	"log/slog"
	"path"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// CRTListsToGeneralFiles converts CRT-list files to general files for storage.
// CRT-list files are stored as general files because the native CRT-list API
// (POST ssl_crt_lists) triggers a reload without supporting skip_reload parameter.
// General file CREATE returns 201 without triggering a reload.
//
// This is also used by the orchestrator to merge CRT-lists into the unified
// general files comparison, since both are stored in general file storage.
//
// IMPORTANT: Filenames are sanitized using client.SanitizeStorageName() to match
// how the HAProxy Dataplane API stores them. Without sanitization, comparison
// would fail because desired files (e.g., "example.com.txt") wouldn't match
// current files (e.g., "example_com.txt" - dots replaced with underscores).
func CRTListsToGeneralFiles(crtLists []CRTListFile) []GeneralFile {
	generalFiles := make([]GeneralFile, len(crtLists))
	for i, crtList := range crtLists {
		// Use the base filename as the identifier, sanitized to match API storage
		filename := path.Base(crtList.Path)
		sanitizedFilename := client.SanitizeStorageName(filename)
		generalFiles[i] = GeneralFile{
			Filename: sanitizedFilename,
			Content:  crtList.Content,
		}
	}
	return generalFiles
}

// convertCRTListDiffToFileDiff converts a CRTListDiff to a FileDiff for general file storage.
// Note: ToDelete paths are already sanitized filenames returned by the comparison,
// so they don't need additional sanitization here.
func convertCRTListDiffToFileDiff(crtListDiff *CRTListDiff) *FileDiff {
	return &FileDiff{
		ToCreate: CRTListsToGeneralFiles(crtListDiff.ToCreate),
		ToUpdate: CRTListsToGeneralFiles(crtListDiff.ToUpdate),
		ToDelete: crtListDiff.ToDelete, // Already sanitized from API response
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
// We normalize using path.Base() for comparison.
//
// Storage strategy: Always uses general file storage instead of native CRT-list API.
// The native CRT-list API (POST ssl_crt_lists) triggers a reload but doesn't support
// the skip_reload parameter. General file CREATE returns 201 without triggering a reload,
// allowing us to batch all changes into a single reload during config sync.
func CompareCRTLists(ctx context.Context, c *client.DataplaneClient, desired []CRTListFile) (*CRTListDiff, error) {
	// Always use general file storage for CRT-lists to avoid reload on create.
	// Native CRT-list API triggers reload without skip_reload support.
	// General file storage returns 201 (no reload), reducing total reloads.
	slog.Debug("using general file storage for CRT-lists to avoid reload on create")

	// Convert CRT-list files to general files for storage
	generalFiles := CRTListsToGeneralFiles(desired)

	// Compare using general file storage
	generalDiff, err := CompareGeneralFiles(ctx, c, generalFiles)
	if err != nil {
		return nil, err
	}

	return &CRTListDiff{
		ToCreate: generalFilesToCRTLists(generalDiff.ToCreate),
		ToUpdate: generalFilesToCRTLists(generalDiff.ToUpdate),
		ToDelete: generalDiff.ToDelete,
	}, nil
}

// generalFilesToCRTLists is the inverse of CRTListsToGeneralFiles: it rebuilds
// CRTListFile entries from their general-file representation, treating the
// general file's Filename (which is the sanitized basename used as the API id)
// as the CRT-list path.
func generalFilesToCRTLists(items []GeneralFile) []CRTListFile {
	out := make([]CRTListFile, len(items))
	for i, gf := range items {
		out[i] = CRTListFile{
			Path:    gf.Filename,
			Content: gf.Content,
		}
	}
	return out
}

// SyncCRTLists synchronizes crt-list files to the desired state by applying
// the provided diff. This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// The caller is responsible for splitting the diff into these phases.
// Returns reload IDs from create/update operations that triggered reloads.
//
// Storage strategy: Always uses general file storage instead of native CRT-list API.
// The native CRT-list API (POST ssl_crt_lists) triggers a reload but doesn't support
// the skip_reload parameter. General file CREATE returns 201 without triggering a reload,
// allowing us to batch all changes into a single reload during config sync.
func SyncCRTLists(ctx context.Context, c *client.DataplaneClient, diff *CRTListDiff) ([]string, error) {
	if diff == nil {
		return nil, nil
	}

	// Always use general file storage for CRT-lists to avoid reload on create.
	// Native CRT-list API triggers reload without skip_reload support.
	// General file storage returns 201 (no reload), reducing total reloads.
	slog.Debug("using general file storage for CRT-lists sync to avoid reload on create")

	// Convert CRT-list diff to general file diff
	generalDiff := convertCRTListDiffToFileDiff(diff)

	// Sync using general file storage
	return SyncGeneralFiles(ctx, c, generalDiff)
}
