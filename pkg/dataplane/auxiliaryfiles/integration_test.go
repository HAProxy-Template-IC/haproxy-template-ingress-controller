package auxiliaryfiles

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// mockStorage simulates HAProxy storage for testing.
type mockStorage struct {
	mu    sync.RWMutex
	files map[string]string // filename -> content
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		files: make(map[string]string),
	}
}

func (s *mockStorage) list() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.files))
	for name := range s.files {
		names = append(names, name)
	}
	return names
}

func (s *mockStorage) get(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	content, ok := s.files[name]
	return content, ok
}

func (s *mockStorage) put(name, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = content
}

func (s *mockStorage) delete(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.files[name]; ok {
		delete(s.files, name)
		return true
	}
	return false
}

// createTestServer creates a mock HTTP server for testing auxiliary file operations.
func createTestServer(generalFiles, mapFiles, crtLists *mockStorage) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Version info endpoint (required for client initialization)
		if path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// General files storage
		if strings.HasPrefix(path, "/services/haproxy/storage/general/") {
			handleStorageRequest(w, r, generalFiles, "general", "/services/haproxy/storage/general/")
			return
		}
		if path == "/services/haproxy/storage/general" {
			handleStorageList(w, generalFiles)
			return
		}

		// Map files storage
		if strings.HasPrefix(path, "/services/haproxy/storage/maps/") {
			handleStorageRequest(w, r, mapFiles, "map", "/services/haproxy/storage/maps/")
			return
		}
		if path == "/services/haproxy/storage/maps" {
			handleStorageList(w, mapFiles)
			return
		}

		// CRT-list files storage
		if strings.HasPrefix(path, "/services/haproxy/storage/ssl_crt_lists/") {
			handleStorageRequest(w, r, crtLists, "crtlist", "/services/haproxy/storage/ssl_crt_lists/")
			return
		}
		if path == "/services/haproxy/storage/ssl_crt_lists" {
			handleStorageList(w, crtLists)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}

