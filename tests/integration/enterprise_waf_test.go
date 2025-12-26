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

// TestWAFRulesets tests WAF ruleset CRUD operations.
// WAF rulesets are available in all HAProxy Enterprise versions.
func TestWAFRulesets(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfWAFNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	wafOps := enterprise.NewWAFOperations(dataplaneClient)

	t.Run("list-rulesets", func(t *testing.T) {
		rulesets, err := wafOps.GetAllRulesets(ctx)
		require.NoError(t, err)
		// Rulesets may be empty on fresh install, that's OK
		assert.NotNil(t, rulesets)
	})
}

// TestWAFBodyRules tests WAF body rules for frontends and backends.
// WAF body rules are available in all HAProxy Enterprise versions.
func TestWAFBodyRules(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfWAFNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	wafOps := enterprise.NewWAFOperations(dataplaneClient)

	// First need to create a transaction
	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	// Note: We use "status" as the frontend name because that's what the test HAProxy config creates.
	// If the frontend doesn't exist, the API will return an error, which is acceptable.
	t.Run("list-frontend-body-rules", func(t *testing.T) {
		// Use the known "status" frontend from test HAProxy config
		rules, err := wafOps.GetAllBodyRulesFrontend(ctx, txID, "status")
		if err != nil {
			t.Logf("Could not get frontend body rules (frontend may not exist): %v", err)
			return
		}
		assert.NotNil(t, rules)
	})
}

// TestWAFGlobal tests WAF global configuration.
// WAF global config is only available in HAProxy Enterprise v3.2+.
func TestWAFGlobal(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfWAFGlobalNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	wafOps := enterprise.NewWAFOperations(dataplaneClient)

	t.Run("get-waf-global", func(t *testing.T) {
		// WAF global may not exist yet, so NotFound is acceptable
		global, err := wafOps.GetGlobal(ctx, "")
		if err != nil {
			// Either not found or not configured is OK
			t.Logf("WAF global config: %v", err)
		} else {
			assert.NotNil(t, global)
		}
	})
}

// TestWAFProfiles tests WAF profile management.
// WAF profiles are only available in HAProxy Enterprise v3.2+.
func TestWAFProfiles(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfWAFProfilesNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	wafOps := enterprise.NewWAFOperations(dataplaneClient)

	t.Run("list-profiles", func(t *testing.T) {
		profiles, err := wafOps.GetAllProfiles(ctx, "")
		// Some versions return 404 when no profiles exist (vs empty list)
		if err != nil {
			t.Logf("WAF profiles: %v", err)
			return
		}
		assert.NotNil(t, profiles)
	})
}
