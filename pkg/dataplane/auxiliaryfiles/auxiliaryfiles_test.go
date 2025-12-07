package auxiliaryfiles

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeneralFile_GetIdentifier tests the GetIdentifier method for GeneralFile.
func TestGeneralFile_GetIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		file     GeneralFile
		expected string
	}{
		{
			name:     "simple filename",
			file:     GeneralFile{Filename: "400.http", Content: "content"},
			expected: "400.http",
		},
		{
			name:     "empty filename",
			file:     GeneralFile{Filename: "", Content: "content"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.file.GetIdentifier())
		})
	}
}

// TestGeneralFile_GetContent tests the GetContent method for GeneralFile.
func TestGeneralFile_GetContent(t *testing.T) {
	tests := []struct {
		name     string
		file     GeneralFile
		expected string
	}{
		{
			name:     "simple content",
			file:     GeneralFile{Filename: "test.http", Content: "HTTP/1.0 500"},
			expected: "HTTP/1.0 500",
		},
		{
			name:     "empty content",
			file:     GeneralFile{Filename: "test.http", Content: ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.file.GetContent())
		})
	}
}

// TestSSLCertificate_GetIdentifier tests the GetIdentifier method for SSLCertificate.
func TestSSLCertificate_GetIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		cert     SSLCertificate
		expected string
	}{
		{
			name:     "full path",
			cert:     SSLCertificate{Path: "/etc/haproxy/ssl/cert.pem", Content: "cert"},
			expected: "/etc/haproxy/ssl/cert.pem",
		},
		{
			name:     "filename only",
			cert:     SSLCertificate{Path: "cert.pem", Content: "cert"},
			expected: "cert.pem",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.cert.GetIdentifier())
		})
	}
}

// TestSSLCertificate_GetContent tests the GetContent method for SSLCertificate.
func TestSSLCertificate_GetContent(t *testing.T) {
	cert := SSLCertificate{Path: "cert.pem", Content: "-----BEGIN CERTIFICATE-----"}
	assert.Equal(t, "-----BEGIN CERTIFICATE-----", cert.GetContent())
}

// TestMapFile_GetIdentifier tests the GetIdentifier method for MapFile.
func TestMapFile_GetIdentifier(t *testing.T) {
	mapFile := MapFile{Path: "/etc/haproxy/maps/domains.map", Content: "example.com backend1"}
	assert.Equal(t, "/etc/haproxy/maps/domains.map", mapFile.GetIdentifier())
}

// TestMapFile_GetContent tests the GetContent method for MapFile.
func TestMapFile_GetContent(t *testing.T) {
	mapFile := MapFile{Path: "domains.map", Content: "example.com backend1\ntest.com backend2"}
	assert.Equal(t, "example.com backend1\ntest.com backend2", mapFile.GetContent())
}

// TestCRTListFile_GetIdentifier tests the GetIdentifier method for CRTListFile.
func TestCRTListFile_GetIdentifier(t *testing.T) {
	crtList := CRTListFile{Path: "/etc/haproxy/certs/crt-list.txt", Content: "cert content"}
	assert.Equal(t, "/etc/haproxy/certs/crt-list.txt", crtList.GetIdentifier())
}

// TestCRTListFile_GetContent tests the GetContent method for CRTListFile.
func TestCRTListFile_GetContent(t *testing.T) {
	crtList := CRTListFile{Path: "crt-list.txt", Content: "/path/cert.pem [ocsp-update on]"}
	assert.Equal(t, "/path/cert.pem [ocsp-update on]", crtList.GetContent())
}

// TestFileDiff_HasChanges tests the HasChanges method for FileDiff.
func TestFileDiff_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		diff     FileDiff
		expected bool
	}{
		{
			name:     "no changes",
			diff:     FileDiff{},
			expected: false,
		},
		{
			name:     "has creates",
			diff:     FileDiff{ToCreate: []GeneralFile{{Filename: "test"}}},
			expected: true,
		},
		{
			name:     "has updates",
			diff:     FileDiff{ToUpdate: []GeneralFile{{Filename: "test"}}},
			expected: true,
		},
		{
			name:     "has deletes",
			diff:     FileDiff{ToDelete: []string{"test"}},
			expected: true,
		},
		{
			name: "has all",
			diff: FileDiff{
				ToCreate: []GeneralFile{{Filename: "new"}},
				ToUpdate: []GeneralFile{{Filename: "updated"}},
				ToDelete: []string{"deleted"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.diff.HasChanges())
		})
	}
}

