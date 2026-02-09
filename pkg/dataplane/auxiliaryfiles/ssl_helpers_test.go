package auxiliaryfiles

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- sslStorageOps.Create ---

func TestSSLStorageOps_Create_Success(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, id, content string) (string, error) {
			assert.Equal(t, "ca-bundle.pem", id)
			assert.Equal(t, "pem-data", content)
			return "reload-1", nil
		},
	}

	reloadID, err := ops.Create(context.Background(), "ca-bundle.pem", "pem-data")
	require.NoError(t, err)
	assert.Equal(t, "reload-1", reloadID)
}

func TestSSLStorageOps_Create_PathNormalization(t *testing.T) {
	var receivedID string
	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, id, _ string) (string, error) {
			receivedID = id
			return "", nil
		},
	}

	_, err := ops.Create(context.Background(), "/etc/haproxy/ssl/ca/ca-bundle.pem", "pem-data")
	require.NoError(t, err)
	assert.Equal(t, "ca-bundle.pem", receivedID, "should strip directory components")
}

func TestSSLStorageOps_Create_AlreadyExists_FallsBackToUpdate(t *testing.T) {
	var updateCalled bool
	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("file already exists")
		},
		update: func(_ context.Context, id, content string) (string, error) {
			updateCalled = true
			assert.Equal(t, "ca-bundle.pem", id)
			assert.Equal(t, "pem-data", content)
			return "reload-2", nil
		},
	}

	reloadID, err := ops.Create(context.Background(), "/etc/haproxy/ssl/ca/ca-bundle.pem", "pem-data")
	require.NoError(t, err)
	assert.True(t, updateCalled, "should fall back to Update when file already exists")
	assert.Equal(t, "reload-2", reloadID)
}

func TestSSLStorageOps_Create_500_FileExistsOnRetry(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("unexpected status code 500")
		},
		getAll: func(_ context.Context) ([]string, error) {
			return []string{"ca-bundle.pem"}, nil
		},
	}

	reloadID, err := ops.Create(context.Background(), "ca-bundle.pem", "pem-data")
	require.NoError(t, err)
	assert.Equal(t, "", reloadID, "should return empty reload ID on 500+exists workaround")
}

func TestSSLStorageOps_Create_500_FileNotFound(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("unexpected status code 500")
		},
		getAll: func(_ context.Context) ([]string, error) {
			return []string{"other-file.pem"}, nil
		},
	}

	_, err := ops.Create(context.Background(), "ca-bundle.pem", "pem-data")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestSSLStorageOps_Create_ContextCancelled_DuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("unexpected status code 500")
		},
		getAll: func(_ context.Context) ([]string, error) {
			// Return empty so it would need to retry — but context is cancelled
			return []string{}, nil
		},
	}

	_, err := ops.Create(ctx, "ca-bundle.pem", "pem-data")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestSSLStorageOps_Create_NonRetryableError(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("permission denied")
		},
	}

	_, err := ops.Create(context.Background(), "ca-bundle.pem", "pem-data")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

// --- sslStorageOps.Update ---

func TestSSLStorageOps_Update_Success(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		update: func(_ context.Context, id, content string) (string, error) {
			assert.Equal(t, "ca-bundle.pem", id)
			assert.Equal(t, "new-data", content)
			return "reload-3", nil
		},
	}

	reloadID, err := ops.Update(context.Background(), "ca-bundle.pem", "new-data")
	require.NoError(t, err)
	assert.Equal(t, "reload-3", reloadID)
}

func TestSSLStorageOps_Update_PathNormalization(t *testing.T) {
	var receivedID string
	ops := &sslStorageOps[SSLCaFile]{
		update: func(_ context.Context, id, _ string) (string, error) {
			receivedID = id
			return "", nil
		},
	}

	_, err := ops.Update(context.Background(), "/etc/haproxy/ssl/ca/ca-bundle.pem", "data")
	require.NoError(t, err)
	assert.Equal(t, "ca-bundle.pem", receivedID, "should strip directory components")
}

func TestSSLStorageOps_Update_500_FileExistsOnRetry(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		update: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("unexpected status code 500")
		},
		getAll: func(_ context.Context) ([]string, error) {
			return []string{"ca-bundle.pem"}, nil
		},
	}

	reloadID, err := ops.Update(context.Background(), "ca-bundle.pem", "data")
	require.NoError(t, err)
	assert.Equal(t, "", reloadID)
}

