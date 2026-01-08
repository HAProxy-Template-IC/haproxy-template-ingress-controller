//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// skipIfCRTListNotSupported skips the test if the HAProxy version doesn't support CRT-list storage.
// CRT-list storage requires DataPlane API v3.2+.
func skipIfCRTListNotSupported(t *testing.T, env fixenv.Env) {
	t.Helper()
	dataplaneClient := TestDataplaneClient(env)
	if !dataplaneClient.Capabilities().SupportsCrtList {
		t.Skipf("Skipping test: CRT-list storage requires DataPlane API v3.2+ (detected version: %s)", dataplaneClient.DetectedVersion())
	}
}

// TestGeneralFiles tests Create, Update, and Delete operations for general files
func TestGeneralFiles(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		setup     func(t *testing.T, ctx context.Context, env fixenv.Env) // Setup initial state
		operation func(t *testing.T, ctx context.Context, env fixenv.Env) // Perform operation
		verify    func(t *testing.T, ctx context.Context, env fixenv.Env) // Verify result
	}{
		{
			name: "create-single-file",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Ensure no files exist
				client := TestDataplaneClient(env)
				files, err := client.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				for _, file := range files {
					_ = client.DeleteGeneralFile(ctx, file)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "error-files/400.http")
				_, err := client.CreateGeneralFile(ctx, "400.http", content)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify file exists
				files, err := client.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				assert.Contains(t, files, "400.http", "file should exist")

				// Verify content
				content, err := client.GetGeneralFileContent(ctx, "400.http")
				require.NoError(t, err)
				expectedContent := LoadTestFileContent(t, "error-files/400.http")
				assert.Equal(t, expectedContent, content, "content should match")
			},
		},
		{
			name: "create-multiple-files",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Ensure no files exist
				client := TestDataplaneClient(env)
				files, err := client.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				for _, file := range files {
					_ = client.DeleteGeneralFile(ctx, file)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				files := map[string]string{
					"400.http": "error-files/400.http",
					"403.http": "error-files/403.http",
					"404.http": "error-files/404.http",
				}

				for filename, testdataPath := range files {
					content := LoadTestFileContent(t, testdataPath)
					_, err := client.CreateGeneralFile(ctx, filename, content)
					require.NoError(t, err)
				}
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify all files exist
				files, err := client.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				assert.Len(t, files, 3, "should have 3 files")
				assert.Contains(t, files, "400.http")
				assert.Contains(t, files, "403.http")
				assert.Contains(t, files, "404.http")

				// Verify each file's content
				for filename, testdataPath := range map[string]string{
					"400.http": "error-files/400.http",
					"403.http": "error-files/403.http",
					"404.http": "error-files/404.http",
				} {
					content, err := client.GetGeneralFileContent(ctx, filename)
					require.NoError(t, err)
					expectedContent := LoadTestFileContent(t, testdataPath)
					assert.Equal(t, expectedContent, content, "content for %s should match", filename)
				}
			},
		},
		{
			name: "update-file-content",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create initial file
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "error-files/400.http")
				_, err := client.CreateGeneralFile(ctx, "400.http", content)
				require.NoError(t, err)
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				newContent := LoadTestFileContent(t, "error-files/custom400.http")
				_, err := client.UpdateGeneralFile(ctx, "400.http", newContent)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify file still exists
				files, err := client.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				assert.Contains(t, files, "400.http", "file should still exist")

				// Verify content was updated
				content, err := client.GetGeneralFileContent(ctx, "400.http")
				require.NoError(t, err)
				expectedContent := LoadTestFileContent(t, "error-files/custom400.http")
				assert.Equal(t, expectedContent, content, "content should be updated")
			},
		},
		{
			name: "delete-single-file",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create file to delete
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "error-files/400.http")
				_, err := client.CreateGeneralFile(ctx, "400.http", content)
				require.NoError(t, err)
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				err := client.DeleteGeneralFile(ctx, "400.http")
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify file no longer exists
				files, err := client.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				assert.NotContains(t, files, "400.http", "file should be deleted")
			},
		},
		{
			name: "delete-multiple-files",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create multiple files
				client := TestDataplaneClient(env)
				files := map[string]string{
					"400.http": "error-files/400.http",
					"403.http": "error-files/403.http",
					"404.http": "error-files/404.http",
				}

				for filename, testdataPath := range files {
					content := LoadTestFileContent(t, testdataPath)
					_, err := client.CreateGeneralFile(ctx, filename, content)
					require.NoError(t, err)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				// Delete two of the three files
				err := client.DeleteGeneralFile(ctx, "400.http")
				require.NoError(t, err)
				err = client.DeleteGeneralFile(ctx, "404.http")
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify only one file remains
				files, err := client.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				assert.Len(t, files, 1, "should have 1 file remaining")
				assert.Contains(t, files, "403.http", "403.http should still exist")
				assert.NotContains(t, files, "400.http", "400.http should be deleted")
				assert.NotContains(t, files, "404.http", "404.http should be deleted")
			},
		},
	}

	for _, tt := range testCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := fixenv.New(t)
			ctx := context.Background()

			// Setup
			if tt.setup != nil {
				tt.setup(t, ctx, env)
			}

			// Operation
			tt.operation(t, ctx, env)

			// Verify
			tt.verify(t, ctx, env)
		})
	}
}

