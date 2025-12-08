//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/dataplane"
	"haproxy-template-ic/pkg/dataplane/auxiliaryfiles"
)

// TestMain sets up package-scoped fixtures and runs tests
func TestMain(m *testing.M) {
	fixenv.RunTests(m)
}

// syncTestCase defines a single sync test scenario
type syncTestCase struct {
	name              string
	initialConfigFile string // Config to push initially
	desiredConfigFile string // Target config to reach

	// Expected operation counts
	expectedCreates int
	expectedUpdates int
	expectedDeletes int

	// Expected operations (validated by operation descriptions)
	// Example: []string{"Create backend 'web'", "Create server 'srv1' in backend 'web'"}
	expectedOperations []string

	// Reload expectations
	// expectedReload indicates whether a HAProxy reload should be triggered.
	// true = expects status 202 with Reload-ID header
	// false = expects status 200 without reload
	expectedReload bool

	// Fallback to raw push expectation
	// expectedFallbackToRaw indicates whether the sync is expected to fall back to raw config push.
	// Fallback typically occurs when fine-grained sync encounters transactional conflicts
	// in the HAProxy Dataplane API (e.g., 409 conflicts when creating certain resources).
	// true = expects fallback to raw push (operation tracking may be less granular)
	// false = expects fine-grained sync to succeed without fallback
	expectedFallbackToRaw bool

	// Auxiliary files for INITIAL configuration
	// These files are uploaded before pushing the initial config to ensure it validates
	// Map: HAProxy file path → testdata file to load
	// Example: map[string]string{"/etc/haproxy/errors/400.http": "error-files/400.http"}
	initialGeneralFiles    map[string]string
	initialSSLCertificates map[string]string
	initialMapFiles        map[string]string

	// Auxiliary files for DESIRED configuration (used in sync operation)
	// These files are synced as part of the high-level Sync() call
	// Map: HAProxy file path → testdata file to load
	// Example: map[string]string{"/etc/haproxy/errors/400.http": "error-files/400.http"}
	generalFiles map[string]string

	// SSL certificates to sync before config operations
	// Map: SSL certificate name → testdata file to load
	// Example: map[string]string{"example.com.pem": "ssl-certs/example.com.pem"}
	sslCertificates map[string]string

	// Map files to sync before config operations
	// Map: map file path → testdata file to load
	// Example: map[string]string{"domains.map": "map-files/domains.map"}
	mapFiles map[string]string

	// Optional: Verify auxiliary file content after sync
	// These fields enable verification that auxiliary files were actually updated
	// Map: filename → testdata file to compare against
	// Example: map[string]string{"domains.map": "map-files/domains-updated.map"}
	verifyMapFiles        map[string]string
	verifyGeneralFiles    map[string]string
	verifySSLCertificates map[string]string

	// Skip reason for unsupported features (test-first approach)
	// If set, test will be skipped with this message
	skipReason string

	// skipFunc is a callback for dynamic capability-based skipping.
	// Called after env is created but before test execution.
	// Use this for version-gated features that require capability checks.
	skipFunc func(t *testing.T, env fixenv.Env)
}

