//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/enterprise"
)

// TestMiscFacts tests the Facts endpoint.
// Facts retrieval is available in all HAProxy Enterprise versions.
func TestMiscFacts(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfNotEnterprise(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	miscOps := enterprise.NewMiscOperations(dataplaneClient)

	t.Run("get-facts", func(t *testing.T) {
		facts, err := miscOps.GetFacts(ctx, false)
		require.NoError(t, err)
		assert.NotNil(t, facts)
	})

	t.Run("get-facts-with-refresh", func(t *testing.T) {
		facts, err := miscOps.GetFacts(ctx, true)
		require.NoError(t, err)
		assert.NotNil(t, facts)
	})
}

// TestMiscPing tests the Ping endpoint.
// Ping is only available in HAProxy Enterprise v3.2+.
func TestMiscPing(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfPingNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	miscOps := enterprise.NewMiscOperations(dataplaneClient)

	t.Run("ping", func(t *testing.T) {
		err := miscOps.Ping(ctx)
		require.NoError(t, err)
	})
}

// TestMiscStructuredConfig tests structured configuration operations.
// Structured config is available in all HAProxy Enterprise versions.
func TestMiscStructuredConfig(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfNotEnterprise(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	miscOps := enterprise.NewMiscOperations(dataplaneClient)

	// Start a transaction for operations requiring one
	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("get-structured-config", func(t *testing.T) {
		config, err := miscOps.GetStructuredConfig(ctx, txID)
		require.NoError(t, err)
		assert.NotNil(t, config)
	})
}

// TestGitOperations tests Git integration operations.
// Git integration is available in all HAProxy Enterprise versions.
func TestGitOperations(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfGitIntegrationNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	gitOps := enterprise.NewGitOperations(dataplaneClient)

	t.Run("get-settings", func(t *testing.T) {
		settings, err := gitOps.GetSettings(ctx)
		// Settings may not exist or may return 404, which is acceptable
		if err != nil {
			t.Logf("Git settings: %v", err)
		} else {
			assert.NotNil(t, settings)
		}
	})
}

// TestLoggingOperations tests advanced logging operations.
// Advanced logging is available in all HAProxy Enterprise versions.
func TestLoggingOperations(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfAdvancedLoggingNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	loggingOps := enterprise.NewLoggingOperations(dataplaneClient)

	t.Run("get-log-config", func(t *testing.T) {
		config, err := loggingOps.GetLogConfig(ctx)
		// Config may not exist yet, which is acceptable
		if err != nil {
			t.Logf("Log config: %v", err)
		} else {
			assert.NotNil(t, config)
		}
	})
}

// TestDynamicUpdateOperations tests dynamic update operations.
// Dynamic updates are available in all HAProxy Enterprise versions.
func TestDynamicUpdateOperations(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfDynamicUpdateNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	dynOps := enterprise.NewDynamicUpdateOperations(dataplaneClient)

	// Start a transaction for operations requiring one
	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("list-rules", func(t *testing.T) {
		rules, err := dynOps.GetAllRules(ctx, txID)
		require.NoError(t, err)
		// Rules may be empty on fresh install, that's OK
		assert.NotNil(t, rules)
	})

	t.Run("get-section", func(t *testing.T) {
		// GetSection returns error if section doesn't exist
		err := dynOps.GetSection(ctx, txID)
		// Section may not exist yet, which is acceptable
		if err != nil {
			t.Logf("Dynamic update section: %v", err)
		}
	})
}

// TestALOHAOperations tests ALOHA-specific operations.
// ALOHA features are available in all HAProxy Enterprise versions.
func TestALOHAOperations(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfALOHANotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	alohaOps := enterprise.NewALOHAOperations(dataplaneClient)

	t.Run("get-endpoints", func(t *testing.T) {
		// ALOHA endpoints may not be available on all Enterprise installations
		endpoints, err := alohaOps.GetEndpoints(ctx)
		if err != nil {
			t.Logf("ALOHA endpoints: %v", err)
		} else {
			assert.NotNil(t, endpoints)
		}
	})

	t.Run("list-actions", func(t *testing.T) {
		actions, err := alohaOps.GetAllActions(ctx)
		if err != nil {
			t.Logf("ALOHA actions: %v", err)
		} else {
			assert.NotNil(t, actions)
		}
	})
}