// TestSSLCertificateDiff_HasChanges tests the HasChanges method for SSLCertificateDiff.
func TestSSLCertificateDiff_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		diff     SSLCertificateDiff
		expected bool
	}{
		{
			name:     "no changes",
			diff:     SSLCertificateDiff{},
			expected: false,
		},
		{
			name:     "has creates",
			diff:     SSLCertificateDiff{ToCreate: []SSLCertificate{{Path: "cert.pem"}}},
			expected: true,
		},
		{
			name:     "has updates",
			diff:     SSLCertificateDiff{ToUpdate: []SSLCertificate{{Path: "cert.pem"}}},
			expected: true,
		},
		{
			name:     "has deletes",
			diff:     SSLCertificateDiff{ToDelete: []string{"cert.pem"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.diff.HasChanges())
		})
	}
}

// TestMapFileDiff_HasChanges tests the HasChanges method for MapFileDiff.
func TestMapFileDiff_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		diff     MapFileDiff
		expected bool
	}{
		{
			name:     "no changes",
			diff:     MapFileDiff{},
			expected: false,
		},
		{
			name:     "has creates",
			diff:     MapFileDiff{ToCreate: []MapFile{{Path: "map.map"}}},
			expected: true,
		},
		{
			name:     "has updates",
			diff:     MapFileDiff{ToUpdate: []MapFile{{Path: "map.map"}}},
			expected: true,
		},
		{
			name:     "has deletes",
			diff:     MapFileDiff{ToDelete: []string{"map.map"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.diff.HasChanges())
		})
	}
}

// TestCRTListDiff_HasChanges tests the HasChanges method for CRTListDiff.
func TestCRTListDiff_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		diff     CRTListDiff
		expected bool
	}{
		{
			name:     "no changes",
			diff:     CRTListDiff{},
			expected: false,
		},
		{
			name:     "has creates",
			diff:     CRTListDiff{ToCreate: []CRTListFile{{Path: "crt-list.txt"}}},
			expected: true,
		},
		{
			name:     "has updates",
			diff:     CRTListDiff{ToUpdate: []CRTListFile{{Path: "crt-list.txt"}}},
			expected: true,
		},
		{
			name:     "has deletes",
			diff:     CRTListDiff{ToDelete: []string{"crt-list.txt"}},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.diff.HasChanges())
		})
	}
}

// mockFileOps is a mock implementation of FileOperations for testing.
type mockFileOps[T FileItem] struct {
	getAllFunc     func(ctx context.Context) ([]string, error)
	getContentFunc func(ctx context.Context, id string) (string, error)
	createFunc     func(ctx context.Context, id, content string) error
	updateFunc     func(ctx context.Context, id, content string) error
	deleteFunc     func(ctx context.Context, id string) error
}

func (m *mockFileOps[T]) GetAll(ctx context.Context) ([]string, error) {
	if m.getAllFunc != nil {
		return m.getAllFunc(ctx)
	}
	return nil, nil
}

func (m *mockFileOps[T]) GetContent(ctx context.Context, id string) (string, error) {
	if m.getContentFunc != nil {
		return m.getContentFunc(ctx, id)
	}
	return "", nil
}

func (m *mockFileOps[T]) Create(ctx context.Context, id, content string) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, id, content)
	}
	return nil
}

func (m *mockFileOps[T]) Update(ctx context.Context, id, content string) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, id, content)
	}
	return nil
}

func (m *mockFileOps[T]) Delete(ctx context.Context, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, id)
	}
	return nil
}

// TestCompare_NoCurrentFiles tests Compare when there are no current files.
func TestCompare_NoCurrentFiles(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{}, nil
		},
	}

	desired := []GeneralFile{
		{Filename: "400.http", Content: "HTTP/1.0 400"},
		{Filename: "500.http", Content: "HTTP/1.0 500"},
	}

	diff, err := Compare[GeneralFile](ctx, ops, desired, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.NoError(t, err)
	assert.Len(t, diff.ToCreate, 2)
	assert.Empty(t, diff.ToUpdate)
	assert.Empty(t, diff.ToDelete)
}

// TestCompare_NoDesiredFiles tests Compare when there are no desired files.
func TestCompare_NoDesiredFiles(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{"400.http", "500.http"}, nil
		},
		getContentFunc: func(ctx context.Context, id string) (string, error) {
			return "content", nil
		},
	}

	diff, err := Compare[GeneralFile](ctx, ops, []GeneralFile{}, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.NoError(t, err)
	assert.Empty(t, diff.ToCreate)
	assert.Empty(t, diff.ToUpdate)
	assert.Len(t, diff.ToDelete, 2)
}

