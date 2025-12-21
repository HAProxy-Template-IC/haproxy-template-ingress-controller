//go:build integration

// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigSyncIdempotencyWithComments tests that syncing a config with inline comments
// twice using the high-level Sync API results in zero operations on the second sync.
//
// This is a regression test for the false positive updates bug where metadata format
// mismatch (flat vs nested) caused spurious updates on every reconciliation.
//
// Root cause: When template renders config with plain comments (e.g., "# Pod: echo-server"),
// the parser creates flat metadata: {"comment": "Pod: echo-server"}. After sync,
// the API stores it as JSON: {"comment":{"value":"Pod: echo-server"}}. On next sync,
// parsing the API config returns nested metadata, while the desired config has flat
// metadata - causing false positive updates.
//
// IMPORTANT: This test uses the high-level Sync API for BOTH syncs to reproduce the bug.
// Using PushRawConfiguration for the initial sync would not trigger the metadata
// transformation that causes the format mismatch.
func TestConfigSyncIdempotencyWithComments(t *testing.T) {
	testCases := []struct {
		name       string
		configFile string
	}{
		{
			name:       "server-with-comment-idempotent",
			configFile: "idempotency/server-with-comment.cfg",
		},
		{
			name:       "acl-with-comment-idempotent",
			configFile: "idempotency/acl-with-comment.cfg",
		},
		{
			name:       "http-response-rule-idempotent",
			configFile: "idempotency/http-response-rule.cfg",
		},
	}

	for _, tt := range testCases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runIdempotencyTest(t, tt.configFile)
		})
	}
}

// runIdempotencyTest runs a sync idempotency test using the high-level Sync API for both syncs.
// This properly reproduces the metadata format mismatch bug.
func runIdempotencyTest(t *testing.T, configFile string) {
	env := fixenv.New(t)
	ctx := context.Background()

	// Skip on DataPlane API v3.0/v3.1 - Metadata field on config models (ACL, Server, etc.)
	// is only available in v3.2+. On earlier versions, comments are lost during API round-trip,
	// causing false positive updates that are expected behavior for those versions.
	skipIfConfigMetadataNotSupported(t, env)

	// Request fixtures
	client := TestDataplaneClient(env)            // Low-level client for verification
	dpClient := TestDataplaneHighLevelClient(env) // High-level client for Sync API

	// Load test config
	configContent := LoadTestConfig(t, configFile)
	t.Logf("Loaded config: %s", configFile)

	// Step 1: First sync - pushes config via high-level Sync API
	// This triggers TransformClientMetadataInJSON which converts flat→nested metadata
	result1, err := dpClient.Sync(ctx, configContent, nil, nil)
	require.NoError(t, err, "first sync should succeed")
	t.Logf("First sync completed: %d operations, reload=%v",
		len(result1.AppliedOperations), result1.ReloadTriggered)
	t.Logf("First sync details: creates=%d, updates=%d, deletes=%d",
		result1.Details.Creates, result1.Details.Updates, result1.Details.Deletes)
	for _, op := range result1.AppliedOperations {
		t.Logf("  First sync op: %s", op.Description)
	}

	// Step 2: Wait for config to be applied
	// After first sync, HAProxy config has JSON metadata comments like:
	// # {"comment":{"value":"Pod: echo-server"}}
	err = WaitForCondition(ctx, FastWaitConfig(), func(ctx context.Context) (bool, error) {
		currentConfig, err := client.GetRawConfiguration(ctx)
		if err != nil {
			return false, nil
		}
		// Check that the config was applied (default "frontend status" is gone)
		return !strings.Contains(currentConfig, "frontend status"), nil
	})
	require.NoError(t, err, "config should be applied within timeout")
	t.Logf("Config applied successfully")

	// Optional: Log the actual config to verify comments are stored as JSON
	currentConfig, err := client.GetRawConfiguration(ctx)
	require.NoError(t, err, "should be able to read current config")
	// Look for evidence of JSON metadata in comments
	if strings.Contains(currentConfig, `{"comment":{"value":`) {
		t.Logf("Verified: Config contains JSON metadata comments")
	}
	// Log if config contains http-response rules for debugging
	if strings.Contains(currentConfig, "http-response") {
		t.Logf("Config contains http-response rules")
		// Extract and log the backend section
		if idx := strings.Index(currentConfig, "backend "); idx != -1 {
			end := idx + 500
			if end > len(currentConfig) {
				end = len(currentConfig)
			}
			t.Logf("Backend section (first 500 chars):\n%s", currentConfig[idx:end])
		}
	}

	// Step 3: Second sync - syncs SAME config again via high-level Sync API
	// This is where the bug manifests: current config has nested metadata,
	// desired config has flat metadata → false positive updates
	result2, err := dpClient.Sync(ctx, configContent, nil, nil)
	require.NoError(t, err, "second sync should succeed")
	t.Logf("Second sync completed: %d operations (creates=%d, updates=%d, deletes=%d)",
		len(result2.AppliedOperations),
		result2.Details.Creates,
		result2.Details.Updates,
		result2.Details.Deletes)

	// Step 4: Assert zero operations on second sync (idempotency check)
	// If metadata format mismatch exists, this will fail with false positive updates
	assert.Equal(t, 0, result2.Details.Creates,
		"idempotent sync should have 0 creates")
	assert.Equal(t, 0, result2.Details.Updates,
		"idempotent sync should have 0 updates (metadata format mismatch bug?)")
	assert.Equal(t, 0, result2.Details.Deletes,
		"idempotent sync should have 0 deletes")

	if result2.Details.Creates > 0 || result2.Details.Updates > 0 || result2.Details.Deletes > 0 {
		t.Logf("⚠️  False positive operations detected! Operations:")
		for _, op := range result2.AppliedOperations {
			t.Logf("  - %s", op.Description)
		}
	}

	if result2.Details.Creates == 0 && result2.Details.Updates == 0 && result2.Details.Deletes == 0 {
		t.Logf("✓ Idempotency check passed: second sync had zero operations")
	}
}