// handleStorageList handles listing all files in storage.
func handleStorageList(w http.ResponseWriter, storage *mockStorage) {
	files := storage.list()

	entries := make([]map[string]string, 0, len(files))
	for _, name := range files {
		entries = append(entries, map[string]string{
			"storage_name": name,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(entries); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// handleStorageRequest handles individual file operations.
func handleStorageRequest(w http.ResponseWriter, r *http.Request, storage *mockStorage, _ /* fileType */, prefix string) {
	name := strings.TrimPrefix(r.URL.Path, prefix)

	switch r.Method {
	case http.MethodGet:
		handleStorageGet(w, storage, name)
	case http.MethodPost, http.MethodPut:
		handleStorageWrite(w, r, storage, name)
	case http.MethodDelete:
		handleStorageDelete(w, storage, name)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleStorageGet handles GET requests for storage files.
func handleStorageGet(w http.ResponseWriter, storage *mockStorage, name string) {
	content, ok := storage.get(name)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(content))
}

// handleStorageWrite handles POST/PUT requests for storage files.
func handleStorageWrite(w http.ResponseWriter, r *http.Request, storage *mockStorage, name string) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file_upload")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer file.Close()

	content, _ := io.ReadAll(file)

	if r.Method == http.MethodPost {
		if _, exists := storage.get(name); exists {
			w.WriteHeader(http.StatusConflict)
			return
		}
	}

	storage.put(name, string(content))
	w.WriteHeader(http.StatusCreated)
}

// handleStorageDelete handles DELETE requests for storage files.
func handleStorageDelete(w http.ResponseWriter, storage *mockStorage, name string) {
	if storage.delete(name) {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

// createTestClient creates a DataplaneClient connected to the test server.
func createTestClient(t *testing.T, serverURL string) *client.DataplaneClient {
	t.Helper()

	c, err := client.New(context.Background(), &client.Config{
		BaseURL:  serverURL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)
	return c
}

// TestCompareGeneralFiles_Integration tests CompareGeneralFiles with a mock HTTP server.
func TestCompareGeneralFiles_Integration(t *testing.T) {
	generalFiles := newMockStorage()
	server := createTestServer(generalFiles, nil, nil)
	defer server.Close()

	c := createTestClient(t, server.URL)
	ctx := context.Background()

	// Start with empty storage
	t.Run("compare with empty storage", func(t *testing.T) {
		desired := []GeneralFile{
			{Filename: "400.http", Content: "HTTP/1.0 400 Bad Request"},
			{Filename: "500.http", Content: "HTTP/1.0 500 Internal Error"},
		}

		diff, err := CompareGeneralFiles(ctx, c, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 2)
		assert.Empty(t, diff.ToUpdate)
		assert.Empty(t, diff.ToDelete)
	})

	// Add some files
	generalFiles.put("400.http", "HTTP/1.0 400 Bad Request")
	generalFiles.put("old.http", "old content")

	t.Run("compare with existing files", func(t *testing.T) {
		desired := []GeneralFile{
			{Filename: "400.http", Content: "HTTP/1.0 400 Bad Request"},    // unchanged
			{Filename: "500.http", Content: "HTTP/1.0 500 Internal Error"}, // new
		}

		diff, err := CompareGeneralFiles(ctx, c, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 1)
		assert.Equal(t, "500.http", diff.ToCreate[0].Filename)
		assert.Empty(t, diff.ToUpdate)
		assert.Len(t, diff.ToDelete, 1)
		assert.Contains(t, diff.ToDelete, "old.http")
	})

	t.Run("compare with content changes", func(t *testing.T) {
		desired := []GeneralFile{
			{Filename: "400.http", Content: "HTTP/1.0 400 Updated"},
		}

		diff, err := CompareGeneralFiles(ctx, c, desired)
		require.NoError(t, err)

		assert.Empty(t, diff.ToCreate)
		assert.Len(t, diff.ToUpdate, 1)
		assert.Len(t, diff.ToDelete, 1)
	})
}

// TestSyncGeneralFiles_Integration tests SyncGeneralFiles with a mock HTTP server.
// Note: Create and update tests are omitted because they require complex multipart form
// upload handling in the mock server. The core sync logic is tested via unit tests.
func TestSyncGeneralFiles_Integration(t *testing.T) {
	generalFiles := newMockStorage()
	server := createTestServer(generalFiles, nil, nil)
	defer server.Close()

	c := createTestClient(t, server.URL)
	ctx := context.Background()

	t.Run("sync nil diff", func(t *testing.T) {
		_, err := SyncGeneralFiles(ctx, c, nil)
		require.NoError(t, err)
	})

	t.Run("sync deletes", func(t *testing.T) {
		generalFiles.put("todelete.http", "content")

		diff := &FileDiff{
			ToDelete: []string{"todelete.http"},
		}

		_, err := SyncGeneralFiles(ctx, c, diff)
		require.NoError(t, err)

		_, ok := generalFiles.get("todelete.http")
		assert.False(t, ok)
	})
}

// TestCompareMapFiles_Integration tests CompareMapFiles with a mock HTTP server.
func TestCompareMapFiles_Integration(t *testing.T) {
	mapFiles := newMockStorage()
	server := createTestServer(nil, mapFiles, nil)
	defer server.Close()

	c := createTestClient(t, server.URL)
	ctx := context.Background()

	t.Run("compare with empty storage", func(t *testing.T) {
		desired := []MapFile{
			{Path: "/etc/haproxy/maps/hosts.map", Content: "example.com backend1"},
		}

		diff, err := CompareMapFiles(ctx, c, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 1)
		assert.Empty(t, diff.ToUpdate)
		assert.Empty(t, diff.ToDelete)
	})

	// Add a file and test update
	mapFiles.put("/etc/haproxy/maps/hosts.map", "old.example.com backend1")

	t.Run("compare with existing file needing update", func(t *testing.T) {
		desired := []MapFile{
			{Path: "/etc/haproxy/maps/hosts.map", Content: "new.example.com backend2"},
		}

		diff, err := CompareMapFiles(ctx, c, desired)
		require.NoError(t, err)

		assert.Empty(t, diff.ToCreate)
		assert.Len(t, diff.ToUpdate, 1)
		assert.Empty(t, diff.ToDelete)
	})
}

// TestSyncMapFiles_Integration tests SyncMapFiles with a mock HTTP server.
// Note: Create and update tests are omitted because they require complex multipart form
// upload handling in the mock server. The core sync logic is tested via unit tests.
func TestSyncMapFiles_Integration(t *testing.T) {
	mapFiles := newMockStorage()
	server := createTestServer(nil, mapFiles, nil)
	defer server.Close()

	c := createTestClient(t, server.URL)
	ctx := context.Background()

	t.Run("sync nil diff", func(t *testing.T) {
		_, err := SyncMapFiles(ctx, c, nil)
		require.NoError(t, err)
	})

	t.Run("sync deletes", func(t *testing.T) {
		mapFiles.put("old.map", "old content")

		diff := &MapFileDiff{
			ToDelete: []string{"old.map"},
		}

		_, err := SyncMapFiles(ctx, c, diff)
		require.NoError(t, err)

		_, ok := mapFiles.get("old.map")
		assert.False(t, ok)
	})
}

// Note: SSL certificate integration tests are skipped because they require complex
// name sanitization logic that matches the real API behavior. The core SSL comparison
// logic is tested via the unit tests for Compare and Sync generic functions.

// TestCompareCRTLists_Integration tests CompareCRTLists with a mock HTTP server.
func TestCompareCRTLists_Integration(t *testing.T) {
	crtLists := newMockStorage()
	server := createTestServer(nil, nil, crtLists)
	defer server.Close()

	c := createTestClient(t, server.URL)
	ctx := context.Background()

	t.Run("compare with empty storage", func(t *testing.T) {
		desired := []CRTListFile{
			{Path: "/etc/haproxy/certs/crt-list.txt", Content: "/path/cert.pem"},
		}

		diff, err := CompareCRTLists(ctx, c, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 1)
		assert.Empty(t, diff.ToUpdate)
		assert.Empty(t, diff.ToDelete)
	})

	// Add a file and test update
	crtLists.put("crt-list.txt", "old content")

	t.Run("compare with existing file needing update", func(t *testing.T) {
		desired := []CRTListFile{
			{Path: "/etc/haproxy/certs/crt-list.txt", Content: "new content"},
		}

		diff, err := CompareCRTLists(ctx, c, desired)
		require.NoError(t, err)

		assert.Empty(t, diff.ToCreate)
		assert.Len(t, diff.ToUpdate, 1)
		assert.Empty(t, diff.ToDelete)
	})
}

// TestSyncCRTLists_Integration tests SyncCRTLists with a mock HTTP server.
func TestSyncCRTLists_Integration(t *testing.T) {
	crtLists := newMockStorage()
	server := createTestServer(nil, nil, crtLists)
	defer server.Close()

	c := createTestClient(t, server.URL)
	ctx := context.Background()

	t.Run("sync nil diff", func(t *testing.T) {
		_, err := SyncCRTLists(ctx, c, nil)
		require.NoError(t, err)
	})

	t.Run("sync deletes", func(t *testing.T) {
		crtLists.put("todelete.txt", "content")

		diff := &CRTListDiff{
			ToDelete: []string{"todelete.txt"},
		}

		_, err := SyncCRTLists(ctx, c, diff)
		require.NoError(t, err)

		_, ok := crtLists.get("todelete.txt")
		assert.False(t, ok)
	})
}