// TestCompare_IdenticalFiles tests Compare when current and desired files are identical.
func TestCompare_IdenticalFiles(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{"400.http"}, nil
		},
		getContentFunc: func(ctx context.Context, id string) (string, error) {
			return "HTTP/1.0 400", nil
		},
	}

	desired := []GeneralFile{
		{Filename: "400.http", Content: "HTTP/1.0 400"},
	}

	diff, err := Compare[GeneralFile](ctx, ops, desired, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.NoError(t, err)
	assert.Empty(t, diff.ToCreate)
	assert.Empty(t, diff.ToUpdate)
	assert.Empty(t, diff.ToDelete)
}

// TestCompare_UpdateNeeded tests Compare when files need updating.
func TestCompare_UpdateNeeded(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{"400.http"}, nil
		},
		getContentFunc: func(ctx context.Context, id string) (string, error) {
			return "old content", nil
		},
	}

	desired := []GeneralFile{
		{Filename: "400.http", Content: "new content"},
	}

	diff, err := Compare[GeneralFile](ctx, ops, desired, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.NoError(t, err)
	assert.Empty(t, diff.ToCreate)
	assert.Len(t, diff.ToUpdate, 1)
	assert.Equal(t, "400.http", diff.ToUpdate[0].Filename)
	assert.Equal(t, "new content", diff.ToUpdate[0].Content)
	assert.Empty(t, diff.ToDelete)
}

// TestCompare_MixedChanges tests Compare with create, update, and delete operations.
func TestCompare_MixedChanges(t *testing.T) {
	ctx := context.Background()

	contents := map[string]string{
		"existing.http": "old content",
		"todelete.http": "delete me",
	}

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{"existing.http", "todelete.http"}, nil
		},
		getContentFunc: func(ctx context.Context, id string) (string, error) {
			return contents[id], nil
		},
	}

	desired := []GeneralFile{
		{Filename: "existing.http", Content: "updated content"},
		{Filename: "new.http", Content: "new content"},
	}

	diff, err := Compare[GeneralFile](ctx, ops, desired, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.NoError(t, err)
	assert.Len(t, diff.ToCreate, 1)
	assert.Len(t, diff.ToUpdate, 1)
	assert.Len(t, diff.ToDelete, 1)
	assert.Equal(t, "new.http", diff.ToCreate[0].Filename)
	assert.Equal(t, "existing.http", diff.ToUpdate[0].Filename)
	assert.Equal(t, "todelete.http", diff.ToDelete[0])
}

// TestCompare_NoFingerprintFallback tests the __NO_FINGERPRINT__ special case.
func TestCompare_NoFingerprintFallback(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{"cert.pem"}, nil
		},
		getContentFunc: func(ctx context.Context, id string) (string, error) {
			return "__NO_FINGERPRINT__", nil
		},
	}

	desired := []GeneralFile{
		{Filename: "cert.pem", Content: "new content"},
	}

	diff, err := Compare[GeneralFile](ctx, ops, desired, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.NoError(t, err)
	assert.Empty(t, diff.ToCreate)
	assert.Len(t, diff.ToUpdate, 1) // Should use UPDATE for __NO_FINGERPRINT__
	assert.Empty(t, diff.ToDelete)
}

// TestCompare_GetAllError tests Compare when GetAll returns an error.
func TestCompare_GetAllError(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return nil, errors.New("connection refused")
		},
	}

	_, err := Compare[GeneralFile](ctx, ops, []GeneralFile{}, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch current files")
}