// runSyncTest executes a single sync test case with full validation
func runSyncTest(t *testing.T, tc syncTestCase) {
	// Skip if this tests an unsupported feature (test-first approach)
	if tc.skipReason != "" {
		t.Skip(tc.skipReason)
	}

	env := fixenv.New(t)

	// Dynamic capability-based skip (must be after env creation)
	if tc.skipFunc != nil {
		tc.skipFunc(t, env)
	}

	ctx := context.Background()

	// Request fixtures
	client := TestDataplaneClient(env)            // Low-level client for setup/verification
	dpClient := TestDataplaneHighLevelClient(env) // High-level client for Sync API
	parser := TestParser(env)
	comp := TestComparator(env)

	// Step 0.5: Prepare auxiliary files (will be synced by Sync API)
	auxFiles := &dataplane.AuxiliaryFiles{
		GeneralFiles:    make([]auxiliaryfiles.GeneralFile, 0),
		SSLCertificates: make([]auxiliaryfiles.SSLCertificate, 0),
		MapFiles:        make([]auxiliaryfiles.MapFile, 0),
	}

	// Load general files from testdata
	if len(tc.generalFiles) > 0 {
		t.Logf("Preparing %d general files", len(tc.generalFiles))
		for haproxyPath, testdataFile := range tc.generalFiles {
			fullPath := filepath.Join("testdata", testdataFile)
			content, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read test file %s", testdataFile)

			auxFiles.GeneralFiles = append(auxFiles.GeneralFiles, auxiliaryfiles.GeneralFile{
				Filename: filepath.Base(haproxyPath),
				Content:  string(content),
			})
			t.Logf("  - %s ← %s", haproxyPath, testdataFile)
		}
	}

	// Load SSL certificates from testdata
	if len(tc.sslCertificates) > 0 {
		t.Logf("Preparing %d SSL certificates", len(tc.sslCertificates))
		for certName, testdataFile := range tc.sslCertificates {
			fullPath := filepath.Join("testdata", testdataFile)
			content, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read test cert %s", testdataFile)

			auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
				Path:    certName,
				Content: string(content),
			})
			t.Logf("  - %s ← %s", certName, testdataFile)
		}
	}

	// Load map files from testdata
	if len(tc.mapFiles) > 0 {
		t.Logf("Preparing %d map files", len(tc.mapFiles))
		for mapName, testdataFile := range tc.mapFiles {
			fullPath := filepath.Join("testdata", testdataFile)
			content, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read test map file %s", testdataFile)

			auxFiles.MapFiles = append(auxFiles.MapFiles, auxiliaryfiles.MapFile{
				Path:    mapName,
				Content: string(content),
			})
			t.Logf("  - %s ← %s", mapName, testdataFile)
		}
	}

	// Step 1: Upload INITIAL auxiliary files (if any) before pushing initial config
	// This ensures files exist when HAProxy validates the initial configuration
	if len(tc.initialGeneralFiles) > 0 {
		t.Logf("Uploading %d initial general files", len(tc.initialGeneralFiles))
		for haproxyPath, testdataFile := range tc.initialGeneralFiles {
			fullPath := filepath.Join("testdata", testdataFile)
			content, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read initial test file %s", testdataFile)

			filename := filepath.Base(haproxyPath)
			err = client.CreateGeneralFile(ctx, filename, string(content))
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				require.NoError(t, err, "failed to upload initial general file %s", filename)
			}
			t.Logf("  - Uploaded initial file: %s", filename)
		}
	}

	if len(tc.initialSSLCertificates) > 0 {
		t.Logf("Uploading %d initial SSL certificates", len(tc.initialSSLCertificates))
		for certName, testdataFile := range tc.initialSSLCertificates {
			fullPath := filepath.Join("testdata", testdataFile)
			content, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read initial SSL cert %s", testdataFile)

			err = client.CreateSSLCertificate(ctx, certName, string(content))
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				require.NoError(t, err, "failed to upload initial SSL certificate %s", certName)
			}
			t.Logf("  - Uploaded initial SSL cert: %s", certName)
		}
	}

	if len(tc.initialMapFiles) > 0 {
		t.Logf("Uploading %d initial map files", len(tc.initialMapFiles))
		for mapName, testdataFile := range tc.initialMapFiles {
			fullPath := filepath.Join("testdata", testdataFile)
			content, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read initial map file %s", testdataFile)

			err = client.CreateMapFile(ctx, mapName, string(content))
			if err != nil && !strings.Contains(err.Error(), "already exists") {
				require.NoError(t, err, "failed to upload initial map file %s", mapName)
			}
			t.Logf("  - Uploaded initial map file: %s", mapName)
		}
	}

	// Step 2: Push initial configuration (using low-level client)
	initialConfigContent := LoadTestConfig(t, tc.initialConfigFile)
	_, err := client.PushRawConfiguration(ctx, initialConfigContent)
	require.NoError(t, err, "pushing initial config should succeed")
	t.Logf("Pushed initial config: %s", tc.initialConfigFile)

	// Step 3: Sync to desired configuration using high-level API
	// This replaces the manual parse/compare/apply steps
	desiredConfigContent := LoadTestConfig(t, tc.desiredConfigFile)
	result, err := dpClient.Sync(ctx, desiredConfigContent, auxFiles, nil)
	require.NoError(t, err, "sync should succeed")
	t.Logf("Sync completed: %d operations, reload=%v, reloadID=%s",
		len(result.AppliedOperations), result.ReloadTriggered, result.ReloadID)

	// Step 3: Assert operation counts from sync result
	// Skip operation checks when fallback to raw push is expected, as operations aren't tracked during raw push
	if !tc.expectedFallbackToRaw {
		assert.Equal(t, tc.expectedCreates, result.Details.Creates, "create count mismatch")
		assert.Equal(t, tc.expectedUpdates, result.Details.Updates, "update count mismatch")
		assert.Equal(t, tc.expectedDeletes, result.Details.Deletes, "delete count mismatch")

		// Step 4: Validate operations by their descriptions
		if tc.expectedOperations != nil && len(tc.expectedOperations) > 0 {
			actualOps := make([]string, len(result.AppliedOperations))
			for i, op := range result.AppliedOperations {
				actualOps[i] = op.Description
			}
			assert.ElementsMatch(t, tc.expectedOperations, actualOps,
				"operation descriptions mismatch")
		}
	}

	// Step 5: Validate reload expectations
	if tc.expectedReload {
		assert.True(t, result.ReloadTriggered, "expected reload to be triggered")
		assert.NotEmpty(t, result.ReloadID, "expected reload ID to be set")
	} else {
		assert.False(t, result.ReloadTriggered, "expected no reload")
		assert.Empty(t, result.ReloadID, "expected no reload ID")
	}

	// Step 5.5: Validate fallback to raw push expectation
	if tc.expectedFallbackToRaw {
		assert.True(t, result.FallbackToRaw, "expected fallback to raw config push")
		t.Logf("⚠️  Fallback to raw config push occurred (expected)")
	} else {
		assert.False(t, result.FallbackToRaw, "expected fine-grained sync without fallback")
	}

	// Step 6: Parse desired config for idempotency verification
	desiredConfig, err := parser.ParseFromString(desiredConfigContent)
	require.NoError(t, err, "parsing desired config should succeed")
	t.Logf("Parsed desired config for verification")

	// Step 7: Read final config via API
	finalConfigStr, err := client.GetRawConfiguration(ctx)
	require.NoError(t, err, "reading final config should succeed")

	// Step 8: Parse final config
	finalConfig, err := parser.ParseFromString(finalConfigStr)
	require.NoError(t, err, "parsing final config should succeed")
	t.Logf("Parsed final config from API")

	// Step 9: Calculate verification diff (final → desired)
	verifyDiff, err := comp.Compare(finalConfig, desiredConfig)
	require.NoError(t, err, "verification comparison should succeed")
	t.Logf("Verification diff: %d creates, %d updates, %d deletes",
		verifyDiff.Summary.TotalCreates, verifyDiff.Summary.TotalUpdates, verifyDiff.Summary.TotalDeletes)

	// Step 10: Assert verification diff is EMPTY (idempotency check)
	// If there are differences, log them for debugging
	if verifyDiff.Summary.TotalCreates > 0 || verifyDiff.Summary.TotalUpdates > 0 || verifyDiff.Summary.TotalDeletes > 0 {
		t.Logf("⚠️  Idempotency check detected differences:")
		for _, op := range verifyDiff.Operations {
			opType := "unknown"
			switch op.Type() {
			case 0:
				opType = "CREATE"
			case 1:
				opType = "UPDATE"
			case 2:
				opType = "DELETE"
			}
			t.Logf("  - %s: %s", opType, op.Describe())
		}
	}

	assert.Equal(t, 0, verifyDiff.Summary.TotalCreates,
		"final config should match desired (no creates needed)")
	assert.Equal(t, 0, verifyDiff.Summary.TotalUpdates,
		"final config should match desired (no updates needed)")
	assert.Equal(t, 0, verifyDiff.Summary.TotalDeletes,
		"final config should match desired (no deletes needed)")

	if verifyDiff.Summary.TotalCreates == 0 &&
		verifyDiff.Summary.TotalUpdates == 0 &&
		verifyDiff.Summary.TotalDeletes == 0 {
		t.Logf("✓ Idempotency check passed: final config matches desired config")
	}

	// Step 11: Verify auxiliary file content if requested
	if len(tc.verifyMapFiles) > 0 {
		t.Logf("Verifying %d map files", len(tc.verifyMapFiles))
		for filename, testdataFile := range tc.verifyMapFiles {
			// Load expected content from testdata
			fullPath := filepath.Join("testdata", testdataFile)
			expectedContent, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read expected map file %s", testdataFile)

			// Fetch actual content from HAProxy
			actualContent, err := client.GetMapFileContent(ctx, filename)
			require.NoError(t, err, "failed to get map file %s from HAProxy", filename)

			// Compare
			assert.Equal(t, string(expectedContent), actualContent,
				"map file %s content mismatch", filename)
			t.Logf("  ✓ Map file %s matches expected content", filename)
		}
	}

	if len(tc.verifyGeneralFiles) > 0 {
		t.Logf("Verifying %d general files", len(tc.verifyGeneralFiles))
		for filename, testdataFile := range tc.verifyGeneralFiles {
			// Load expected content from testdata
			fullPath := filepath.Join("testdata", testdataFile)
			expectedContent, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read expected general file %s", testdataFile)

			// Fetch actual content from HAProxy
			actualContent, err := client.GetGeneralFileContent(ctx, filename)
			require.NoError(t, err, "failed to get general file %s from HAProxy", filename)

			// Compare
			assert.Equal(t, string(expectedContent), actualContent,
				"general file %s content mismatch", filename)
			t.Logf("  ✓ General file %s matches expected content", filename)
		}
	}

	if len(tc.verifySSLCertificates) > 0 {
		t.Logf("Verifying %d SSL certificates", len(tc.verifySSLCertificates))
		for certName, testdataFile := range tc.verifySSLCertificates {
			// Load expected content from testdata
			fullPath := filepath.Join("testdata", testdataFile)
			expectedContent, err := os.ReadFile(fullPath)
			require.NoError(t, err, "failed to read expected SSL cert %s", testdataFile)

			// Fetch actual content from HAProxy
			actualContent, err := client.GetSSLCertificateContent(ctx, certName)
			require.NoError(t, err, "failed to get SSL cert %s from HAProxy", certName)

			// Compare
			assert.Equal(t, string(expectedContent), actualContent,
				"SSL certificate %s content mismatch", certName)
			t.Logf("  ✓ SSL certificate %s matches expected content", certName)
		}
	}
}
