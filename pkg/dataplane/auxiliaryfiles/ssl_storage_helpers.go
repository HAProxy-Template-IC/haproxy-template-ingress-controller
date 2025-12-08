package auxiliaryfiles

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"haproxy-template-ic/pkg/dataplane/client"
)

// sslStorageOps provides a generic FileOperations implementation for SSL storage files (CA, CRL).
// This reduces duplication between ssl_ca.go and ssl_crl.go.
type sslStorageOps[T FileItem] struct {
	getAll     func(ctx context.Context) ([]string, error)
	getContent func(ctx context.Context, id string) (string, error)
	create     func(ctx context.Context, id, content string) error
	update     func(ctx context.Context, id, content string) error
	delete     func(ctx context.Context, id string) error
}

func (o *sslStorageOps[T]) GetAll(ctx context.Context) ([]string, error) {
	return o.getAll(ctx)
}

func (o *sslStorageOps[T]) GetContent(ctx context.Context, id string) (string, error) {
	return o.getContent(ctx, id)
}

func (o *sslStorageOps[T]) Create(ctx context.Context, id, content string) error {
	err := o.create(ctx, id, content)
	if err != nil && strings.Contains(err.Error(), "already exists") {
		// File already exists, fall back to update instead of failing.
		return o.Update(ctx, id, content)
	}
	return err
}

func (o *sslStorageOps[T]) Update(ctx context.Context, id, content string) error {
	return o.update(ctx, id, content)
}

func (o *sslStorageOps[T]) Delete(ctx context.Context, id string) error {
	return o.delete(ctx, id)
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
		slog.Info(config.fileType+" storage not supported, skipping comparison",
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

	// Build map of original desired files for path restoration
	desiredMap := make(map[string]T)
	for _, file := range desired {
		desiredMap[filepath.Base(getPath(file))] = file
	}

	// Create result diff with restored original paths
	result := &FileDiffGeneric[T]{
		ToCreate: make([]T, 0, len(genericDiff.ToCreate)),
		ToUpdate: make([]T, 0, len(genericDiff.ToUpdate)),
		ToDelete: genericDiff.ToDelete,
	}

	// Restore original paths for create operations
	for _, file := range genericDiff.ToCreate {
		if original, exists := desiredMap[getPath(file)]; exists {
			result.ToCreate = append(result.ToCreate, original)
		}
	}

	// Restore original paths for update operations
	for _, file := range genericDiff.ToUpdate {
		if original, exists := desiredMap[getPath(file)]; exists {
			result.ToUpdate = append(result.ToUpdate, original)
		}
	}

	return result, nil
}

// syncSSLStorageFiles is a generic helper for syncing SSL storage files (CA, CRL).
// It handles capability checking and delegates to the generic Sync function.
func syncSSLStorageFiles[T FileItem](
	ctx context.Context,
	diff *FileDiffGeneric[T],
	ops FileOperations[T],
	config sslStorageConfig,
) error {
	if diff == nil {
		return nil
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
		return nil
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

// newSSLCrlOps creates a FileOperations adapter for SSL CRL files.
func newSSLCrlOps(c *client.DataplaneClient) *sslStorageOps[SSLCrlFile] {
	return &sslStorageOps[SSLCrlFile]{
		getAll:     c.GetAllSSLCrlFiles,
		getContent: c.GetSSLCrlFileContent,
		create:     c.CreateSSLCrlFile,
		update:     c.UpdateSSLCrlFile,
		delete:     c.DeleteSSLCrlFile,
	}
}

// newSSLCaConfig creates configuration for SSL CA file operations.
func newSSLCaConfig(c *client.DataplaneClient) sslStorageConfig {
	return sslStorageConfig{
		fileType:        "SSL CA file",
		isSupported:     func() bool { return c.Capabilities().SupportsSslCaFiles },
		detectedVersion: c.DetectedVersion,
	}
}

// newSSLCrlConfig creates configuration for SSL CRL file operations.
func newSSLCrlConfig(c *client.DataplaneClient) sslStorageConfig {
	return sslStorageConfig{
		fileType:        "SSL CRL file",
		isSupported:     func() bool { return c.Capabilities().SupportsSslCrlFiles },
		detectedVersion: c.DetectedVersion,
	}
}