// TestCompare_GetContentError tests Compare when GetContent returns an error.
func TestCompare_GetContentError(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{"400.http"}, nil
		},
		getContentFunc: func(ctx context.Context, id string) (string, error) {
			return "", errors.New("file not found")
		},
	}

	_, err := Compare[GeneralFile](ctx, ops, []GeneralFile{}, func(id, content string) GeneralFile {
		return GeneralFile{Filename: id, Content: content}
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get content for file")
}

// TestSync_NilDiff tests Sync with a nil diff.
func TestSync_NilDiff(t *testing.T) {
	ctx := context.Background()
	ops := &mockFileOps[GeneralFile]{}

	err := Sync[GeneralFile](ctx, ops, nil)
	require.NoError(t, err)
}

// TestSync_EmptyDiff tests Sync with an empty diff.
func TestSync_EmptyDiff(t *testing.T) {
	ctx := context.Background()
	ops := &mockFileOps[GeneralFile]{}

	diff := &FileDiffGeneric[GeneralFile]{
		ToCreate: []GeneralFile{},
		ToUpdate: []GeneralFile{},
		ToDelete: []string{},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.NoError(t, err)
}

// TestSync_CreateFiles tests Sync creating files.
func TestSync_CreateFiles(t *testing.T) {
	ctx := context.Background()

	var created []string
	ops := &mockFileOps[GeneralFile]{
		createFunc: func(ctx context.Context, id, content string) error {
			created = append(created, id)
			return nil
		},
	}

	diff := &FileDiffGeneric[GeneralFile]{
		ToCreate: []GeneralFile{
			{Filename: "400.http", Content: "content1"},
			{Filename: "500.http", Content: "content2"},
		},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"400.http", "500.http"}, created)
}

// TestSync_UpdateFiles tests Sync updating files.
func TestSync_UpdateFiles(t *testing.T) {
	ctx := context.Background()

	var updated []string
	ops := &mockFileOps[GeneralFile]{
		updateFunc: func(ctx context.Context, id, content string) error {
			updated = append(updated, id)
			return nil
		},
	}

	diff := &FileDiffGeneric[GeneralFile]{
		ToUpdate: []GeneralFile{
			{Filename: "400.http", Content: "updated"},
		},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.NoError(t, err)
	assert.Equal(t, []string{"400.http"}, updated)
}

// TestSync_DeleteFiles tests Sync deleting files.
func TestSync_DeleteFiles(t *testing.T) {
	ctx := context.Background()

	var deleted []string
	ops := &mockFileOps[GeneralFile]{
		deleteFunc: func(ctx context.Context, id string) error {
			deleted = append(deleted, id)
			return nil
		},
	}

	diff := &FileDiffGeneric[GeneralFile]{
		ToDelete: []string{"old.http", "unused.http"},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"old.http", "unused.http"}, deleted)
}

// TestSync_CreateError tests Sync when Create returns an error.
func TestSync_CreateError(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		createFunc: func(ctx context.Context, id, content string) error {
			return errors.New("storage full")
		},
	}

	diff := &FileDiffGeneric[GeneralFile]{
		ToCreate: []GeneralFile{{Filename: "400.http", Content: "content"}},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create file")
}

// TestSync_UpdateError tests Sync when Update returns an error.
func TestSync_UpdateError(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		updateFunc: func(ctx context.Context, id, content string) error {
			return errors.New("permission denied")
		},
	}

	diff := &FileDiffGeneric[GeneralFile]{
		ToUpdate: []GeneralFile{{Filename: "400.http", Content: "content"}},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update file")
}

// TestSync_DeleteError tests Sync when Delete returns an error.
func TestSync_DeleteError(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[GeneralFile]{
		deleteFunc: func(ctx context.Context, id string) error {
			return errors.New("file not found")
		},
	}

	diff := &FileDiffGeneric[GeneralFile]{
		ToDelete: []string{"old.http"},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete file")
}

// TestCalculateCertificateFingerprint tests the SHA256 fingerprint calculation.
func TestCalculateCertificateFingerprint(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "empty content",
			content:  "",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:     "simple content",
			content:  "test",
			expected: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		},
		{
			name:     "certificate-like content",
			content:  "-----BEGIN CERTIFICATE-----\nMIID...\n-----END CERTIFICATE-----",
			expected: "a4f2c5f8b6d7e9a0c3b4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6",
		},
	}

	// Test the first two with known SHA256 values
	for _, tt := range tests[:2] {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateCertificateFingerprint(tt.content)
			assert.Equal(t, tt.expected, result)
		})
	}

	// Test that fingerprints are consistent
	t.Run("consistency check", func(t *testing.T) {
		content := "-----BEGIN CERTIFICATE-----\nMIID...\n-----END CERTIFICATE-----"
		result1 := calculateCertificateFingerprint(content)
		result2 := calculateCertificateFingerprint(content)
		assert.Equal(t, result1, result2)
	})

	// Test that different content produces different fingerprints
	t.Run("different content produces different fingerprints", func(t *testing.T) {
		fp1 := calculateCertificateFingerprint("content1")
		fp2 := calculateCertificateFingerprint("content2")
		assert.NotEqual(t, fp1, fp2)
	})
}