// TestSSLCertificates tests Create, Update, and Delete operations for SSL certificates
func TestSSLCertificates(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		setup     func(t *testing.T, ctx context.Context, env fixenv.Env)
		operation func(t *testing.T, ctx context.Context, env fixenv.Env)
		verify    func(t *testing.T, ctx context.Context, env fixenv.Env)
	}{
		{
			name: "create-single-certificate",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Ensure no certificates exist
				client := TestDataplaneClient(env)
				certs, err := client.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				for _, cert := range certs {
					_ = client.DeleteSSLCertificate(ctx, cert)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "ssl-certs/example.com.pem")
				_, err := client.CreateSSLCertificate(ctx, "example.com.pem", content)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Verify certificate exists
				certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				assert.Contains(t, certs, client.SanitizeSSLCertName("example.com.pem"), "certificate should exist")

				// Verify GetSSLCertificateContent returns fingerprint (or placeholder if API doesn't support it)
				fingerprint, err := dataplaneClient.GetSSLCertificateContent(ctx, "example.com.pem")
				require.NoError(t, err)
				assert.NotEmpty(t, fingerprint, "fingerprint should not be empty")
				// Fingerprint is either a SHA256 hash or "__NO_FINGERPRINT__" placeholder
			},
		},
		{
			name: "create-multiple-certificates",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Ensure no certificates exist
				client := TestDataplaneClient(env)
				certs, err := client.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				for _, cert := range certs {
					_ = client.DeleteSSLCertificate(ctx, cert)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				certs := map[string]string{
					"example.com.pem": "ssl-certs/example.com.pem",
					"test.com.pem":    "ssl-certs/test.com.pem",
				}

				for certName, testdataPath := range certs {
					content := LoadTestFileContent(t, testdataPath)
					_, err := client.CreateSSLCertificate(ctx, certName, content)
					require.NoError(t, err)
				}
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Verify all certificates exist
				certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				assert.Len(t, certs, 2, "should have 2 certificates")
				assert.Contains(t, certs, client.SanitizeSSLCertName("example.com.pem"))
				assert.Contains(t, certs, client.SanitizeSSLCertName("test.com.pem"))

				// Verify each certificate returns a fingerprint (or placeholder if API doesn't support it)
				for _, certName := range []string{"example.com.pem", "test.com.pem"} {
					fingerprint, err := dataplaneClient.GetSSLCertificateContent(ctx, certName)
					require.NoError(t, err)
					assert.NotEmpty(t, fingerprint, "fingerprint for %s should not be empty", certName)
					// Fingerprint is either a SHA256 hash or "__NO_FINGERPRINT__" placeholder
				}
			},
		},
		{
			name: "update-certificate-content",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create initial certificate
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "ssl-certs/example.com.pem")
				_, err := client.CreateSSLCertificate(ctx, "example.com.pem", content)
				require.NoError(t, err)
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				newContent := LoadTestFileContent(t, "ssl-certs/updated.com.pem")
				_, err := client.UpdateSSLCertificate(ctx, "example.com.pem", newContent)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Verify certificate still exists
				certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				assert.Contains(t, certs, client.SanitizeSSLCertName("example.com.pem"), "certificate should still exist")

				// Verify GetSSLCertificateContent returns fingerprint (or placeholder if API doesn't support it)
				fingerprint, err := dataplaneClient.GetSSLCertificateContent(ctx, "example.com.pem")
				require.NoError(t, err)
				assert.NotEmpty(t, fingerprint, "fingerprint should not be empty")
				// Fingerprint is either a SHA256 hash or "__NO_FINGERPRINT__" placeholder
			},
		},
		{
			name: "delete-single-certificate",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create certificate to delete
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "ssl-certs/example.com.pem")
				_, err := client.CreateSSLCertificate(ctx, "example.com.pem", content)
				require.NoError(t, err)
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				err := client.DeleteSSLCertificate(ctx, "example.com.pem")
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Verify certificate no longer exists
				certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				assert.NotContains(t, certs, client.SanitizeSSLCertName("example.com.pem"), "certificate should be deleted")
			},
		},
		{
			name: "delete-multiple-certificates",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create multiple certificates
				client := TestDataplaneClient(env)
				certs := map[string]string{
					"example.com.pem": "ssl-certs/example.com.pem",
					"test.com.pem":    "ssl-certs/test.com.pem",
					"updated.com.pem": "ssl-certs/updated.com.pem",
				}

				for certName, testdataPath := range certs {
					content := LoadTestFileContent(t, testdataPath)
					_, err := client.CreateSSLCertificate(ctx, certName, content)
					require.NoError(t, err)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				// Delete two of the three certificates
				err := client.DeleteSSLCertificate(ctx, "example.com.pem")
				require.NoError(t, err)
				err = client.DeleteSSLCertificate(ctx, "updated.com.pem")
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Verify only one certificate remains
				certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				assert.Len(t, certs, 1, "should have 1 certificate remaining")
				assert.Contains(t, certs, client.SanitizeSSLCertName("test.com.pem"), "test.com.pem should still exist")
				assert.NotContains(t, certs, client.SanitizeSSLCertName("example.com.pem"), "example.com.pem should be deleted")
				assert.NotContains(t, certs, client.SanitizeSSLCertName("updated.com.pem"), "updated.com.pem should be deleted")
			},
		},
	}

	for _, tt := range testCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := fixenv.New(t)
			ctx := context.Background()

			// Setup
			if tt.setup != nil {
				tt.setup(t, ctx, env)
			}

			// Operation
			tt.operation(t, ctx, env)

			// Verify
			tt.verify(t, ctx, env)
		})
	}
}

