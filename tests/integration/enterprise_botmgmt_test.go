//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/rekby/fixenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/dataplane/client/enterprise"
)

// TestBotManagementProfiles tests bot management profile operations.
// Bot management is available in all HAProxy Enterprise versions.
func TestBotManagementProfiles(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfBotManagementNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	botOps := enterprise.NewBotManagementOperations(dataplaneClient)

	// Start a transaction for operations requiring one
	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("list-profiles", func(t *testing.T) {
		profiles, err := botOps.GetAllProfiles(ctx, txID)
		require.NoError(t, err)
		// Profiles may be empty on fresh install, that's OK
		assert.NotNil(t, profiles)
	})
}

// TestBotManagementCaptchas tests CAPTCHA configuration operations.
// CAPTCHAs are available in all HAProxy Enterprise versions.
func TestBotManagementCaptchas(t *testing.T) {
	t.Parallel()
	env := fixenv.New(t)
	skipIfBotManagementNotSupported(t, env)

	ctx := context.Background()
	dataplaneClient := TestDataplaneClient(env)
	botOps := enterprise.NewBotManagementOperations(dataplaneClient)

	tx, err := StartTestTransaction(ctx, dataplaneClient)
	require.NoError(t, err)
	defer func() {
		_ = tx.Abort(ctx)
	}()

	txID := tx.ID

	t.Run("list-captchas", func(t *testing.T) {
		captchas, err := botOps.GetAllCaptchas(ctx, txID)
		require.NoError(t, err)
		// CAPTCHAs may be empty on fresh install, that's OK
		assert.NotNil(t, captchas)
	})
}