// TestConvertCRTListsToGeneralFiles tests conversion of CRT-list files to general files.
func TestConvertCRTListsToGeneralFiles(t *testing.T) {
	tests := []struct {
		name     string
		input    []CRTListFile
		expected []GeneralFile
	}{
		{
			name:     "empty list",
			input:    []CRTListFile{},
			expected: []GeneralFile{},
		},
		{
			name: "single crt-list",
			input: []CRTListFile{
				{Path: "/etc/haproxy/crt-list.txt", Content: "cert content"},
			},
			expected: []GeneralFile{
				{Filename: "crt-list.txt", Content: "cert content"},
			},
		},
		{
			name: "multiple crt-lists with paths",
			input: []CRTListFile{
				{Path: "/etc/haproxy/certs/list1.txt", Content: "content1"},
				{Path: "/opt/haproxy/list2.txt", Content: "content2"},
			},
			expected: []GeneralFile{
				{Filename: "list1.txt", Content: "content1"},
				{Filename: "list2.txt", Content: "content2"},
			},
		},
		{
			name: "filename only",
			input: []CRTListFile{
				{Path: "simple.txt", Content: "simple content"},
			},
			expected: []GeneralFile{
				{Filename: "simple.txt", Content: "simple content"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertCRTListsToGeneralFiles(tt.input)
			require.Len(t, result, len(tt.expected))
			for i, expected := range tt.expected {
				assert.Equal(t, expected.Filename, result[i].Filename)
				assert.Equal(t, expected.Content, result[i].Content)
			}
		})
	}
}

// TestConvertCRTListDiffToFileDiff tests conversion of CRTListDiff to FileDiff.
func TestConvertCRTListDiffToFileDiff(t *testing.T) {
	tests := []struct {
		name     string
		input    *CRTListDiff
		expected *FileDiff
	}{
		{
			name: "empty diff",
			input: &CRTListDiff{
				ToCreate: []CRTListFile{},
				ToUpdate: []CRTListFile{},
				ToDelete: []string{},
			},
			expected: &FileDiff{
				ToCreate: []GeneralFile{},
				ToUpdate: []GeneralFile{},
				ToDelete: []string{},
			},
		},
		{
			name: "with creates and updates",
			input: &CRTListDiff{
				ToCreate: []CRTListFile{{Path: "/path/new.txt", Content: "new"}},
				ToUpdate: []CRTListFile{{Path: "/path/updated.txt", Content: "updated"}},
				ToDelete: []string{"old.txt"},
			},
			expected: &FileDiff{
				ToCreate: []GeneralFile{{Filename: "new.txt", Content: "new"}},
				ToUpdate: []GeneralFile{{Filename: "updated.txt", Content: "updated"}},
				ToDelete: []string{"old.txt"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertCRTListDiffToFileDiff(tt.input)
			require.Len(t, result.ToCreate, len(tt.expected.ToCreate))
			require.Len(t, result.ToUpdate, len(tt.expected.ToUpdate))
			assert.Equal(t, tt.expected.ToDelete, result.ToDelete)

			for i, expected := range tt.expected.ToCreate {
				assert.Equal(t, expected.Filename, result.ToCreate[i].Filename)
				assert.Equal(t, expected.Content, result.ToCreate[i].Content)
			}

			for i, expected := range tt.expected.ToUpdate {
				assert.Equal(t, expected.Filename, result.ToUpdate[i].Filename)
				assert.Equal(t, expected.Content, result.ToUpdate[i].Content)
			}
		})
	}
}

// TestCategorizeFile tests the categorizeFile helper function.
func TestCategorizeFile(t *testing.T) {
	tests := []struct {
		name        string
		currentMap  map[string]GeneralFile
		id          string
		desiredFile GeneralFile
		wantCreates int
		wantUpdates int
	}{
		{
			name:        "file does not exist - should create",
			currentMap:  map[string]GeneralFile{},
			id:          "new.http",
			desiredFile: GeneralFile{Filename: "new.http", Content: "new content"},
			wantCreates: 1,
			wantUpdates: 0,
		},
		{
			name: "file exists with different content - should update",
			currentMap: map[string]GeneralFile{
				"existing.http": {Filename: "existing.http", Content: "old content"},
			},
			id:          "existing.http",
			desiredFile: GeneralFile{Filename: "existing.http", Content: "new content"},
			wantCreates: 0,
			wantUpdates: 1,
		},
		{
			name: "file exists with same content - no action",
			currentMap: map[string]GeneralFile{
				"same.http": {Filename: "same.http", Content: "same content"},
			},
			id:          "same.http",
			desiredFile: GeneralFile{Filename: "same.http", Content: "same content"},
			wantCreates: 0,
			wantUpdates: 0,
		},
		{
			name: "file exists with __NO_FINGERPRINT__ - should update",
			currentMap: map[string]GeneralFile{
				"nofingerprint.http": {Filename: "nofingerprint.http", Content: "__NO_FINGERPRINT__"},
			},
			id:          "nofingerprint.http",
			desiredFile: GeneralFile{Filename: "nofingerprint.http", Content: "content"},
			wantCreates: 0,
			wantUpdates: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := &FileDiffGeneric[GeneralFile]{
				ToCreate: []GeneralFile{},
				ToUpdate: []GeneralFile{},
				ToDelete: []string{},
			}

			categorizeFile(tt.currentMap, tt.id, tt.desiredFile, diff)

			assert.Len(t, diff.ToCreate, tt.wantCreates)
			assert.Len(t, diff.ToUpdate, tt.wantUpdates)
		})
	}
}

// TestCompare_WithMapFiles tests Compare with MapFile type.
func TestCompare_WithMapFiles(t *testing.T) {
	ctx := context.Background()

	ops := &mockFileOps[MapFile]{
		getAllFunc: func(ctx context.Context) ([]string, error) {
			return []string{"/etc/haproxy/maps/hosts.map"}, nil
		},
		getContentFunc: func(ctx context.Context, id string) (string, error) {
			return "old.example.com backend1", nil
		},
	}

	desired := []MapFile{
		{Path: "/etc/haproxy/maps/hosts.map", Content: "new.example.com backend2"},
		{Path: "/etc/haproxy/maps/new.map", Content: "content"},
	}

	diff, err := Compare[MapFile](ctx, ops, desired, func(id, content string) MapFile {
		return MapFile{Path: id, Content: content}
	})

	require.NoError(t, err)
	assert.Len(t, diff.ToCreate, 1)
	assert.Len(t, diff.ToUpdate, 1)
	assert.Empty(t, diff.ToDelete)
}

// TestSync_WithSSLCertificates tests Sync with SSLCertificate type.
func TestSync_WithSSLCertificates(t *testing.T) {
	ctx := context.Background()

	var operations []string
	ops := &mockFileOps[SSLCertificate]{
		createFunc: func(ctx context.Context, id, content string) error {
			operations = append(operations, "create:"+id)
			return nil
		},
		updateFunc: func(ctx context.Context, id, content string) error {
			operations = append(operations, "update:"+id)
			return nil
		},
		deleteFunc: func(ctx context.Context, id string) error {
			operations = append(operations, "delete:"+id)
			return nil
		},
	}

	diff := &FileDiffGeneric[SSLCertificate]{
		ToCreate: []SSLCertificate{{Path: "new.pem", Content: "cert"}},
		ToUpdate: []SSLCertificate{{Path: "updated.pem", Content: "cert"}},
		ToDelete: []string{"old.pem"},
	}

	err := Sync[SSLCertificate](ctx, ops, diff)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{
		"create:new.pem",
		"update:updated.pem",
		"delete:old.pem",
	}, operations)
}

// TestSync_ExecutionOrder tests that Sync executes operations in the correct order.
func TestSync_ExecutionOrder(t *testing.T) {
	ctx := context.Background()

	var order []string
	ops := &mockFileOps[GeneralFile]{
		createFunc: func(ctx context.Context, id, content string) error {
			order = append(order, "create:"+id)
			return nil
		},
		updateFunc: func(ctx context.Context, id, content string) error {
			order = append(order, "update:"+id)
			return nil
		},
		deleteFunc: func(ctx context.Context, id string) error {
			order = append(order, "delete:"+id)
			return nil
		},
	}

	diff := &FileDiffGeneric[GeneralFile]{
		ToCreate: []GeneralFile{{Filename: "new.http", Content: "c1"}},
		ToUpdate: []GeneralFile{{Filename: "existing.http", Content: "c2"}},
		ToDelete: []string{"old.http"},
	}

	err := Sync[GeneralFile](ctx, ops, diff)
	require.NoError(t, err)

	// Verify order: creates, then updates, then deletes
	require.Len(t, order, 3)
	assert.Equal(t, "create:new.http", order[0])
	assert.Equal(t, "update:existing.http", order[1])
	assert.Equal(t, "delete:old.http", order[2])
}