// TestSSLCertificatesCompareAndSync tests the Compare and Sync functions for SSL certificates
func TestSSLCertificatesCompareAndSync(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)

	// Clean up any existing certificates
	certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
	require.NoError(t, err)
	for _, cert := range certs {
		_ = dataplaneClient.DeleteSSLCertificate(ctx, cert)
	}

	// Test: Compare empty state to desired certificates (should show all creates)
	t.Run("compare-empty-to-desired", func(t *testing.T) {
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "example.com.pem", Content: LoadTestFileContent(t, "ssl-certs/example.com.pem")},
			{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 2, "should have 2 certificates to create")
		assert.Len(t, diff.ToUpdate, 0, "should have 0 certificates to update")
		assert.Len(t, diff.ToDelete, 0, "should have 0 certificates to delete")
	})

	// Test: Sync creates certificates
	t.Run("sync-create-certificates", func(t *testing.T) {
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "example.com.pem", Content: LoadTestFileContent(t, "ssl-certs/example.com.pem")},
			{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncSSLCertificates(ctx, dataplaneClient, diff)
		require.NoError(t, err)

		// Verify certificates were created
		certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
		require.NoError(t, err)
		assert.Len(t, certs, 2)
		assert.Contains(t, certs, client.SanitizeSSLCertName("example.com.pem"))
		assert.Contains(t, certs, client.SanitizeSSLCertName("test.com.pem"))
	})

	// Test: Compare with certificates already present
	// NOTE: Behavior depends on whether Dataplane API provides sha256_finger_print:
	//  - With fingerprints: accurately detects changes (0 create, 1 update, 0 delete)
	//  - Without fingerprints: uses CREATE-first approach (2 create, 0 update, 0 delete)
	t.Run("compare-with-existing-certs", func(t *testing.T) {
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "example.com.pem", Content: LoadTestFileContent(t, "ssl-certs/updated.com.pem")}, // Changed content
			{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},       // Same content
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		// Verify total operations match expected
		// With fingerprints (v3.2+): 1 operation (only changed file)
		// Without fingerprints: 2 operations (CREATE-first approach)
		totalOps := len(diff.ToCreate) + len(diff.ToUpdate)
		assert.True(t, totalOps == 1 || totalOps == 2, "should have 1 (fingerprint-based) or 2 (CREATE-first) total operations, got %d", totalOps)
		assert.Len(t, diff.ToDelete, 0, "should have 0 certificates to delete")
	})

	// Test: Sync updates certificates (proper UPDATE or CREATE-with-fallback)
	t.Run("sync-update-certificates", func(t *testing.T) {
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "example.com.pem", Content: LoadTestFileContent(t, "ssl-certs/updated.com.pem")},
			{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncSSLCertificates(ctx, dataplaneClient, diff)
		require.NoError(t, err)

		// Verify certificates still exist
		certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
		require.NoError(t, err)
		assert.Len(t, certs, 2)
		assert.Contains(t, certs, client.SanitizeSSLCertName("example.com.pem"))
		assert.Contains(t, certs, client.SanitizeSSLCertName("test.com.pem"))
	})

	// Test: Compare with one certificate removed
	t.Run("compare-with-delete", func(t *testing.T) {
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},
			// example.com.pem is not in desired, so it should be deleted
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		// At least one DELETE operation (example.com.pem should be deleted)
		assert.Len(t, diff.ToDelete, 1, "should have 1 certificate to delete")
		assert.Equal(t, client.SanitizeSSLCertName("example.com.pem"), diff.ToDelete[0])

		// Remaining certificate may be CREATE (no fingerprints) or no-op (with fingerprints)
		totalOps := len(diff.ToCreate) + len(diff.ToUpdate)
		assert.LessOrEqual(t, totalOps, 1, "should have at most 1 operation for test.com.pem")
	})

	// Test: Sync deletes certificates
	t.Run("sync-delete-certificates", func(t *testing.T) {
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncSSLCertificates(ctx, dataplaneClient, diff)
		require.NoError(t, err)

		// Verify certificate was deleted
		certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
		require.NoError(t, err)
		assert.Len(t, certs, 1)
		assert.Contains(t, certs, client.SanitizeSSLCertName("test.com.pem"))
		assert.NotContains(t, certs, client.SanitizeSSLCertName("example.com.pem"))
	})

	// Test: Idempotency - running sync again with same desired state
	t.Run("sync-idempotent", func(t *testing.T) {
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		// Verify no DELETE operations (content hasn't been removed)
		assert.Len(t, diff.ToDelete, 0, "should have 0 certificates to delete")

		// With fingerprints: no operations. Without fingerprints: CREATE with fallback
		totalOps := len(diff.ToCreate) + len(diff.ToUpdate)
		assert.LessOrEqual(t, totalOps, 1, "should have at most 1 operation")

		// Verify syncing again doesn't fail (idempotent via CREATE→UPDATE fallback)
		_, err = auxiliaryfiles.SyncSSLCertificates(ctx, dataplaneClient, diff)
		require.NoError(t, err, "sync should be idempotent")
	})

	// Test: Bug regression - cert doesn't exist should CREATE, not UPDATE
	// This specifically tests the bug that was fixed where non-existent certs were marked for UPDATE
	t.Run("non-existent-cert-creates-not-updates", func(t *testing.T) {
		// Clean up all certificates
		certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
		require.NoError(t, err)
		for _, cert := range certs {
			_ = dataplaneClient.DeleteSSLCertificate(ctx, cert)
		}

		// Request a certificate that doesn't exist
		desired := []auxiliaryfiles.SSLCertificate{
			{Path: "new-cert.pem", Content: LoadTestFileContent(t, "ssl-certs/example.com.pem")},
		}

		diff, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
		require.NoError(t, err)

		// CRITICAL: Should be marked for CREATE, not UPDATE
		assert.Len(t, diff.ToCreate, 1, "non-existent cert should be marked for CREATE")
		assert.Len(t, diff.ToUpdate, 0, "non-existent cert should NOT be marked for UPDATE")
		assert.Len(t, diff.ToDelete, 0, "should have 0 certificates to delete")

		// Verify sync succeeds (would fail with old bug when trying to UPDATE non-existent cert)
		_, err = auxiliaryfiles.SyncSSLCertificates(ctx, dataplaneClient, diff)
		require.NoError(t, err, "sync should succeed when creating new certificate")

		// Verify certificate was actually created
		certs, err = dataplaneClient.GetAllSSLCertificates(ctx)
		require.NoError(t, err)
		assert.Contains(t, certs, client.SanitizeSSLCertName("new-cert.pem"), "certificate should exist after sync")
	})
}