func TestSSLStorageOps_Update_500_FileNotFound(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		update: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("unexpected status code 500")
		},
		getAll: func(_ context.Context) ([]string, error) {
			return []string{}, nil
		},
	}

	_, err := ops.Update(context.Background(), "ca-bundle.pem", "data")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// --- sslStorageOps.Delete ---

func TestSSLStorageOps_Delete_Success(t *testing.T) {
	var receivedID string
	ops := &sslStorageOps[SSLCaFile]{
		delete: func(_ context.Context, id string) error {
			receivedID = id
			return nil
		},
	}

	err := ops.Delete(context.Background(), "/etc/haproxy/ssl/ca/ca-bundle.pem")
	require.NoError(t, err)
	assert.Equal(t, "ca-bundle.pem", receivedID, "should strip directory components")
}

func TestSSLStorageOps_Delete_Error(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		delete: func(_ context.Context, _ string) error {
			return errors.New("delete failed")
		},
	}

	err := ops.Delete(context.Background(), "ca-bundle.pem")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

// --- sslStorageOps.GetAll / GetContent ---

func TestSSLStorageOps_GetAll(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		getAll: func(_ context.Context) ([]string, error) {
			return []string{"a.pem", "b.pem"}, nil
		},
	}

	files, err := ops.GetAll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"a.pem", "b.pem"}, files)
}

func TestSSLStorageOps_GetContent(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		getContent: func(_ context.Context, id string) (string, error) {
			if id == "a.pem" {
				return "cert-data", nil
			}
			return "", errors.New("not found")
		},
	}

	content, err := ops.GetContent(context.Background(), "a.pem")
	require.NoError(t, err)
	assert.Equal(t, "cert-data", content)
}

// --- verifyExistsWithRetry ---

func TestVerifyExistsWithRetry_FoundFirstAttempt(t *testing.T) {
	ops := &sslStorageOps[SSLCaFile]{
		getAll: func(_ context.Context) ([]string, error) {
			return []string{"ca-bundle.pem"}, nil
		},
	}

	found := ops.verifyExistsWithRetry(context.Background(), "ca-bundle.pem")
	assert.True(t, found)
}

func TestVerifyExistsWithRetry_NeverFound(t *testing.T) {
	callCount := 0
	ops := &sslStorageOps[SSLCaFile]{
		getAll: func(_ context.Context) ([]string, error) {
			callCount++
			return []string{"other.pem"}, nil
		},
	}

	found := ops.verifyExistsWithRetry(context.Background(), "ca-bundle.pem")
	assert.False(t, found)
	assert.Equal(t, 3, callCount, "should retry 3 times")
}

func TestVerifyExistsWithRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	callCount := 0
	ops := &sslStorageOps[SSLCaFile]{
		getAll: func(_ context.Context) ([]string, error) {
			callCount++
			return []string{}, nil
		},
	}

	found := ops.verifyExistsWithRetry(ctx, "ca-bundle.pem")
	assert.False(t, found)
	// First attempt runs, second attempt cancelled by context
	assert.LessOrEqual(t, callCount, 2)
}

func TestVerifyExistsWithRetry_GetAllErrors_ContinuesRetrying(t *testing.T) {
	callCount := 0
	ops := &sslStorageOps[SSLCaFile]{
		getAll: func(_ context.Context) ([]string, error) {
			callCount++
			if callCount < 3 {
				return nil, errors.New("connection error")
			}
			return []string{"ca-bundle.pem"}, nil
		},
	}

	found := ops.verifyExistsWithRetry(context.Background(), "ca-bundle.pem")
	assert.True(t, found)
	assert.Equal(t, 3, callCount)
}

// --- compareSSLStorageFiles ---

func TestCompareSSLStorageFiles_UnsupportedCapability(t *testing.T) {
	cfg := sslStorageConfig{
		fileType:        "SSL CA file",
		isSupported:     func() bool { return false },
		detectedVersion: func() string { return "v3.0.4" },
	}

	diff, err := compareSSLStorageFiles[SSLCaFile](
		context.Background(),
		[]SSLCaFile{{Path: "ca.pem", Content: "data"}},
		nil, // ops not needed — capability check short-circuits
		cfg,
		func(f SSLCaFile) SSLCaFile { return f },
		func(id, content string) SSLCaFile { return SSLCaFile{Path: id, Content: content} },
		func(f SSLCaFile) string { return f.Path },
	)
	require.NoError(t, err)
	assert.Empty(t, diff.ToCreate)
	assert.Empty(t, diff.ToUpdate)
	assert.Empty(t, diff.ToDelete)
}

