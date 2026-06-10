package auxiliaryfiles

import (
	"context"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

const sslCAFileType = "SSL CA file"

// sslStorageOps provides a generic FileOperations implementation for SSL storage files.
type sslStorageOps[T FileItem] struct {
	getAll     func(ctx context.Context) ([]string, error)
	getContent func(ctx context.Context, id string) (string, error)
	create     func(ctx context.Context, id, content string) (string, error)
	update     func(ctx context.Context, id, content string) (string, error)
	delete     func(ctx context.Context, id string) error
}

func (o *sslStorageOps[T]) GetAll(ctx context.Context) ([]string, error) {
	return o.getAll(ctx)
}

func (o *sslStorageOps[T]) GetContent(ctx context.Context, id string) (string, error) {
	return o.getContent(ctx, id)
}

func (o *sslStorageOps[T]) Create(ctx context.Context, id, content string) (string, error) {
	// Normalize to filename only - DataPlane API expects just the filename,
	// not a path with directory components like "ssl/filename.pem".
	// path.Base (not filepath.Base): ids are slash-separated HAProxy target
	// paths regardless of the OS the controller runs on.
	name := path.Base(id)
	reloadID, err := o.create(ctx, name, content)
	if err != nil {
		if isAlreadyExistsError(err) {
			// File already exists, fall back to update instead of failing.
			return o.Update(ctx, id, content)
		}
		if o.recoverFrom500(ctx, err, name, "create") {
			return "", nil
		}
	}
	return reloadID, err
}

func (o *sslStorageOps[T]) Update(ctx context.Context, id, content string) (string, error) {
	// Normalize to filename only - DataPlane API expects just the filename.
	name := path.Base(id)
	reloadID, err := o.update(ctx, name, content)
	if err != nil && o.recoverFrom500(ctx, err, name, "update") {
		return "", nil
	}
	return reloadID, err
}

// recoverFrom500 inspects err and, when it carries a "500" status from
// HAProxy's runtime SSL endpoints, verifies the file is actually present
// (with retries — file creation is asynchronous). Returns true when the
// caller can safely treat the original error as success.
//
// We only check existence, not content, because the API returns
// metadata/fingerprint instead of raw certificate content.
func (o *sslStorageOps[T]) recoverFrom500(ctx context.Context, err error, name, action string) bool {
	if err == nil || !strings.Contains(err.Error(), "500") {
		return false
	}
	if !o.verifyExistsWithRetry(ctx, name) {
		return false
	}
	slog.Debug("SSL CA file "+action+" returned 500 but file exists, treating as success",
		"file", name)
	return true
}

// verifyExistsWithRetry checks if a file exists in storage with retries.
// This is used as a workaround for HAProxy runtime API returning 500 errors
// even when the operation actually succeeds. File creation is asynchronous,
// so we retry a few times to allow the operation to complete.
// We only check existence, not content, because the API returns metadata/
// fingerprint format instead of raw certificate content.
func (o *sslStorageOps[T]) verifyExistsWithRetry(ctx context.Context, name string) bool {
	const maxRetries = 3
	const retryDelay = 500 * time.Millisecond

	for attempt := range maxRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(retryDelay):
			}
		}

		files, err := o.getAll(ctx)
		if err != nil {
			slog.Debug("SSL storage verification: failed to list files",
				"attempt", attempt+1,
				"error", err)
			continue
		}

		if slices.Contains(files, name) {
			return true
		}

		slog.Debug("SSL storage verification: file not found yet",
			"file", name,
			"attempt", attempt+1)
	}

	return false
}

func (o *sslStorageOps[T]) Delete(ctx context.Context, id string) error {
	// Normalize to filename only - DataPlane API expects just the filename.
	name := path.Base(id)
	return o.delete(ctx, name)
}

// sslStorageConfig holds configuration for SSL storage file comparison/sync operations.
type sslStorageConfig struct {
	fileType        string // "SSL CA file" or "SSL CRL file" for logging
	isSupported     func() bool
	detectedVersion func() string
}

// compareSSLStorageFiles is a generic helper for comparing SSL storage files (CA, CRL).
// It handles capability checking, path normalization, and diff restoration.
func compareSSLStorageFiles[T FileItem](
	ctx context.Context,
	desired []T,
	ops FileOperations[T],
	config sslStorageConfig,
	normalize func(T) T,
	newFile func(id, content string) T,
	getPath func(T) string,
) (*FileDiffGeneric[T], error) {
	// Check if storage is supported
	if !config.isSupported() {
		slog.Debug(config.fileType+" storage not supported, skipping comparison",
			"haproxy_version", config.detectedVersion())
		return &FileDiffGeneric[T]{}, nil
	}

	// Normalize desired files to use filenames for identifiers
	normalizedDesired := make([]T, len(desired))
	for i, file := range desired {
		normalizedDesired[i] = normalize(file)
	}

	// Use generic Compare function
	genericDiff, err := Compare[T](ctx, ops, normalizedDesired, newFile)
	if err != nil {
		return nil, err
	}

	// Build map of original desired files keyed by basename so the normalised
	// entries returned from the generic diff can be re-keyed back to the
	// originals (which carry the full caller-supplied paths).
	desiredMap := make(map[string]T)
	for _, file := range desired {
		desiredMap[path.Base(getPath(file))] = file
	}

	return &FileDiffGeneric[T]{
		ToCreate: restoreOriginals(genericDiff.ToCreate, desiredMap, getPath),
		ToUpdate: restoreOriginals(genericDiff.ToUpdate, desiredMap, getPath),
		ToDelete: genericDiff.ToDelete,
	}, nil
}

// syncSSLStorageFiles is a generic helper for syncing SSL storage files (CA, CRL).
// It handles capability checking and delegates to the generic Sync function.
// Returns reload IDs from create/update operations that triggered reloads.
func syncSSLStorageFiles[T FileItem](
	ctx context.Context,
	diff *FileDiffGeneric[T],
	ops FileOperations[T],
	config sslStorageConfig,
) ([]string, error) {
	if diff == nil {
		return nil, nil
	}

	// Check if storage is supported
	if !config.isSupported() {
		if len(diff.ToCreate) > 0 || len(diff.ToUpdate) > 0 || len(diff.ToDelete) > 0 {
			slog.Warn(config.fileType+" storage not supported, skipping sync operations",
				"haproxy_version", config.detectedVersion(),
				"creates", len(diff.ToCreate),
				"updates", len(diff.ToUpdate),
				"deletes", len(diff.ToDelete))
		}
		return nil, nil
	}

	return Sync[T](ctx, ops, diff)
}

// newSSLCaOps creates a FileOperations adapter for SSL CA files.
func newSSLCaOps(c *client.DataplaneClient) *sslStorageOps[SSLCaFile] {
	return &sslStorageOps[SSLCaFile]{
		getAll:     c.GetAllSSLCaFiles,
		getContent: c.GetSSLCaFileContent,
		create:     c.CreateSSLCaFile,
		update:     c.UpdateSSLCaFile,
		delete:     c.DeleteSSLCaFile,
	}
}

// newSSLCaConfig creates configuration for SSL CA file operations.
func newSSLCaConfig(c *client.DataplaneClient) sslStorageConfig {
	return sslStorageConfig{
		fileType:        sslCAFileType,
		isSupported:     func() bool { return c.Capabilities().SupportsSslCaFiles },
		detectedVersion: c.DetectedVersion,
	}
}