// TestGeneralFilesCompareAndSync tests the Compare and Sync functions for general files
func TestGeneralFilesCompareAndSync(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	ctx := context.Background()
	client := TestDataplaneClient(env)

	// Clean up any existing files
	files, err := client.GetAllGeneralFiles(ctx)
	require.NoError(t, err)
	for _, file := range files {
		_ = client.DeleteGeneralFile(ctx, file)
	}

	// Test: Compare empty state to desired files (should show all creates)
	t.Run("compare-empty-to-desired", func(t *testing.T) {
		desired := []auxiliaryfiles.GeneralFile{
			{Filename: "400.http", Content: LoadTestFileContent(t, "error-files/400.http")},
			{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},
		}

		diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, client, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 2, "should have 2 files to create")
		assert.Len(t, diff.ToUpdate, 0, "should have 0 files to update")
		assert.Len(t, diff.ToDelete, 0, "should have 0 files to delete")
	})

	// Test: Sync creates files
	t.Run("sync-create-files", func(t *testing.T) {
		desired := []auxiliaryfiles.GeneralFile{
			{Filename: "400.http", Content: LoadTestFileContent(t, "error-files/400.http")},
			{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},
		}

		diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, client, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncGeneralFiles(ctx, client, diff)
		require.NoError(t, err)

		// Verify files were created
		files, err := client.GetAllGeneralFiles(ctx)
		require.NoError(t, err)
		assert.Len(t, files, 2)
		assert.Contains(t, files, "400.http")
		assert.Contains(t, files, "403.http")
	})

	// Test: Compare with one file changed (should show update)
	t.Run("compare-with-update", func(t *testing.T) {
		desired := []auxiliaryfiles.GeneralFile{
			{Filename: "400.http", Content: LoadTestFileContent(t, "error-files/custom400.http")}, // Changed
			{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},       // Same
		}

		diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, client, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 0, "should have 0 files to create")
		assert.Len(t, diff.ToUpdate, 1, "should have 1 file to update")
		assert.Len(t, diff.ToDelete, 0, "should have 0 files to delete")
		assert.Equal(t, "400.http", diff.ToUpdate[0].Filename)
	})

	// Test: Sync updates files
	t.Run("sync-update-files", func(t *testing.T) {
		desired := []auxiliaryfiles.GeneralFile{
			{Filename: "400.http", Content: LoadTestFileContent(t, "error-files/custom400.http")},
			{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},
		}

		diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, client, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncGeneralFiles(ctx, client, diff)
		require.NoError(t, err)

		// Verify file content was updated
		content, err := client.GetGeneralFileContent(ctx, "400.http")
		require.NoError(t, err)
		expectedContent := LoadTestFileContent(t, "error-files/custom400.http")
		assert.Equal(t, expectedContent, content)
	})

	// Test: Compare with one file removed (should show delete)
	t.Run("compare-with-delete", func(t *testing.T) {
		desired := []auxiliaryfiles.GeneralFile{
			{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},
			// 400.http is not in desired, so it should be deleted
		}

		diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, client, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 0, "should have 0 files to create")
		assert.Len(t, diff.ToUpdate, 0, "should have 0 files to update")
		assert.Len(t, diff.ToDelete, 1, "should have 1 file to delete")
		assert.Equal(t, "400.http", diff.ToDelete[0])
	})

	// Test: Sync deletes files
	t.Run("sync-delete-files", func(t *testing.T) {
		desired := []auxiliaryfiles.GeneralFile{
			{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},
		}

		diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, client, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncGeneralFiles(ctx, client, diff)
		require.NoError(t, err)

		// Verify file was deleted
		files, err := client.GetAllGeneralFiles(ctx)
		require.NoError(t, err)
		assert.Len(t, files, 1)
		assert.Contains(t, files, "403.http")
		assert.NotContains(t, files, "400.http")
	})

	// Test: Idempotency - running sync again with same desired state should do nothing
	t.Run("sync-idempotent", func(t *testing.T) {
		desired := []auxiliaryfiles.GeneralFile{
			{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},
		}

		diff, err := auxiliaryfiles.CompareGeneralFiles(ctx, client, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 0, "should have 0 files to create")
		assert.Len(t, diff.ToUpdate, 0, "should have 0 files to update")
		assert.Len(t, diff.ToDelete, 0, "should have 0 files to delete")
	})
}

