package auxiliaryfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

// --- sslStorageOps.Create ---

func TestSSLStorageOps_Create_Success(t *testing.T) {
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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

	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
		getAll: func(_ context.Context) ([]string, error) {
			return []string{"a.pem", "b.pem"}, nil
		},
	}

	files, err := ops.GetAll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"a.pem", "b.pem"}, files)
}

func TestSSLStorageOps_GetContent(t *testing.T) {
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
		getAll: func(_ context.Context) ([]string, error) {
			return []string{"ca-bundle.pem"}, nil
		},
	}

	found := ops.verifyExistsWithRetry(context.Background(), "ca-bundle.pem")
	assert.True(t, found)
}

func TestVerifyExistsWithRetry_NeverFound(t *testing.T) {
	callCount := 0
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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
	ops := &sslStorageOps{
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

// --- SSL CA storage server (runtime ssl_ca_files endpoints) ---

// createSSLCaTestServer creates a mock HTTP server speaking the DataPlane API
// runtime ssl_ca_files endpoints, backed by the given storage. The API version
// in /v3/info controls SupportsSslCaFiles (v3.2+ => supported).
func createSSLCaTestServer(t *testing.T, caFiles *mockStorage, apiVersion string) *httptest.Server {
	t.Helper()
	const collection = "/services/haproxy/runtime/ssl_ca_files"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"api":{"version":%q}}`, apiVersion)
			return
		}

		switch {
		case r.URL.Path == collection && r.Method == http.MethodGet:
			handleStorageList(w, caFiles)
		case r.URL.Path == collection && r.Method == http.MethodPost:
			handleSSLCaCreate(w, r, caFiles)
		case strings.HasPrefix(r.URL.Path, collection+"/"):
			name := strings.TrimPrefix(r.URL.Path, collection+"/")
			switch r.Method {
			case http.MethodGet:
				handleStorageGet(w, caFiles, name)
			case http.MethodPut:
				handleStorageWrite(w, r, caFiles, name)
			case http.MethodDelete:
				handleStorageDelete(w, caFiles, name)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// handleSSLCaCreate handles POST to the collection endpoint, where the filename
// is carried in the multipart file_upload part rather than the URL path.
func handleSSLCaCreate(w http.ResponseWriter, r *http.Request, storage *mockStorage) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file_upload")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer file.Close()

	content, _ := io.ReadAll(file)
	name := header.Filename
	if _, exists := storage.get(name); exists {
		w.WriteHeader(http.StatusConflict)
		return
	}
	storage.put(name, string(content))
	w.WriteHeader(http.StatusCreated)
}

// --- CompareSSLCaFiles ---

func TestCompareSSLCaFiles_UnsupportedCapability(t *testing.T) {
	// v3.0 does not support SSL CA file storage; comparison short-circuits.
	server := createSSLCaTestServer(t, newMockStorage(), "v3.0.4 87ad0bcf")
	defer server.Close()
	c := testutil.NewTestClient(t, server)

	diff, err := CompareSSLCaFiles(
		context.Background(),
		c,
		[]SSLCaFile{{Path: "ca.pem", Content: "data"}},
	)
	require.NoError(t, err)
	assert.Empty(t, diff.ToCreate)
	assert.Empty(t, diff.ToUpdate)
	assert.Empty(t, diff.ToDelete)
}

func TestCompareSSLCaFiles_PathNormalizationAndRestoration(t *testing.T) {
	// Empty storage — all desired files should be created.
	server := createSSLCaTestServer(t, newMockStorage(), "v3.2.6 87ad0bcf")
	defer server.Close()
	c := testutil.NewTestClient(t, server)

	desired := []SSLCaFile{
		{Path: "/etc/haproxy/ssl/ca/trusted.pem", Content: "ca-data"},
	}

	diff, err := CompareSSLCaFiles(context.Background(), c, desired)
	require.NoError(t, err)
	require.Len(t, diff.ToCreate, 1)
	// Original (full) path should be restored on the diff entry.
	assert.Equal(t, "/etc/haproxy/ssl/ca/trusted.pem", diff.ToCreate[0].Path)
	assert.Equal(t, "ca-data", diff.ToCreate[0].Content)
}

// --- SyncSSLCaFiles ---

func TestSyncSSLCaFiles_NilDiff(t *testing.T) {
	server := createSSLCaTestServer(t, newMockStorage(), "v3.2.6 87ad0bcf")
	defer server.Close()
	c := testutil.NewTestClient(t, server)

	reloadIDs, err := SyncSSLCaFiles(context.Background(), c, nil)
	require.NoError(t, err)
	assert.Nil(t, reloadIDs)
}

func TestSyncSSLCaFiles_UnsupportedCapability(t *testing.T) {
	// v3.0 does not support SSL CA file storage; sync is skipped.
	server := createSSLCaTestServer(t, newMockStorage(), "v3.0.4 87ad0bcf")
	defer server.Close()
	c := testutil.NewTestClient(t, server)

	diff := &SSLCaFileDiff{
		ToCreate: []SSLCaFile{{Path: "ca.pem", Content: "data"}},
	}

	reloadIDs, err := SyncSSLCaFiles(context.Background(), c, diff)
	require.NoError(t, err)
	assert.Nil(t, reloadIDs)
}

func TestSyncSSLCaFiles_DelegatesToSync(t *testing.T) {
	storage := newMockStorage()
	server := createSSLCaTestServer(t, storage, "v3.2.6 87ad0bcf")
	defer server.Close()
	c := testutil.NewTestClient(t, server)

	diff := &SSLCaFileDiff{
		ToCreate: []SSLCaFile{
			{Path: "a.pem", Content: "data-a"},
			{Path: "b.pem", Content: "data-b"},
		},
	}

	_, err := SyncSSLCaFiles(context.Background(), c, diff)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a.pem", "b.pem"}, storage.list())
}

func TestSyncSSLCaFiles_EmptyDiff(t *testing.T) {
	server := createSSLCaTestServer(t, newMockStorage(), "v3.2.6 87ad0bcf")
	defer server.Close()
	c := testutil.NewTestClient(t, server)

	diff := &SSLCaFileDiff{
		ToCreate: []SSLCaFile{},
		ToUpdate: []SSLCaFile{},
		ToDelete: []string{},
	}

	reloadIDs, err := SyncSSLCaFiles(context.Background(), c, diff)
	require.NoError(t, err)
	assert.Empty(t, reloadIDs)
}