func TestCompareSSLStorageFiles_PathNormalizationAndRestoration(t *testing.T) {
	// Mock ops that returns no current files — all desired files should be created
	ops := &sslStorageOps[SSLCaFile]{
		getAll: func(_ context.Context) ([]string, error) {
			return []string{}, nil
		},
	}

	cfg := sslStorageConfig{
		fileType:        "SSL CA file",
		isSupported:     func() bool { return true },
		detectedVersion: func() string { return "v3.2.0" },
	}

	desired := []SSLCaFile{
		{Path: "/etc/haproxy/ssl/ca/trusted.pem", Content: "ca-data"},
	}

	diff, err := compareSSLStorageFiles[SSLCaFile](
		context.Background(),
		desired,
		ops,
		cfg,
		func(f SSLCaFile) SSLCaFile {
			// Normalize: strip directory path
			return SSLCaFile{Path: "trusted.pem", Content: f.Content}
		},
		func(id, content string) SSLCaFile { return SSLCaFile{Path: id, Content: content} },
		func(f SSLCaFile) string { return f.Path },
	)
	require.NoError(t, err)
	require.Len(t, diff.ToCreate, 1)
	// Original path should be restored
	assert.Equal(t, "/etc/haproxy/ssl/ca/trusted.pem", diff.ToCreate[0].Path)
	assert.Equal(t, "ca-data", diff.ToCreate[0].Content)
}

// --- syncSSLStorageFiles ---

func TestSyncSSLStorageFiles_NilDiff(t *testing.T) {
	cfg := sslStorageConfig{
		fileType:    "SSL CA file",
		isSupported: func() bool { return true },
	}

	reloadIDs, err := syncSSLStorageFiles[SSLCaFile](context.Background(), nil, nil, cfg)
	require.NoError(t, err)
	assert.Nil(t, reloadIDs)
}

func TestSyncSSLStorageFiles_UnsupportedCapability(t *testing.T) {
	cfg := sslStorageConfig{
		fileType:        "SSL CA file",
		isSupported:     func() bool { return false },
		detectedVersion: func() string { return "v3.0.4" },
	}

	diff := &FileDiffGeneric[SSLCaFile]{
		ToCreate: []SSLCaFile{{Path: "ca.pem", Content: "data"}},
	}

	reloadIDs, err := syncSSLStorageFiles[SSLCaFile](context.Background(), diff, nil, cfg)
	require.NoError(t, err)
	assert.Nil(t, reloadIDs)
}

func TestSyncSSLStorageFiles_DelegatesToSync(t *testing.T) {
	var createdIDs []string
	ops := &sslStorageOps[SSLCaFile]{
		create: func(_ context.Context, id, _ string) (string, error) {
			createdIDs = append(createdIDs, id)
			return "reload-" + id, nil
		},
		update: func(_ context.Context, id, _ string) (string, error) {
			return "", nil
		},
		delete: func(_ context.Context, _ string) error {
			return nil
		},
	}

	cfg := sslStorageConfig{
		fileType:        "SSL CA file",
		isSupported:     func() bool { return true },
		detectedVersion: func() string { return "v3.2.0" },
	}

	diff := &FileDiffGeneric[SSLCaFile]{
		ToCreate: []SSLCaFile{
			{Path: "a.pem", Content: "data-a"},
			{Path: "b.pem", Content: "data-b"},
		},
	}

	reloadIDs, err := syncSSLStorageFiles[SSLCaFile](context.Background(), diff, ops, cfg)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.pem", "b.pem"}, createdIDs)
	assert.ElementsMatch(t, []string{"reload-a.pem", "reload-b.pem"}, reloadIDs)
}

func TestSyncSSLStorageFiles_EmptyDiff(t *testing.T) {
	cfg := sslStorageConfig{
		fileType:        "SSL CA file",
		isSupported:     func() bool { return true },
		detectedVersion: func() string { return "v3.2.0" },
	}

	diff := &FileDiffGeneric[SSLCaFile]{
		ToCreate: []SSLCaFile{},
		ToUpdate: []SSLCaFile{},
		ToDelete: []string{},
	}

	reloadIDs, err := syncSSLStorageFiles[SSLCaFile](context.Background(), diff, nil, cfg)
	require.NoError(t, err)
	assert.Empty(t, reloadIDs)
}