// LoadTestFileContent loads a file from testdata and returns its content as a string
func LoadTestFileContent(t *testing.T, relativePath string) string {
	fullPath := filepath.Join("testdata", relativePath)
	content, err := os.ReadFile(fullPath)
	require.NoError(t, err, "failed to read test file %s", relativePath)
	return string(content)
}

// TestCRTLists tests Create, Update, and Delete operations for CRT-list files
func TestCRTLists(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		setup     func(t *testing.T, ctx context.Context, env fixenv.Env)
		operation func(t *testing.T, ctx context.Context, env fixenv.Env)
		verify    func(t *testing.T, ctx context.Context, env fixenv.Env)
	}{
		{
			name: "create-single-crtlist",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Ensure no crt-list files exist
				client := TestDataplaneClient(env)
				crtlists, err := client.GetAllCRTListFiles(ctx)
				require.NoError(t, err)
				for _, crtlist := range crtlists {
					_ = client.DeleteCRTListFile(ctx, crtlist)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")
				_, err := client.CreateCRTListFile(ctx, "crt-list.txt", content)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify file exists
				crtlists, err := client.GetAllCRTListFiles(ctx)
				require.NoError(t, err)
				assert.Contains(t, crtlists, "crt-list.txt", "crt-list file should exist")

				// Verify content
				content, err := client.GetCRTListFileContent(ctx, "crt-list.txt")
				require.NoError(t, err)
				expectedContent := LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")
				assert.Equal(t, expectedContent, content, "content should match")
			},
		},
		{
			name: "create-multiple-crtlists",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Ensure no crt-list files exist
				client := TestDataplaneClient(env)
				crtlists, err := client.GetAllCRTListFiles(ctx)
				require.NoError(t, err)
				for _, crtlist := range crtlists {
					_ = client.DeleteCRTListFile(ctx, crtlist)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				files := map[string]string{
					"crt-list.txt":         "crt-lists/basic-crt-list.txt",
					"crt-list-options.txt": "crt-lists/crt-list-with-options.txt",
					"single-cert.txt":      "crt-lists/crt-list-single-cert.txt",
				}

				for filename, testdataPath := range files {
					content := LoadTestFileContent(t, testdataPath)
					_, err := client.CreateCRTListFile(ctx, filename, content)
					require.NoError(t, err)
				}
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify all files exist
				crtlists, err := client.GetAllCRTListFiles(ctx)
				require.NoError(t, err)
				assert.Len(t, crtlists, 3, "should have 3 crt-list files")
				assert.Contains(t, crtlists, "crt-list.txt")
				assert.Contains(t, crtlists, "crt-list-options.txt")
				assert.Contains(t, crtlists, "single-cert.txt")

				// Verify each file's content
				for filename, testdataPath := range map[string]string{
					"crt-list.txt":         "crt-lists/basic-crt-list.txt",
					"crt-list-options.txt": "crt-lists/crt-list-with-options.txt",
					"single-cert.txt":      "crt-lists/crt-list-single-cert.txt",
				} {
					content, err := client.GetCRTListFileContent(ctx, filename)
					require.NoError(t, err)
					expectedContent := LoadTestFileContent(t, testdataPath)
					assert.Equal(t, expectedContent, content, "content for %s should match", filename)
				}
			},
		},
		{
			name: "update-crtlist-content",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create initial file
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")
				_, err := client.CreateCRTListFile(ctx, "crt-list.txt", content)
				require.NoError(t, err)
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				newContent := LoadTestFileContent(t, "crt-lists/crt-list-updated.txt")
				_, err := client.UpdateCRTListFile(ctx, "crt-list.txt", newContent)
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify file still exists
				crtlists, err := client.GetAllCRTListFiles(ctx)
				require.NoError(t, err)
				assert.Contains(t, crtlists, "crt-list.txt", "crt-list file should still exist")

				// Verify content was updated
				content, err := client.GetCRTListFileContent(ctx, "crt-list.txt")
				require.NoError(t, err)
				expectedContent := LoadTestFileContent(t, "crt-lists/crt-list-updated.txt")
				assert.Equal(t, expectedContent, content, "content should be updated")
			},
		},
		{
			name: "delete-single-crtlist",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create file to delete
				client := TestDataplaneClient(env)
				content := LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")
				_, err := client.CreateCRTListFile(ctx, "crt-list.txt", content)
				require.NoError(t, err)
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				err := client.DeleteCRTListFile(ctx, "crt-list.txt")
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify file no longer exists
				crtlists, err := client.GetAllCRTListFiles(ctx)
				require.NoError(t, err)
				assert.NotContains(t, crtlists, "crt-list.txt", "crt-list file should be deleted")
			},
		},
		{
			name: "delete-multiple-crtlists",
			setup: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				// Create multiple files
				client := TestDataplaneClient(env)
				files := map[string]string{
					"crt-list.txt":         "crt-lists/basic-crt-list.txt",
					"crt-list-options.txt": "crt-lists/crt-list-with-options.txt",
					"single-cert.txt":      "crt-lists/crt-list-single-cert.txt",
				}

				for filename, testdataPath := range files {
					content := LoadTestFileContent(t, testdataPath)
					_, err := client.CreateCRTListFile(ctx, filename, content)
					require.NoError(t, err)
				}
			},
			operation: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)
				// Delete two of the three files
				err := client.DeleteCRTListFile(ctx, "crt-list.txt")
				require.NoError(t, err)
				err = client.DeleteCRTListFile(ctx, "single-cert.txt")
				require.NoError(t, err)
			},
			verify: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				client := TestDataplaneClient(env)

				// Verify only one file remains
				crtlists, err := client.GetAllCRTListFiles(ctx)
				require.NoError(t, err)
				assert.Len(t, crtlists, 1, "should have 1 crt-list file remaining")
				assert.Contains(t, crtlists, "crt-list-options.txt", "crt-list-options.txt should still exist")
				assert.NotContains(t, crtlists, "crt-list.txt", "crt-list.txt should be deleted")
				assert.NotContains(t, crtlists, "single-cert.txt", "single-cert.txt should be deleted")
			},
		},
	}

	for _, tt := range testCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := fixenv.New(t)
			ctx := context.Background()

			// Skip if CRT-list storage is not supported (requires DataPlane API v3.2+)
			skipIfCRTListNotSupported(t, env)

			// Setup
			if tt.setup != nil {
				tt.setup(t, ctx, env)
			}

			// Operation
			tt.operation(t, ctx, env)

			// Verify
			tt.verify(t, ctx, env)
		})
	}
}

// TestCRTListsCompareAndSync tests the Compare and Sync workflow for CRT-lists
// through sequential phases that build upon each other.
//
// NOTE: CRT-lists are stored as general files to avoid HAProxy reloads on create.
// The native CRT-list API triggers a reload but doesn't support skip_reload parameter.
// General file CREATE returns HTTP 201 without triggering a reload.
func TestCRTListsCompareAndSync(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)

	// No version skip needed - general file storage works on all HAProxy versions
	ctx := context.Background()
	client := TestDataplaneClient(env)

	// Clean up any existing general files that might be CRT-lists from previous runs
	generalFiles, err := client.GetAllGeneralFiles(ctx)
	require.NoError(t, err)
	for _, file := range generalFiles {
		_ = client.DeleteGeneralFile(ctx, file)
	}

	// Test: Compare empty state to desired crt-lists (should show all creates)
	t.Run("compare-empty-to-desired", func(t *testing.T) {
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "example.com.txt", Content: LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")},
			{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")},
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		assert.Len(t, diff.ToCreate, 2, "should have 2 crt-lists to create")
		assert.Len(t, diff.ToUpdate, 0, "should have 0 crt-lists to update")
		assert.Len(t, diff.ToDelete, 0, "should have 0 crt-lists to delete")
	})

	// Test: Sync creates crt-lists
	t.Run("sync-create-crtlists", func(t *testing.T) {
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "example.com.txt", Content: LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")},
			{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")},
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncCRTLists(ctx, client, diff)
		require.NoError(t, err)

		// Verify crt-lists were created (stored as general files)
		// Note: filenames are sanitized (dots → underscores) by sanitizeStorageName()
		generalFiles, err := client.GetAllGeneralFiles(ctx)
		require.NoError(t, err)
		assert.Len(t, generalFiles, 2)
		assert.Contains(t, generalFiles, "example_com.txt")
		assert.Contains(t, generalFiles, "test_com.txt")
	})

	// Test: Compare with crt-lists already present
	// CRT-lists use direct content comparison, so changes are accurately detected
	t.Run("compare-with-existing-crtlists", func(t *testing.T) {
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "example.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-updated.txt")},  // Changed content
			{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")}, // Same content
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		// Should detect the change via content comparison
		// Note: paths in diff are sanitized (dots → underscores) to match API storage
		assert.Len(t, diff.ToCreate, 0, "should have 0 crt-lists to create")
		assert.Len(t, diff.ToUpdate, 1, "should have 1 crt-list to update")
		assert.Len(t, diff.ToDelete, 0, "should have 0 crt-lists to delete")
		assert.Equal(t, "example_com.txt", diff.ToUpdate[0].Path)
	})

	// Test: Sync updates crt-lists
	t.Run("sync-update-crtlists", func(t *testing.T) {
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "example.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-updated.txt")},
			{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")},
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncCRTLists(ctx, client, diff)
		require.NoError(t, err)

		// Verify crt-lists still exist (stored as general files)
		// Note: filenames are sanitized (dots → underscores) by sanitizeStorageName()
		generalFiles, err := client.GetAllGeneralFiles(ctx)
		require.NoError(t, err)
		assert.Len(t, generalFiles, 2)
		assert.Contains(t, generalFiles, "example_com.txt")
		assert.Contains(t, generalFiles, "test_com.txt")

		// Verify content was updated (use sanitized filename)
		content, err := client.GetGeneralFileContent(ctx, "example_com.txt")
		require.NoError(t, err)
		assert.Equal(t, LoadTestFileContent(t, "crt-lists/crt-list-updated.txt"), content)
	})

	// Test: Compare with one crt-list removed
	t.Run("compare-with-delete", func(t *testing.T) {
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")},
			// example.com.crtlist is not in desired, so it should be deleted
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		// Note: paths in diff are sanitized (dots → underscores) to match API storage
		assert.Len(t, diff.ToCreate, 0, "should have 0 crt-lists to create")
		assert.Len(t, diff.ToUpdate, 0, "should have 0 crt-lists to update")
		assert.Len(t, diff.ToDelete, 1, "should have 1 crt-list to delete")
		assert.Equal(t, "example_com.txt", diff.ToDelete[0])
	})

	// Test: Sync deletes crt-lists
	t.Run("sync-delete-crtlists", func(t *testing.T) {
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")},
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		_, err = auxiliaryfiles.SyncCRTLists(ctx, client, diff)
		require.NoError(t, err)

		// Verify crt-list was deleted (from general files storage)
		// Note: filenames are sanitized (dots → underscores) by sanitizeStorageName()
		generalFiles, err := client.GetAllGeneralFiles(ctx)
		require.NoError(t, err)
		assert.Len(t, generalFiles, 1)
		assert.Contains(t, generalFiles, "test_com.txt")
		assert.NotContains(t, generalFiles, "example_com.txt")
	})

	// Test: Idempotency - running sync again with same desired state
	t.Run("sync-idempotent", func(t *testing.T) {
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")},
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		// Verify no operations needed
		assert.Len(t, diff.ToCreate, 0, "should have 0 crt-lists to create")
		assert.Len(t, diff.ToUpdate, 0, "should have 0 crt-lists to update")
		assert.Len(t, diff.ToDelete, 0, "should have 0 crt-lists to delete")

		// Verify syncing again doesn't fail (idempotent)
		_, err = auxiliaryfiles.SyncCRTLists(ctx, client, diff)
		require.NoError(t, err, "sync should be idempotent")
	})

	// Test: Regression - crt-list doesn't exist should CREATE, not UPDATE
	t.Run("non-existent-crtlist-creates-not-updates", func(t *testing.T) {
		// Clean up all general files (CRT-lists are stored as general files)
		generalFiles, err := client.GetAllGeneralFiles(ctx)
		require.NoError(t, err)
		for _, file := range generalFiles {
			_ = client.DeleteGeneralFile(ctx, file)
		}

		// Request a crt-list that doesn't exist
		desired := []auxiliaryfiles.CRTListFile{
			{Path: "new-list.txt", Content: LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")},
		}

		diff, err := auxiliaryfiles.CompareCRTLists(ctx, client, desired)
		require.NoError(t, err)

		// CRITICAL: Should be marked for CREATE, not UPDATE
		assert.Len(t, diff.ToCreate, 1, "non-existent crt-list should be marked for CREATE")
		assert.Len(t, diff.ToUpdate, 0, "non-existent crt-list should NOT be marked for UPDATE")
		assert.Len(t, diff.ToDelete, 0, "should have 0 crt-lists to delete")

		// Verify sync succeeds
		_, err = auxiliaryfiles.SyncCRTLists(ctx, client, diff)
		require.NoError(t, err, "sync should succeed when creating new crt-list")

		// Verify crt-list was actually created (in general files storage)
		generalFiles, err = client.GetAllGeneralFiles(ctx)
		require.NoError(t, err)
		assert.Contains(t, generalFiles, "new-list.txt", "crt-list should exist after sync")
	})
}

// TestAuxiliaryFilesIdempotency tests that syncing the same content twice results in zero operations.
// This is a strict test - after the first sync, the second comparison MUST return empty diffs.
// This test specifically catches false positive change detection bugs.
func TestAuxiliaryFilesIdempotency(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		test func(t *testing.T, ctx context.Context, env fixenv.Env)
	}{
		{
			name: "ssl-certificates",
			test: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Clean up any existing certificates
				certs, err := dataplaneClient.GetAllSSLCertificates(ctx)
				require.NoError(t, err)
				for _, cert := range certs {
					_ = dataplaneClient.DeleteSSLCertificate(ctx, cert)
				}

				desired := []auxiliaryfiles.SSLCertificate{
					{Path: "example.com.pem", Content: LoadTestFileContent(t, "ssl-certs/example.com.pem")},
					{Path: "test.com.pem", Content: LoadTestFileContent(t, "ssl-certs/test.com.pem")},
				}

				// Phase 1: Sync files
				diff1, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
				require.NoError(t, err)
				_, err = auxiliaryfiles.SyncSSLCertificates(ctx, dataplaneClient, diff1)
				require.NoError(t, err)

				// Phase 2: Compare again with SAME content - MUST have zero operations
				diff2, err := auxiliaryfiles.CompareSSLCertificates(ctx, dataplaneClient, desired)
				require.NoError(t, err)

				// STRICT ASSERTION: idempotent sync must have zero operations
				assert.Empty(t, diff2.ToCreate, "idempotent sync should have 0 creates")
				assert.Empty(t, diff2.ToUpdate, "idempotent sync should have 0 updates")
				assert.Empty(t, diff2.ToDelete, "idempotent sync should have 0 deletes")
			},
		},
		{
			name: "map-files",
			test: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Clean up any existing map files
				maps, err := dataplaneClient.GetAllMapFiles(ctx)
				require.NoError(t, err)
				for _, mapFile := range maps {
					_ = dataplaneClient.DeleteMapFile(ctx, mapFile)
				}

				desired := []auxiliaryfiles.MapFile{
					{Path: "domains.map", Content: LoadTestFileContent(t, "map-files/domains.map")},
					{Path: "paths.map", Content: LoadTestFileContent(t, "map-files/paths.map")},
				}

				// Phase 1: Sync files
				diff1, err := auxiliaryfiles.CompareMapFiles(ctx, dataplaneClient, desired)
				require.NoError(t, err)
				_, err = auxiliaryfiles.SyncMapFiles(ctx, dataplaneClient, diff1)
				require.NoError(t, err)

				// Phase 2: Compare again with SAME content - MUST have zero operations
				diff2, err := auxiliaryfiles.CompareMapFiles(ctx, dataplaneClient, desired)
				require.NoError(t, err)

				// STRICT ASSERTION: idempotent sync must have zero operations
				assert.Empty(t, diff2.ToCreate, "idempotent sync should have 0 creates")
				assert.Empty(t, diff2.ToUpdate, "idempotent sync should have 0 updates")
				assert.Empty(t, diff2.ToDelete, "idempotent sync should have 0 deletes")
			},
		},
		{
			name: "map-files-without-trailing-newline",
			test: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Clean up any existing map files
				maps, err := dataplaneClient.GetAllMapFiles(ctx)
				require.NoError(t, err)
				for _, mapFile := range maps {
					_ = dataplaneClient.DeleteMapFile(ctx, mapFile)
				}

				// Content WITHOUT trailing newline - this specifically tests
				// the normalization fix for map file comparison
				// (tests if API adds trailing newline to stored content)
				contentWithoutNewline := "example.com backend1\ntest.com backend2"

				desired := []auxiliaryfiles.MapFile{
					{Path: "domains.map", Content: contentWithoutNewline},
				}

				// Phase 1: Sync files
				diff1, err := auxiliaryfiles.CompareMapFiles(ctx, dataplaneClient, desired)
				require.NoError(t, err)
				_, err = auxiliaryfiles.SyncMapFiles(ctx, dataplaneClient, diff1)
				require.NoError(t, err)

				// Phase 2: Compare again with SAME content - MUST have zero operations
				diff2, err := auxiliaryfiles.CompareMapFiles(ctx, dataplaneClient, desired)
				require.NoError(t, err)

				// Debug: if test fails, show the actual difference
				if len(diff2.ToUpdate) > 0 {
					for _, f := range diff2.ToUpdate {
						t.Logf("False positive update for %s", f.Path)
						apiContent, _ := dataplaneClient.GetMapFileContent(ctx, f.Path)
						t.Logf("Desired (len=%d): %q", len(f.Content), f.Content)
						t.Logf("API     (len=%d): %q", len(apiContent), apiContent)
					}
				}

				// STRICT ASSERTION: idempotent sync must have zero operations
				assert.Empty(t, diff2.ToCreate, "idempotent sync should have 0 creates")
				assert.Empty(t, diff2.ToUpdate, "idempotent sync should have 0 updates")
				assert.Empty(t, diff2.ToDelete, "idempotent sync should have 0 deletes")
			},
		},
		{
			name: "general-files",
			test: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Clean up any existing general files
				files, err := dataplaneClient.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				for _, file := range files {
					_ = dataplaneClient.DeleteGeneralFile(ctx, file)
				}

				desired := []auxiliaryfiles.GeneralFile{
					{Filename: "400.http", Content: LoadTestFileContent(t, "error-files/400.http")},
					{Filename: "403.http", Content: LoadTestFileContent(t, "error-files/403.http")},
				}

				// Phase 1: Sync files
				diff1, err := auxiliaryfiles.CompareGeneralFiles(ctx, dataplaneClient, desired)
				require.NoError(t, err)
				_, err = auxiliaryfiles.SyncGeneralFiles(ctx, dataplaneClient, diff1)
				require.NoError(t, err)

				// Phase 2: Compare again with SAME content - MUST have zero operations
				diff2, err := auxiliaryfiles.CompareGeneralFiles(ctx, dataplaneClient, desired)
				require.NoError(t, err)

				// STRICT ASSERTION: idempotent sync must have zero operations
				assert.Empty(t, diff2.ToCreate, "idempotent sync should have 0 creates")
				assert.Empty(t, diff2.ToUpdate, "idempotent sync should have 0 updates")
				assert.Empty(t, diff2.ToDelete, "idempotent sync should have 0 deletes")
			},
		},
		{
			name: "crt-lists",
			test: func(t *testing.T, ctx context.Context, env fixenv.Env) {
				dataplaneClient := TestDataplaneClient(env)

				// Clean up any existing general files (CRT-lists are stored as general files)
				files, err := dataplaneClient.GetAllGeneralFiles(ctx)
				require.NoError(t, err)
				for _, file := range files {
					_ = dataplaneClient.DeleteGeneralFile(ctx, file)
				}

				desired := []auxiliaryfiles.CRTListFile{
					{Path: "example.com.txt", Content: LoadTestFileContent(t, "crt-lists/basic-crt-list.txt")},
					{Path: "test.com.txt", Content: LoadTestFileContent(t, "crt-lists/crt-list-single-cert.txt")},
				}

				// Phase 1: Sync files
				diff1, err := auxiliaryfiles.CompareCRTLists(ctx, dataplaneClient, desired)
				require.NoError(t, err)
				_, err = auxiliaryfiles.SyncCRTLists(ctx, dataplaneClient, diff1)
				require.NoError(t, err)

				// Phase 2: Compare again with SAME content - MUST have zero operations
				diff2, err := auxiliaryfiles.CompareCRTLists(ctx, dataplaneClient, desired)
				require.NoError(t, err)

				// STRICT ASSERTION: idempotent sync must have zero operations
				assert.Empty(t, diff2.ToCreate, "idempotent sync should have 0 creates")
				assert.Empty(t, diff2.ToUpdate, "idempotent sync should have 0 updates")
				assert.Empty(t, diff2.ToDelete, "idempotent sync should have 0 deletes")
			},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := fixenv.New(t)
			ctx := context.Background()
			tt.test(t, ctx, env)
		})
	}
}
